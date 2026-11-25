package store

import (
	"context"
	"database/sql"
	"io"
	"net"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func openDatabasePool(driver, dsn string) (*sql.DB, error) {
	if driver != "pgx" {
		return sql.Open(driver, dsn)
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	dial := config.DialFunc
	config.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dial(ctx, network, address)
		if err != nil {
			return conn, err
		}
		return postgresTransport(conn), nil
	}
	return stdlib.OpenDB(*config), nil
}

// postgresTransport keeps the transport origin of a closed PostgreSQL
// connection. The net package reports a peer close as a bare io.EOF, which pgx
// returns as an unattributed short read; other short reads, such as decoding
// stored data, are not connection failures. TLS runs above this connection.
func postgresTransport(conn net.Conn) net.Conn {
	switch c := conn.(type) {
	case *net.TCPConn:
		return postgresTCPConn{c}
	case *net.UnixConn:
		return postgresUnixConn{c}
	}
	return conn
}

type postgresTCPConn struct{ *net.TCPConn }

func (c postgresTCPConn) Read(p []byte) (int, error) {
	n, err := c.TCPConn.Read(p)
	return n, postgresReadError(err)
}

type postgresUnixConn struct{ *net.UnixConn }

func (c postgresUnixConn) Read(p []byte) (int, error) {
	n, err := c.UnixConn.Read(p)
	return n, postgresReadError(err)
}

func postgresReadError(err error) error {
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return &net.OpError{Op: "read", Net: "postgres", Err: err}
	}
	return err
}
