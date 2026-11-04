package xgorm

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
)

// fakePostgres accepts startup and Ping messages without contacting an existing database.
type fakePostgres struct {
	listener    net.Listener
	startups    chan map[string]string
	wait        sync.WaitGroup
	connections []net.Conn
	acceptDone  chan struct{}
}

func newFakePostgres(t *testing.T) *fakePostgres {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &fakePostgres{listener: listener, startups: make(chan map[string]string, 16), acceptDone: make(chan struct{})}
	server.wait.Go(func() {
		defer close(server.acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			server.connections = append(server.connections, conn)
			server.wait.Go(func() { server.serve(conn) })
		}
	})
	t.Cleanup(func() {
		listener.Close()
		<-server.acceptDone
		for _, conn := range server.connections {
			_ = conn.Close()
		}
		server.wait.Wait()
	})
	return server
}
func (s *fakePostgres) serve(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	var length uint32
	if binary.Read(conn, binary.BigEndian, &length) != nil {
		return
	}
	startup := make([]byte, length-4)
	if _, err := io.ReadFull(conn, startup); err != nil {
		return
	}
	parts := strings.Split(string(startup[4:]), "\x00")
	params := make(map[string]string)
	for i := 0; i+1 < len(parts) && parts[i] != ""; i += 2 {
		params[parts[i]] = parts[i+1]
	}
	s.startups <- params
	sendMessage(conn, 'R', []byte{0, 0, 0, 0})
	sendMessage(conn, 'Z', []byte{'I'})
	for {
		header := make([]byte, 5)
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}
		body := make([]byte, binary.BigEndian.Uint32(header[1:])-4)
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}
		switch header[0] {
		case 'X':
			return
		case 'Q':
			sendMessage(conn, 'C', []byte("SELECT 1\x00"))
			sendMessage(conn, 'Z', []byte{'I'})
		default:
			return
		}
	}
}
func sendMessage(conn net.Conn, kind byte, body []byte) {
	message := make([]byte, 5+len(body))
	message[0] = kind
	binary.BigEndian.PutUint32(message[1:5], uint32(4+len(body)))
	copy(message[5:], body)
	conn.Write(message)
}

func TestPostgresParsedTimezone(t *testing.T) {
	type timezoneCase struct {
		name, options, zone string
		uri                 bool
	}
	for _, tc := range []timezoneCase{
		{name: "quoted", options: "timezone='Asia/Shanghai'", zone: "Asia/Shanghai"},
		{name: "uri_encoded", options: "timezone=Asia%2FShanghai", zone: "Asia/Shanghai", uri: true},
		{name: "password_decoy", options: "password='timezone=Invalid/Decoy' timezone=UTC", zone: "UTC"},
		{name: "password_only", options: "password='timezone=Invalid/Decoy'"},
		{name: "mixed_case_alias", options: "TimeZone='Asia/Shanghai'", zone: "Asia/Shanghai"},
		{name: "underscore_alias", options: "time_zone='Asia/Shanghai'", zone: "Asia/Shanghai"},
		{name: "matching_aliases", options: "TimeZone=UTC time_zone=UTC", zone: "UTC"},
		{name: "canonical_wins", options: "timezone=UTC TimeZone=Asia/Shanghai time_zone=Europe/Berlin", zone: "UTC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := newFakePostgres(t)
			dsn := "host=127.0.0.1 port=" + strings.Split(server.listener.Addr().String(), ":")[1] + " user=audit dbname=audit sslmode=disable " + tc.options
			if tc.uri {
				dsn = (&url.URL{Scheme: "postgres", Host: server.listener.Addr().String(), User: url.User("audit"), Path: "/audit", RawQuery: "sslmode=disable&" + tc.options}).String()
			}
			startup, cancel := context.WithCancel(context.Background())
			defer cancel()
			config := Config{Alias: tc.name, Driver: DriverPostgres, DSN: dsn, PingTimeout: 1, MaxOpenConns: 1, MaxIdleConns: 1}
			if err := Init(startup, []Config{config}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := Close(tc.name); err != nil {
					t.Error(err)
				}
			})
			var params map[string]string
			select {
			case params = <-server.startups:
			case <-time.After(time.Second):
				t.Fatal("startup message not received within 1s")
			}
			if params["timezone"] != tc.zone {
				t.Errorf("wire timezone = %q, want %q", params["timezone"], tc.zone)
			}
			for _, alias := range []string{"TimeZone", "time_zone"} {
				if _, ok := params[alias]; ok {
					t.Errorf("wire retained alias %q", alias)
				}
			}
			db := Get(tc.name)
			pool, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			conn, err := pool.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			err = conn.Raw(func(native any) error {
				typ, ok := native.(*stdlib.Conn).Conn().TypeMap().TypeForOID(pgtype.TimestampOID)
				if !ok {
					t.Fatal("timestamp codec missing")
				}
				location := typ.Codec.(*pgtype.TimestampCodec).ScanLocation
				if tc.zone == "" {
					if location != nil {
						t.Errorf("implicit codec location=%v", location)
					}
				} else if location == nil || location.String() != tc.zone {
					t.Errorf("codec location=%v, want %q", location, tc.zone)
				}
				return nil
			})
			conn.Close()
			if err != nil {
				t.Fatal(err)
			}
			cancel()
			if err := db.Statement.Context.Err(); err != nil {
				t.Fatalf("startup context retained: %v", err)
			}
			runtimeCtx, runtimeCancel := context.WithTimeout(context.Background(), time.Second)
			defer runtimeCancel()
			if err := pool.PingContext(runtimeCtx); err != nil {
				t.Fatalf("ping after startup cancellation: %v", err)
			}
		})
	}
}
func TestPostgresConflictingAliases(t *testing.T) {
	server := newFakePostgres(t)
	dsn := "host=127.0.0.1 port=" + strings.Split(server.listener.Addr().String(), ":")[1] + " user=audit dbname=audit sslmode=disable TimeZone=UTC time_zone=Asia/Shanghai"
	config := Config{Alias: "conflict", Driver: DriverPostgres, DSN: dsn, PingTimeout: 1, MaxOpenConns: 1, MaxIdleConns: 1}
	err := Init(context.Background(), []Config{config})
	defer Close("conflict")
	if err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("want explicit conflicting aliases error, got %v", err)
	}
	select {
	case params := <-server.startups:
		t.Fatalf("opened connection for conflicting config: %v", params)
	default:
	}
}

func TestPostgresInvalidTimezone(t *testing.T) {
	server := newFakePostgres(t)
	dsn := "host=127.0.0.1 port=" + strings.Split(server.listener.Addr().String(), ":")[1] + " user=example dbname=example sslmode=disable timezone=Invalid/Example"
	config := Config{Alias: "invalid_zone", Driver: DriverPostgres, DSN: dsn, PingTimeout: 1, MaxOpenConns: 1, MaxIdleConns: 1}
	if err := Init(t.Context(), []Config{config}); err == nil {
		t.Fatal("invalid timestamp scan location accepted")
	}
	if Get(config.Alias) != nil {
		t.Fatal("failed initialization registered")
	}
}
