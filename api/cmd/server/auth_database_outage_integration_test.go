//go:build integration

package main

import (
	"context"
	"database/sql"
	"encoding/hex"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"flow/api/internal/store"

	"github.com/go-sql-driver/mysql"
)

func TestExternalDatabaseAuthOutage(t *testing.T) {
	// This test stops its own containers. Run it under the shared rig lock
	// and heavy.sh with FLOW_TEST_DB_OUTAGE_DOCKER=1.
	if os.Getenv("FLOW_TEST_DB_OUTAGE_DOCKER") != "1" {
		t.Skip("requires explicitly enabling disposable Docker databases")
	}
	for _, dialect := range []string{"mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			config, disconnect := startAuthOutageDatabase(t, dialect)
			repo := openOutageStore(t, config, true)
			exerciseAuthDatabaseOutage(t, repo, disconnect)
		})
	}
}

func startAuthOutageDatabase(t *testing.T, dialect string) (store.DatabaseConfig, func()) {
	t.Helper()
	const database, password = "flow_outage", "disposable-test-password"
	image, port, sqlDriver := "postgres:17", "5432", "pgx"
	env := []string{"-e", "POSTGRES_DB=" + database, "-e", "POSTGRES_PASSWORD=" + password}
	if dialect == "mysql" {
		image, port, sqlDriver = "mysql:8.4", "3306", "mysql"
		env = []string{"-e", "MYSQL_DATABASE=" + database, "-e", "MYSQL_ROOT_PASSWORD=" + password, "-e", "MYSQL_ROOT_HOST=%"}
	}
	docker := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		raw, err := exec.CommandContext(ctx, "docker", args...).Output()
		if err != nil {
			t.Fatalf("Docker %s failed: %v", args[0], err)
		}
		return strings.TrimSpace(string(raw))
	}
	args := []string{"run", "-d", "--pull=never", "--name", "flow-codex-db-outage-" + dialect, "-p", "127.0.0.1::" + port}
	id := docker(append(append(args, env...), image)...)
	if _, err := hex.DecodeString(id); err != nil || len(id) != 64 {
		t.Fatal("Docker did not return a container ID")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := exec.CommandContext(ctx, "docker", "rm", "-f", "-v", id).Run(); err != nil {
			t.Errorf("could not remove test container: %v", err)
		}
	})
	address := docker("port", id, port+"/tcp")
	if host, _, err := net.SplitHostPort(address); err != nil || host != "127.0.0.1" {
		t.Fatal("test database has no loopback port")
	}
	var dsn string
	if dialect == "mysql" {
		config := mysql.NewConfig()
		config.User, config.Passwd, config.Net, config.Addr, config.DBName = "root", password, "tcp", address, database
		config.Timeout = 2 * time.Second
		dsn = config.FormatDSN()
	} else {
		dsn = (&url.URL{Scheme: "postgres", User: url.UserPassword("postgres", password),
			Host: address, Path: database, RawQuery: "sslmode=disable&connect_timeout=2"}).String()
	}

	db, err := sql.Open(sqlDriver, dsn)
	if err != nil {
		t.Fatal("could not configure test database")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	for db.PingContext(ctx) != nil {
		select {
		case <-ctx.Done():
			t.Fatal("test database did not become ready")
		case <-time.After(250 * time.Millisecond):
		}
	}
	return store.DatabaseConfig{Driver: dialect, URL: dsn, MaxOpenConns: 4}, func() {
		docker("stop", "--time", "1", id)
	}
}
