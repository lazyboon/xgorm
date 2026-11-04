package xgorm

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net"
	"sync"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"
	"gorm.io/driver/sqlserver"
)

// sqlServerConnector cancels login sockets without changing runtime socket timeouts.
// DNS resolution and protocol selection retain the native driver's behavior.
type sqlServerConnector struct {
	dsn       string
	driver    driver.Driver
	keepAlive time.Duration
}

var _ driver.Connector = (*sqlServerConnector)(nil)

// newSQLServerConnection uses a context-aware connector for the native SQL Server dialect.
func newSQLServerConnection(dsn string) (*driverConnection, error) {
	config, err := msdsn.Parse(dsn)
	if err != nil {
		return nil, err
	}
	keepAlive := config.KeepAlive
	if keepAlive == 0 {
		keepAlive = 30 * time.Second // Match the native driver's default for an unspecified keep-alive value.
	}
	pool := sql.OpenDB(&sqlServerConnector{
		dsn:       dsn,
		driver:    mssql.NewConnectorConfig(config).Driver(),
		keepAlive: keepAlive,
	})
	return &driverConnection{
		dialect: sqlserver.New(sqlserver.Config{DSN: dsn, Conn: pool}),
		pool:    pool,
	}, nil
}

// Driver returns the native SQL Server driver used by this connector.
func (c *sqlServerConnector) Driver() driver.Driver {
	return c.driver
}

// Connect closes in-progress login sockets on cancellation and detaches that cancellation after success.
func (c *sqlServerConnector) Connect(ctx context.Context) (driver.Conn, error) {
	// Each attempt owns its native configuration and socket scope, including failover settings.
	native, err := mssql.NewConnector(c.dsn)
	if err != nil {
		return nil, err
	}
	dialer := &sqlServerDialer{KeepAlive: c.keepAlive}
	native.Dialer = dialer
	canceled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		// finish retains close errors for the synchronous return path below.
		_ = dialer.finish(true)
		close(canceled)
	})
	connection, err := native.Connect(ctx)
	if !stop() {
		// Wait for an already-started callback so it cannot close a connection after we return it.
		<-canceled
	}
	err = errors.Join(err, ctx.Err())
	if err != nil {
		// Close sockets before asking the driver to clean up an incomplete session.
		err = errors.Join(err, dialer.finish(true))
		// The native connector can return an interface containing a nil *mssql.Conn on failure.
		if nativeConnection, ok := connection.(*mssql.Conn); ok && nativeConnection != nil {
			err = errors.Join(err, nativeConnection.Close())
		}
		return nil, err
	}
	if err = dialer.finish(false); err != nil {
		return nil, errors.Join(err, connection.Close())
	}
	return connection, nil
}

// sqlServerDialer tracks sockets only while their login attempt is in progress.
type sqlServerDialer struct {
	net.Dialer

	mu          sync.Mutex
	connections []net.Conn
	finished    bool
	closeErr    error
}

var _ mssql.Dialer = (*sqlServerDialer)(nil)

// DialContext tracks a newly opened socket or closes it if its login scope has already ended.
func (d *sqlServerDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	connection, err := d.Dialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	if d.finished {
		d.mu.Unlock()
		return nil, errors.Join(context.Canceled, connection.Close())
	}
	d.connections = append(d.connections, connection)
	d.mu.Unlock()
	return connection, nil
}

// finish ends socket tracking and optionally closes sockets from a failed or canceled login.
func (d *sqlServerDialer) finish(closeConnections bool) error {
	d.mu.Lock()
	if d.finished {
		err := d.closeErr
		d.mu.Unlock()
		return err
	}
	d.finished = true
	connections := d.connections
	d.connections = nil
	d.mu.Unlock()

	var closeErrors []error
	if closeConnections {
		for _, connection := range connections {
			if err := connection.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				closeErrors = append(closeErrors, err)
			}
		}
	}
	d.mu.Lock()
	d.closeErr = errors.Join(closeErrors...)
	err := d.closeErr
	d.mu.Unlock()
	return err
}
