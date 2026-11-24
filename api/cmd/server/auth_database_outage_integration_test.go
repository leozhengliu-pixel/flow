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
	config, id := startOutageDatabase(t, dialect, "flow-codex-db-outage-"+dialect)
	return config, func() { runOutageDocker(t, "stop", "--time", "1", id) }
}

func runOutageDocker(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, "docker", args...).Output()
	if err != nil {
		t.Fatalf("Docker %s failed: %v", args[0], err)
	}
	return strings.TrimSpace(string(raw))
}

const outageDatabaseName = "flow_outage"
const outageDatabasePassword = "disposable-test-password"

func startOutageDatabase(t *testing.T, dialect, name string) (store.DatabaseConfig, string) {
	t.Helper()
	image, port := "postgres:17", "5432"
	env := []string{"-e", "POSTGRES_DB=" + outageDatabaseName, "-e", "POSTGRES_PASSWORD=" + outageDatabasePassword}
	if dialect == "mysql" {
		image, port = "mysql:8.4", "3306"
		env = []string{"-e", "MYSQL_DATABASE=" + outageDatabaseName, "-e", "MYSQL_ROOT_PASSWORD=" + outageDatabasePassword, "-e", "MYSQL_ROOT_HOST=%"}
	}
	args := []string{"run", "-d", "--pull=never", "--name", name, "-p", "127.0.0.1::" + port}
	id := runOutageDocker(t, append(append(args, env...), image)...)
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
	config := outageDatabaseConfig(t, dialect, id)
	waitForOutageDatabase(t, openOutageVerificationDB(t, config))
	return config, id
}

func outageDatabaseConfig(t *testing.T, dialect, id string) store.DatabaseConfig {
	t.Helper()
	port := "5432"
	if dialect == "mysql" {
		port = "3306"
	}
	address := runOutageDocker(t, "port", id, port+"/tcp")
	if host, _, err := net.SplitHostPort(address); err != nil || host != "127.0.0.1" {
		t.Fatal("test database has no loopback port")
	}
	var dsn string
	if dialect == "mysql" {
		config := mysql.NewConfig()
		config.User, config.Passwd, config.Net, config.Addr, config.DBName = "root", outageDatabasePassword, "tcp", address, outageDatabaseName
		config.Timeout = 2 * time.Second
		config.ReadTimeout, config.WriteTimeout = 5*time.Second, 5*time.Second
		dsn = config.FormatDSN()
	} else {
		dsn = (&url.URL{Scheme: "postgres", User: url.UserPassword("postgres", outageDatabasePassword),
			Host: address, Path: outageDatabaseName, RawQuery: "sslmode=disable&connect_timeout=2"}).String()
	}
	return store.DatabaseConfig{Driver: dialect, URL: dsn, MaxOpenConns: 4}
}

func waitForOutageDatabase(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	for db.PingContext(ctx) != nil {
		select {
		case <-ctx.Done():
			t.Fatal("test database did not become ready")
		case <-time.After(250 * time.Millisecond):
		}
	}
}
