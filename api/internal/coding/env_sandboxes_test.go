package coding

import (
	"context"
	"testing"
	"time"

	"flow/api/internal/config"
	"flow/api/internal/sandbox"
)

type memoryRecords struct{ records map[string]sandbox.Record }

func (m *memoryRecords) CreateCodingSandbox(_ context.Context, record sandbox.Record) error {
	m.records[record.ID] = record
	return nil
}
func (m *memoryRecords) SetCodingSandboxDesired(_ context.Context, id string, desired sandbox.Desired) error {
	record, ok := m.records[id]
	if !ok {
		return ErrNotFound
	}
	record.Desired = desired
	m.records[id] = record
	return nil
}
func (m *memoryRecords) CodingSandbox(_ context.Context, id string) (sandbox.Record, error) {
	record, ok := m.records[id]
	if !ok {
		return sandbox.Record{}, ErrNotFound
	}
	return record, nil
}

func TestRecordLauncher(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	records := &memoryRecords{records: map[string]sandbox.Record{}}
	launcher := RecordLauncher{Store: records, Config: config.CodingConfig{Image: "img"}, Now: func() time.Time { return now }}
	request := LaunchRequest{ID: "sbx", WorkspaceKey: "w", Kind: sandbox.KindDebug, UserID: "u1", Deadline: 20 * time.Minute, Env: map[string]string{sandbox.EnvMode: ModeDebug}}
	for range 2 { // idempotent
		if err := launcher.Launch(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	record := records.records["sbx"]
	if record.Desired != sandbox.DesiredRunning || record.Kind != sandbox.KindDebug || record.UserID != "u1" || record.Size != "medium" || !record.DeadlineAt.Equal(now.Add(20*time.Minute)) || record.Image != "img" {
		t.Fatalf("record: %+v", record)
	}
	record.Phase = sandbox.PhaseRunning
	records.records["sbx"] = record
	if status, err := launcher.Observe(context.Background(), "w", "sbx"); err != nil || status.Phase != sandbox.PhaseRunning {
		t.Fatalf("observe: %+v %v", status, err)
	}
	if status, _ := launcher.Observe(context.Background(), "w", "missing"); status.Phase != sandbox.PhaseMissing {
		t.Fatal("missing record")
	}
	if err := launcher.Release(context.Background(), "w", "sbx"); err != nil || records.records["sbx"].Desired != sandbox.DesiredStopped {
		t.Fatalf("release: %v", err)
	}
	if err := launcher.Release(context.Background(), "w", "missing"); err != nil {
		t.Fatalf("release missing: %v", err)
	}
}

func TestNewResourceIDAndCost(t *testing.T) {
	id := NewResourceID(time.Now())
	if len(id) != 26 || id != toLowerASCII(id) {
		t.Fatal(id)
	}
	if NewResourceID(time.Now()) == id {
		t.Fatal("ids must be unique")
	}
	if cost := RuntimeCostMicros(map[string]float64{"medium": 0.36}, "medium", 3600); cost != 360000 {
		t.Fatal(cost)
	}
}

func toLowerASCII(value string) string {
	out := []byte(value)
	for index, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[index] = c + 32
		}
	}
	return string(out)
}
