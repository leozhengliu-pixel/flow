package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/coordination"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

const contractReplicaWorkspace = "contract-scale"

func contractReplicaConfig() store.DatabaseConfig {
	return store.DatabaseConfig{Driver: os.Getenv("FLOW_TEST_DATABASE_DRIVER"), URL: os.Getenv("FLOW_TEST_DATABASE_URL"), Path: os.Getenv("FLOW_TEST_DATABASE_URL"), MaxOpenConns: 4, MaxIdleConns: 2}
}

// The helper runs the actual API handler and scripted model in a separate OS
// process. Its parent stops it by PID, including when an assertion fails.
func TestLoopContractReplicaProcess(t *testing.T) {
	if os.Getenv("FLOW_LOOP_CONTRACT_CHILD") != "1" {
		t.Skip("contract gate subprocess")
	}
	repo, err := store.OpenDatabase(contractReplicaConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	coordinator, err := coordination.Open(ctx, coordination.Config{Mode: "standalone", URL: os.Getenv("FLOW_TEST_REDIS_URL"), Prefix: "contract-replicas", LockWait: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer coordinator.Close()
	repo.SetWorkspaceCoordinator(coordinator)
	provider := &fakeLoopProvider{}
	for range 32 {
		provider.contracts = append(provider.contracts, `tool:record_output_contract {"readOnly":true,"outputs":[],"always":false,"skipConditions":[]}`)
	}
	upstream := httptest.NewServer(http.HandlerFunc(provider.serve))
	defer upstream.Close()
	srv := &server{store: repo, uploadPath: os.Getenv("FLOW_LOOP_CONTRACT_DIR"), authDisabled: true, coordinator: coordinator, coordinationContext: ctx, loopContractLive: true,
		agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: upstream.URL, Model: "scripted-contract", MaxOutputTokens: 256}, agentClient: upstream.Client()}
	api := httptest.NewServer(newHandler(srv))
	defer api.Close()
	if err := os.WriteFile(filepath.Join(os.Getenv("FLOW_LOOP_CONTRACT_DIR"), "url"), []byte(api.URL), 0600); err != nil {
		t.Fatal(err)
	}
	<-ctx.Done()
}

func TestLoopContractTwoReplicaGate(t *testing.T) {
	if os.Getenv("FLOW_LOOP_CONTRACT_REPLICAS") != "1" {
		t.Skip("opt-in two-process contract write gate")
	}
	config := contractReplicaConfig()
	if config.Driver == "" || config.URL == "" || os.Getenv("FLOW_TEST_REDIS_URL") == "" {
		t.Fatal("the gate requires a throwaway database and Redis")
	}
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	if !repo.HasWorkspace(contractReplicaWorkspace) {
		if _, err := repo.CreateWorkspace(t.Context(), "Contract scale", contractReplicaWorkspace, "us"); err != nil {
			t.Fatal(err)
		}
		if err := repo.MutateWorkspace(t.Context(), contractReplicaWorkspace, "loop.created", "contract_loop", nil, func(data *domain.Bootstrap) error {
			data.Loops = append(data.Loops, domain.Loop{ID: "contract_loop", Name: "Summary", Instructions: "Reply with a summary.", Status: "published", Level: "workspace", Creator: data.Viewer, OwnerID: data.Viewer.ID})
			data.LoopVersions = append(data.LoopVersions, domain.LoopVersion{ID: "contract_version_0", LoopID: "contract_loop", Definition: domain.LoopDefinition{Name: "Summary", Instructions: "Reply with a summary."}})
			for i := range 2 {
				data.Drafts = append(data.Drafts, domain.Draft{ID: fmt.Sprintf("contract_draft_%d", i), UserID: data.Viewer.ID, Type: "issue", Title: "Seed draft"})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	repo.Close()
	driver := config.Driver
	if driver == "postgres" {
		driver = "pgx"
	}
	db, err := sql.Open(driver, config.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	contentDigest := func() string {
		t.Helper()
		hash := sha256.New()
		for _, query := range []string{
			`SELECT id,data FROM issue_records WHERE workspace_key='contract-scale' ORDER BY id`,
			`SELECT id,data FROM workspace_content_records WHERE workspace_key='contract-scale' ORDER BY kind,resource_id,id`,
		} {
			rows, err := db.Query(query)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var id string
				var raw []byte
				if err := rows.Scan(&id, &raw); err != nil {
					t.Fatal(err)
				}
				fmt.Fprintf(hash, "%d:%s%d:", len(id), id, len(raw))
				hash.Write(raw)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
		}
		return fmt.Sprintf("%x", hash.Sum(nil))
	}
	before := contentDigest()
	var processes []*exec.Cmd
	var urls []string
	stop := func() {
		for _, cmd := range processes {
			_ = cmd.Process.Signal(os.Interrupt)
		}
		for _, cmd := range processes {
			if err := cmd.Wait(); err != nil {
				t.Errorf("replica process: %v (see %s)", err, cmd.Dir)
			}
		}
		processes = nil
	}
	t.Cleanup(stop)
	for i := range 2 {
		dir := t.TempDir()
		output, err := os.Create(filepath.Join(dir, "replica.log"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { output.Close() })
		cmd := exec.Command(os.Args[0], "-test.run=^TestLoopContractReplicaProcess$", "-test.timeout=3m")
		cmd.Env = append(os.Environ(), "FLOW_LOOP_CONTRACT_CHILD=1", "FLOW_LOOP_CONTRACT_DIR="+dir)
		cmd.Stdout, cmd.Stderr, cmd.Dir = output, output, dir
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		processes = append(processes, cmd)
		deadline := time.Now().Add(90 * time.Second)
		for {
			url, err := os.ReadFile(filepath.Join(dir, "url"))
			if err == nil {
				urls = append(urls, string(url))
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("replica %d did not start; see %s", i, dir)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	client := &http.Client{Timeout: 15 * time.Second}
	request := func(base, method, path string, input any) error {
		raw, _ := json.Marshal(input)
		req, _ := http.NewRequest(method, base+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Workspace-Key", contractReplicaWorkspace)
		response, err := client.Do(req)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil {
			return err
		}
		if response.StatusCode != http.StatusOK || strings.Contains(string(body), "workspace is busy") {
			return fmt.Errorf("%s %s status=%d: %s", method, path, response.StatusCode, body)
		}
		return nil
	}
	started := time.Now()
	const rounds = 12
	for round := range rounds {
		var group sync.WaitGroup
		failures := make(chan error, 4)
		for i := range 2 {
			group.Add(2)
			go func() {
				defer group.Done()
				failures <- request(urls[i], http.MethodPost, "/api/loops/contract_loop/output-contract", map[string]bool{"force": true})
			}()
			go func() {
				defer group.Done()
				failures <- request(urls[i], http.MethodPatch, fmt.Sprintf("/api/drafts/contract_draft_%d", i), map[string]string{"title": fmt.Sprintf("Replica %d round %d", i, round), "body": fmt.Sprintf("Saved body %d", round)})
			}()
		}
		group.Wait()
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	stop()
	if after := contentDigest(); after != before {
		t.Fatal("issue/comment/activity bytes changed during contract and draft saves")
	}
	repo, err = store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data, ok := repo.WorkspaceMetadataFields(contractReplicaWorkspace, "loops", "loopVersions", "drafts")
	if !ok || len(data.Loops) != 1 || data.Loops[0].OutputContract == nil || data.Loops[0].OutputContract.InstructionsHash != loopContractHashOf(data.Loops[0]) {
		t.Fatal("final contract missing or stale after reopening")
	}
	for _, version := range data.LoopVersions {
		if version.OutputContract == nil || version.OutputContract.InstructionsHash != data.Loops[0].OutputContract.InstructionsHash {
			t.Fatalf("version %s contract=%+v differs from current=%+v after reopening", version.ID, version.OutputContract, data.Loops[0].OutputContract)
		}
	}
	if len(data.Drafts) != 2 {
		t.Fatalf("lost drafts: %d", len(data.Drafts))
	}
	for i := range 2 {
		found := false
		for _, draft := range data.Drafts {
			if draft.ID == fmt.Sprintf("contract_draft_%d", i) && draft.Title == fmt.Sprintf("Replica %d round %d", i, rounds-1) && draft.Body == fmt.Sprintf("Saved body %d", rounds-1) {
				found = true
			}
		}
		if !found {
			t.Fatalf("replica %d lost its final draft update", i)
		}
	}
	t.Logf("CONTRACT_REPLICAS driver=%s processes=2 requests=%d busy=0 lost_updates=0 content_sha256=%s elapsed=%s", config.Driver, rounds*4, before, time.Since(started).Round(time.Millisecond))
}
