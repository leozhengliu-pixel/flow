package main

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

func scaleP21SW180Socket(t *testing.T) (*realtimeHub, *realtimeSocketClient) {
	t.Helper()
	hub := newRealtimeHub()
	hub.budget = &realtimeBudget{limit: 16 << 20}
	client := hub.addSocket("sw180-workspace", "sw180-client", func() {})
	if client == nil {
		t.Fatal("socket admission failed")
	}
	t.Cleanup(func() { hub.removeSocket(client) })
	return hub, client
}

func scaleP21SW180Header(t *testing.T, hub *realtimeHub, client *realtimeSocketClient, collaborationID, documentID string, edit bool) (string, *documentSocketAccess) {
	t.Helper()
	before := hub.budget.used.Load()
	key, err := hub.joinDocumentHeader(t.Context(), client, collaborationID, documentID, edit)
	if err != nil {
		t.Fatal(err)
	}
	hub.mu.Lock()
	access := client.documentAccess[key]
	_, joined := client.documents[key]
	hub.mu.Unlock()
	if !joined || access == nil || access.documentID != documentID || access.canEdit != edit || access.checkedAt.IsZero() {
		t.Fatal("retained header changed its identity or access")
	}
	if access.ownedBytes < int64(len(collaborationID)+len(documentID)) {
		t.Fatal("header reservation does not cover both retained strings")
	}
	if before+access.ownedBytes < hub.budget.used.Load() {
		t.Fatal("header retained unexplained budget")
	}
	if unsafe.StringData(key) == unsafe.StringData(collaborationID) ||
		unsafe.StringData(access.documentID) == unsafe.StringData(documentID) {
		t.Fatal("header retained caller string storage")
	}
	return key, access
}

func TestScaleP21SW180CollaborationHeaderOwnershipAndReplacement(t *testing.T) {
	hub, client := scaleP21SW180Socket(t)
	base := hub.budget.used.Load()
	// Substrings must not keep the unrelated tail of either caller allocation.
	keyPrefix := strings.Repeat("collaboration-", 16384)
	recordPrefix := strings.Repeat("document-", 32768)
	keyBacking := keyPrefix + strings.Repeat("unused-key-tail", 65536)
	recordBacking := recordPrefix + strings.Repeat("unused-record-tail", 65536)
	collaborationID := keyBacking[:len(keyPrefix)]
	documentID := recordBacking[:len(recordPrefix)]
	_, first := scaleP21SW180Header(t, hub, client, collaborationID, documentID, false)
	if got := hub.budget.used.Load(); got != base+first.ownedBytes {
		t.Fatalf("long header charge=%d want=%d", got, base+first.ownedBytes)
	}
	replacementID := strings.Repeat("replacement-", 8192)
	nextKey, next := scaleP21SW180Header(t, hub, client, collaborationID, replacementID, true)
	if first.ownedBytes != 0 || first.documentID != "" {
		t.Fatal("replacement retained the prior header owner")
	}
	if got := hub.budget.used.Load(); got != base+next.ownedBytes {
		t.Fatalf("replacement retained prior charge: %d", got)
	}
	hub.mu.Lock()
	if len(client.documentAccess) != 1 || len(client.documents) != 1 {
		hub.mu.Unlock()
		t.Fatal("rejoin changed cache or join cardinality")
	}
	for joined := range client.documents {
		if unsafe.StringData(joined) != unsafe.StringData(nextKey) {
			hub.mu.Unlock()
			t.Fatal("rejoin retained the previous key allocation")
		}
	}
	hub.mu.Unlock()

	hub.budget.limit = hub.budget.used.Load()
	_, err := hub.joinDocumentHeader(t.Context(), client, collaborationID, documentID, false)
	if !errors.Is(err, errRealtimeBudget) || client.documentAccess[nextKey] != next ||
		!next.canEdit || next.documentID != replacementID || hub.budget.used.Load() != base+next.ownedBytes {
		t.Fatal("refused replacement changed the live header or its charge", err)
	}
	hub.removeSocket(client)
	if next.ownedBytes != 0 || next.documentID != "" || hub.budget.used.Load() != 0 {
		t.Fatal("socket teardown retained replacement ownership")
	}
}

