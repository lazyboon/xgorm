package xgorm

import "testing"

func TestNewSQLiteConnectionOwnsUnopenedPool(t *testing.T) {
	t.Parallel()
	connection, err := newSQLiteConnection(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer connection.pool.Close()
	if connection.dialect.Name() != "sqlite" || connection.pool.Stats().OpenConnections != 0 {
		t.Fatal("factory did not return a lazy SQLite pool")
	}
	if err = connection.pool.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
}
