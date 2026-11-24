package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	appconfig "flow/api/internal/config"
	"flow/api/internal/store"
)

// runMetadataPrepareCommand is `server metadata-prepare`: the one-shot
// maintenance a FLOW_BOUNDED_METADATA=on start needs, run against the same
// configuration as the server. It repairs the record journal's writer
// coverage of every workspace and completes the metadata catalog of every
// collection (store.PrepareBoundedMetadata), prints a JSON report and exits
// 0 when a bounded start would find everything covered, 1 when something is
// still incomplete (the report says what) and 2 on an error. It opens the
// database flag-off whatever FLOW_BOUNDED_METADATA says, runs next to
// serving flag-off replicas, and can be interrupted and run again.
func runMetadataPrepareCommand(args []string) int {
	return metadataPrepare(args, os.Stdout, os.Stderr)
}

func metadataPrepare(args []string, stdout, stderr io.Writer) int {
	// Progress goes to stderr; stdout carries only the JSON report.
	previousLogger, previousOutput, previousFlags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(stderr, nil)))
	log.SetOutput(stderr)
	defer func() {
		slog.SetDefault(previousLogger)
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
	}()
	if _, err := appconfig.LoadFileFromArgs(args); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	configuration, err := appconfig.Load()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if configuration.Database.RecordJournalMode() == "off" {
		fmt.Fprintln(stderr, "metadata-prepare: set FLOW_RECORD_JOURNAL=on (as on the replicas) first; the repair and the catalog need the record journal")
		return 2
	}
	// This process prepares the flag; it never serves bounded, and it runs
	// the catalog pass itself rather than in a background worker.
	_ = os.Setenv("FLOW_BOUNDED_METADATA", "off")
	_ = os.Setenv("FLOW_METADATA_CATALOG", "off")
	repository, err := store.OpenDatabase(configuration.Database)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer repository.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var report store.BoundedMetadataPreparation
	err = repository.WithStartupLock(ctx, "metadata preparation", func(ctx context.Context) error {
		var err error
		report, err = repository.PrepareBoundedMetadata(ctx)
		return err
	})
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(report)
	if err != nil {
		fmt.Fprintf(stderr, "metadata-prepare: %v (progress is kept; run it again to continue)\n", err)
		return 2
	}
	if !report.Ready {
		fmt.Fprintf(stderr, "metadata-prepare: %d workspaces still need the journal repair and %d collections the catalog; see the report\n", report.UncoveredWorkspaces, report.IncompleteCollections)
		for _, gap := range report.Uncovered {
			scopes := make([]string, 0, len(gap.Pending))
			for _, scope := range gap.Pending {
				scopes = append(scopes, fmt.Sprintf("%s from %s: %d keys", scope.Field, scope.Source, scope.Keys))
			}
			fmt.Fprintf(stderr, "metadata-prepare: workspace key %q (workspace exists: %t, activation walk unfinished: %t): %s\n", gap.Workspace, gap.WorkspaceExists, gap.ActivationWalk, strings.Join(scopes, "; "))
		}
		return 1
	}
	return 0
}