func TestScaleP21SW180CollaborationHeaderRevocationAndTeardown(t *testing.T) {
	hub, client := scaleP21SW180Socket(t)
	base := hub.budget.used.Load()
	key, first := scaleP21SW180Header(t, hub, client, "joined-collaboration", "joined-record", true)
	otherKey, other := scaleP21SW180Header(t, hub, client, "other-collaboration", "other-record", false)
	hub.revokeDocument(client, key, "")
	if first.ownedBytes != 0 || first.documentID != "" || client.documentAccess[key] != nil || hub.joinedDocument(client, key) {
		t.Fatal("document revocation retained the joined header")
	}
	select {
	case notice := <-client.send:
		notice.release(client)
	default:
		t.Fatal("joined document revocation did not enqueue its notice")
	}
	if got := hub.budget.used.Load(); got != base+other.ownedBytes {
		t.Fatalf("revocation affected independent ownership: %d", got)
	}
	hub.revokeDocument(client, key, "")
	if got := hub.budget.used.Load(); got != base+other.ownedBytes {
		t.Fatal("repeated revocation released ownership twice")
	}
	// Even an orphaned cache entry must release its header without a join notice.
	hub.mu.Lock()
	delete(client.documents, otherKey)
	hub.mu.Unlock()
	hub.revokeDocument(client, otherKey, "")
	if other.ownedBytes != 0 || hub.budget.used.Load() != base {
		t.Fatal("unjoined header revocation leaked its reservation")
	}
	_, final := scaleP21SW180Header(t, hub, client, "final-collaboration", "final-record", true)
	if !client.enqueue(realtimeSocketMessage{data: []byte("queued before teardown")}, true) {
		t.Fatal("could not stage queued socket ownership")
	}
	hub.removeSocket(client)
	hub.removeSocket(client)
	if final.ownedBytes != 0 || final.documentID != "" || client.baseBytes != 0 ||
		client.queuedBytes.Load() != 0 || hub.budget.used.Load() != 0 ||
		len(client.documentAccess) != 0 || hub.hasSockets(client.workspace) {
		t.Fatal("idempotent teardown did not release header, queue and socket ownership")
	}
}

type scaleP21SW180ObservedContext struct {
	context.Context
	once    sync.Once
	checked chan struct{}
}

func (ctx *scaleP21SW180ObservedContext) Err() error {
	err := ctx.Context.Err()
	ctx.once.Do(func() { close(ctx.checked) })
	return err
}

func TestScaleP21SW180CollaborationHeaderRefusals(t *testing.T) {
	for _, state := range []string{"canceled", "closed", "over-budget", "canceled-hub-wait", "canceled-queue-wait"} {
		t.Run(state, func(t *testing.T) {
			hub, client := scaleP21SW180Socket(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := errRealtimeBudget
			switch state {
			case "canceled":
				cancel()
				want = context.Canceled
			case "closed":
				hub.removeSocket(client)
			case "over-budget":
				hub.budget.limit = hub.budget.used.Load()
			}
			before := hub.budget.used.Load()
			var err error
			if strings.HasSuffix(state, "-wait") {
				observed := &scaleP21SW180ObservedContext{Context: ctx, checked: make(chan struct{})}
				if state == "canceled-hub-wait" {
					hub.mu.Lock()
				} else {
					client.queueMu.Lock()
				}
				result := make(chan error, 1)
				go func() {
					_, retainErr := hub.joinDocumentHeader(observed, client, "refused-collaboration", "refused-record", true)
					result <- retainErr
				}()
				<-observed.checked
				cancel()
				if state == "canceled-hub-wait" {
					hub.mu.Unlock()
				} else {
					client.queueMu.Unlock()
				}
				select {
				case err = <-result:
				case <-time.After(5 * time.Second):
					t.Fatal("canceled header retention deadlocked")
				}
				want = context.Canceled
			} else {
				_, err = hub.joinDocumentHeader(ctx, client, "refused-collaboration", "refused-record", true)
			}
			if !errors.Is(err, want) {
				t.Fatalf("refusal=%v want=%v", err, want)
			}
			hub.mu.Lock()
			empty := len(client.documentAccess) == 0 && len(client.documents) == 0
			hub.mu.Unlock()
			if !empty || hub.budget.used.Load() != before {
				t.Fatal("refused header mutated membership or leaked budget")
			}
		})
	}
}

func TestScaleP21SW180CollaborationHeaderConcurrentLifetime(t *testing.T) {
	hub, client := scaleP21SW180Socket(t)
	scaleP21SW180Header(t, hub, client, "initial-collaboration", "initial-record", true)
	start := make(chan struct{})
	retainedHeaders := make(chan struct{})
	var retainedCount atomic.Int32
	failures := make(chan error, 4)
	var workers sync.WaitGroup
	for worker := range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			if worker == 3 {
				<-retainedHeaders
			}
			for iteration := range 100 {
				id := fmt.Sprintf("concurrent-%d", iteration%8)
				switch worker {
				case 0, 1:
					_, err := hub.joinDocumentHeader(t.Context(), client, id, strings.Repeat(id, 256), iteration%2 == 0)
					if err != nil && !errors.Is(err, errRealtimeBudget) {
						failures <- err
						return
					}
					if err == nil && retainedCount.Add(1) == 16 {
						close(retainedHeaders)
					}
				case 2:
					hub.revokeDocument(client, id, "")
				case 3:
					hub.removeSocket(client)
				}
			}
		}()
	}
	close(start)
	done := make(chan struct{})
	go func() {
		workers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent retain/revoke/remove deadlocked")
	}
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if retainedCount.Load() < 16 {
		t.Fatal("concurrency check did not exercise live header retention")
	}
	hub.removeSocket(client)
	hub.mu.Lock()
	retained := len(client.documentAccess) != 0 || len(client.documents) != 0
	hub.mu.Unlock()
	if retained || hub.budget.used.Load() != 0 || client.queuedBytes.Load() != 0 {
		t.Fatal("concurrent lifetime left socket headers or budget behind")
	}
}

