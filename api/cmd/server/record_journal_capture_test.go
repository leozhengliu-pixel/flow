package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"flow/api/internal/store"
)

func TestRecordJournalCaptureCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := recordJournalCaptureCommand([]string{"rebuild"}, &stdout, &stderr); code != 2 {
		t.Fatalf("unknown action exited %d", code)
	}
	t.Setenv("FLOW_DATABASE_DRIVER", "sqlite")
	t.Setenv("FLOW_DATABASE_PATH", filepath.Join(t.TempDir(), "capture.db"))
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, action := range []string{"status", "resync"} {
		stdout.Reset()
		if code := recordJournalCaptureCommand([]string{action}, &stdout, &stderr); code != 0 {
			t.Fatalf("%s exited %d: %s", action, code, stderr.String())
		}
		var report struct {
			Action string                           `json:"action"`
			Status store.RecordJournalCaptureStatus `json:"status"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || report.Action != action || report.Status.Capture != store.RecordJournalCaptureTriggers || !report.Status.Enabled {
			t.Fatalf("%s report %s: %v", action, stdout.String(), err)
		}
	}
	// Dropping triggers is a MySQL application-capture step.
	if code := recordJournalCaptureCommand([]string{"drop-triggers"}, &stdout, &stderr); code != 2 {
		t.Fatalf("drop-triggers on SQLite exited %d", code)
	}
}
