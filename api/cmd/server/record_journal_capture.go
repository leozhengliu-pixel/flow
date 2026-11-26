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
	"syscall"

	appconfig "flow/api/internal/config"
	"flow/api/internal/store"
)

// runRecordJournalCaptureCommand is `server record-journal-capture ACTION`,
// run against the same configuration as the server (MySQL journal capture,
// docs/bounded-metadata-activation.md):
//
//	status         print the capture this configuration uses and the
//	               database's recorded capture, triggers and gaps
//	resync         schedule a journal repair of every workspace (after
//	               canonical rows were changed with SQL outside Flow)
//	drop-triggers  remove the journal triggers once every replica captures
//	               in the application (needs the TRIGGER grant)
//
// It prints a JSON report and exits 0, or 2 on an error.
func runRecordJournalCaptureCommand(args []string) int {
	return recordJournalCaptureCommand(args, os.Stdout, os.Stderr)
}

func recordJournalCaptureCommand(args []string, stdout, stderr io.Writer) int {
	previousLogger, previousOutput := slog.Default(), log.Writer()
	slog.SetDefault(slog.New(slog.NewTextHandler(stderr, nil)))
	log.SetOutput(stderr)
	defer func() {
		slog.SetDefault(previousLogger)
		log.SetOutput(previousOutput)
	}()
	if len(args) == 0 || args[0] != "status" && args[0] != "resync" && args[0] != "drop-triggers" {
		fmt.Fprintln(stderr, "usage: server record-journal-capture status|resync|drop-triggers [--config FILE]")
		return 2
	}
	action := args[0]
	if _, err := appconfig.LoadFileFromArgs(args[1:]); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	configuration, err := appconfig.Load()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if configuration.Database.RecordJournalMode() == "off" {
		fmt.Fprintln(stderr, "record-journal-capture: the record journal is off in this configuration")
		return 2
	}
	// Maintenance never serves bounded metadata or runs the catalog worker.
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
	report := map[string]any{"action": action}
	switch action {
	case "resync":
		var count int
		if count, err = repository.ScheduleRecordJournalResync(ctx); err == nil {
			report["workspaces"] = count
		}
	case "drop-triggers":
		err = repository.DropRecordJournalTriggers(ctx)
	}
	if err == nil {
		var status store.RecordJournalCaptureStatus
		status, err = repository.RecordJournalCaptureStatus(ctx)
		report["status"] = status
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(report)
	if err != nil {
		fmt.Fprintf(stderr, "record-journal-capture %s: %v\n", action, err)
		return 2
	}
	return 0
}
