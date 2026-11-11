//go:build loadtest

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/store"
)

// An instrumented browser appends a line after state installation, not in its
// EventSource callback: {eventId,clientId,appliedAt:RFC3339Nano}. Clocks must
// share the API host's wall clock. Partial trailing lines wait for the next poll.
type scaleP01BrowserLog struct {
	path   string
	offset int64
}

func TestScaleP01BrowserAcknowledgements(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acks.jsonl")
	now := time.Now().UTC()
	ledger := newScaleP01Ledger()
	ledger.commit(store.ScaleP01Commit{ID: "browser-event", At: now}, "document", []bool{true, true})
	raw, err := json.Marshal(scaleP01Ack{"browser-event", 0, now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	log := scaleP01BrowserLog{path: path}
	if err := log.poll(ledger); err != nil {
		t.Fatal(err)
	}
	if ledger.report(now.Add(2*time.Second), 10*time.Second, "browser")["deliveries"] != 0 {
		t.Fatal("partial acknowledgement accepted")
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := log.poll(ledger); err != nil {
		t.Fatal(err)
	}
	if err := log.poll(ledger); err != nil {
		t.Fatal(err)
	}
	report := ledger.report(now.Add(2*time.Second), 10*time.Second, "browser")
	if report["deliveries"] != 1 || report["expected"] != 2 || report["duplicateAcks"] != 0 || report["sloLatencySeconds"].(map[string]any)["p95"] != nil {
		t.Fatal("browser acknowledgements lost their denominator or were replayed")
	}
}

func (log *scaleP01BrowserLog) poll(ledger *scaleP01Ledger) error {
	if log.path == "" {
		return nil
	}
	file, err := os.Open(log.path)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(io.NewSectionReader(file, log.offset, 1<<62), 4096)
	for {
		line, err := reader.ReadSlice('\n')
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return errors.New("browser acknowledgement line exceeds 4096 bytes or cannot be read")
		}
		var ack scaleP01Ack
		if err := json.Unmarshal(line, &ack); err != nil {
			return errors.New("invalid browser acknowledgement JSON")
		}
		ledger.ack(ack, "browser")
		log.offset += int64(len(line))
	}
}
