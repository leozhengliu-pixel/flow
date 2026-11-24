package store

import (
	"database/sql"
	"io"
	"net"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgproto3"
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
	config.BuildFrontend = func(r io.Reader, w io.Writer) *pgproto3.Frontend {
		return pgproto3.NewFrontend(postgresProtocolReader{r}, w)
	}
	return stdlib.OpenDB(*config), nil
}

type postgresProtocolReader struct{ io.Reader }

func (r postgresProtocolReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		// Keep the transport origin when pgx would return an unattributed
		// short read. Other short reads, such as decoding stored data, differ.
		return n, &net.OpError{Op: "read", Err: err}
	}
	return n, err
}
