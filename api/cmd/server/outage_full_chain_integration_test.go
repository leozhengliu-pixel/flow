//go:build integration

package main

import (
	"context"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"flow/api/internal/store"

	"github.com/go-sql-driver/mysql"
)

func TestFullChainDatabaseOutageExternal(t *testing.T) {
	// Run under heavy.sh and lockf /tmp/flow-rig.lock.
	if os.Getenv("FLOW_TEST_DB_OUTAGE_DOCKER") != "1" {
		t.Skip("requires explicitly enabling disposable Docker databases")
	}
	for _, dialect := range []string{"mysql", "postgres"} {
		for _, bounded := range []bool{false, true} {
			t.Run(dialect+"/bounded="+strconv.FormatBool(bounded), func(t *testing.T) {
				config, id := startOutageDatabase(t, dialect, "flow-codex-outage-403-"+dialect)
				proxyConfig, drop, restore := proxyOutageDatabase(t, config)
				repo := openOutageStore(t, proxyConfig, bounded)
				handlers := newOutageMutationHandlers(t, repo)
				cases := fullChainOutageFixture(t, repo, handlers.authenticated)
				db := openOutageVerificationDB(t, config)
				ready := checkHealthyOutageMutations(t, db, handlers, cases)

				t.Run("paused", func(t *testing.T) {
					runOutageDocker(t, "pause", id)
					defer runOutageDocker(t, "unpause", id)
					checkFullChainOutage(t, handlers, cases, 250*time.Millisecond, true)
				})
				waitForOutageDatabase(t, db)
				if outageMutationCounts(t, db) != ready {
					t.Fatal("a deadline request persisted a mutation")
				}

				t.Run("dropped-network", func(t *testing.T) {
					drop()
					defer restore()
					checkFullChainOutage(t, handlers, cases, 5*time.Second, false)
					if outageMutationCounts(t, db) != ready {
						t.Fatal("a disconnected request persisted a mutation")
					}
					restore()
					t.Run("after-authorization", func(t *testing.T) {
						checkWriterOutage(t, handlers, cases, bounded, 5*time.Second, func(context.Context) { drop() }, restore)
					})
					if outageMutationCounts(t, db) != ready {
						t.Fatal("a disconnected writer persisted a mutation")
					}
				})

				t.Run("stopped", func(t *testing.T) {
					runOutageDocker(t, "stop", "--time", "10", id)
					checkFullChainOutage(t, handlers, cases, 5*time.Second, false)
					runOutageDocker(t, "start", id)
					db := openOutageVerificationDB(t, outageDatabaseConfig(t, dialect, id))
					waitForOutageDatabase(t, db)
					if outageMutationCounts(t, db) != ready {
						t.Fatal("a stopped database request persisted a mutation")
					}
				})
			})
		}
	}
}

// proxyOutageDatabase drops established TCP streams and accepts then closes
// new connections. The database itself stays available to the verifier.
func proxyOutageDatabase(t *testing.T, config store.DatabaseConfig) (store.DatabaseConfig, func(), func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("could not listen for database proxy connections")
	}
	t.Cleanup(func() { _ = listener.Close() })
	var upstream string
	if config.Driver == "mysql" {
		dsn, err := mysql.ParseDSN(config.URL)
		if err != nil {
			t.Fatal("could not configure the MySQL proxy")
		}
		upstream, dsn.Addr = dsn.Addr, listener.Addr().String()
		config.URL = dsn.FormatDSN()
	} else {
		dsn, err := url.Parse(config.URL)
		if err != nil {
			t.Fatal("could not configure the PostgreSQL proxy")
		}
		upstream, dsn.Host = dsn.Host, listener.Addr().String()
		config.URL = dsn.String()
	}
	var mu sync.Mutex
	var dropped bool
	streams := map[[2]net.Conn]struct{}{}
	drop := func() {
		mu.Lock()
		defer mu.Unlock()
		dropped = true
		for pair := range streams {
			_ = pair[0].Close()
			_ = pair[1].Close()
		}
	}
	restore := func() {
		mu.Lock()
		defer mu.Unlock()
		dropped = false
	}
	t.Cleanup(drop)
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			if dropped {
				mu.Unlock()
				_ = client.Close()
				continue
			}
			peer, err := net.DialTimeout("tcp", upstream, time.Second)
			if err != nil {
				mu.Unlock()
				_ = client.Close()
				continue
			}
			pair := [2]net.Conn{client, peer}
			streams[pair] = struct{}{}
			mu.Unlock()
			go func() {
				done := make(chan struct{})
				go func() {
					_, _ = io.Copy(peer, client)
					_ = peer.Close()
					close(done)
				}()
				_, _ = io.Copy(client, peer)
				_ = client.Close()
				<-done
				mu.Lock()
				delete(streams, pair)
				mu.Unlock()
			}()
		}
	}()
	return config, drop, restore
}