func TestScaleP21SW180CollaborationHeaderConsumerAtomicJoin(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate collaboration consumer source")
	}
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, filepath.Join(filepath.Dir(source), "realtime_collaboration.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var consumer *ast.FuncDecl
	for _, declaration := range parsed.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok && function.Name.Name == "handleCollaborationCommand" {
			consumer = function
		}
	}
	if consumer == nil || consumer.Body == nil {
		t.Fatal("collaboration command consumer is missing")
	}
	isName := func(expression ast.Expr, name string) bool {
		identifier, ok := expression.(*ast.Ident)
		return ok && identifier.Name == name
	}
	var ancestors []ast.Node
	var headerJoin, fallbackJoin token.Pos
	headerCalls, fallbackCalls := 0, 0
	activated := false
	// Pair the runtime ownership invariant below with the actual consumer's
	// sequencing: revocation must not be undone by a second, uncharged join.
	ast.Inspect(consumer.Body, func(node ast.Node) bool {
		if node == nil {
			ancestors = ancestors[:len(ancestors)-1]
			return true
		}
		ancestors = append(ancestors, node)
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch selector.Sel.Name {
		case "joinDocumentHeader":
			headerCalls++
			headerJoin = call.Pos()
			for index := len(ancestors) - 1; index >= 0; index-- {
				block, ok := ancestors[index].(*ast.BlockStmt)
				if !ok {
					continue
				}
				for _, statement := range block.List {
					assignment, ok := statement.(*ast.AssignStmt)
					if !ok || assignment.Pos() <= call.End() || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
						continue
					}
					activated = activated || isName(assignment.Lhs[0], "headerJoined") && isName(assignment.Rhs[0], "true")
				}
				break
			}
		case "joinDocument":
			fallbackCalls++
			fallbackJoin = call.Pos()
			guarded := false
			for _, ancestor := range ancestors {
				branch, ok := ancestor.(*ast.IfStmt)
				if !ok || call.Pos() < branch.Body.Pos() || call.End() > branch.Body.End() {
					continue
				}
				condition, ok := branch.Cond.(*ast.UnaryExpr)
				guarded = guarded || ok && condition.Op == token.NOT && isName(condition.X, "headerJoined")
			}
			if !guarded {
				t.Errorf("separate document join at %s can rejoin a revoked bounded header", files.Position(call.Pos()))
			}
		}
		return true
	})
	if headerCalls != 1 || fallbackCalls != 1 || !activated || fallbackJoin <= headerJoin {
		t.Fatalf("consumer lost atomic join handoff: header=%d fallback=%d activated=%t", headerCalls, fallbackCalls, activated)
	}

	hub, client := scaleP21SW180Socket(t)
	base := hub.budget.used.Load()
	key, access := scaleP21SW180Header(t, hub, client, "atomic-collaboration", "atomic-record", true)
	if got := hub.budget.used.Load(); got != base+access.ownedBytes {
		t.Fatal("atomic join returned before charging its cache")
	}
	hub.revokeDocument(client, key, "")
	select {
	case notice := <-client.send:
		notice.release(client)
	default:
		t.Fatal("atomic join was not visible to revocation")
	}
	hub.mu.Lock()
	removed := len(client.documents) == 0 && len(client.documentAccess) == 0
	hub.mu.Unlock()
	if !removed || access.ownedBytes != 0 || hub.budget.used.Load() != base {
		t.Fatal("revocation left a joined key without matching ownership")
	}
}
