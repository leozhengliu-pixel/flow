package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/objectstore"
	"flow/api/internal/store"
)

// TestStorageGrowthAtScale measures database growth and the storage
// maintenance (event retention, trash purge, offloaded content, search cap) on
// a workspace shaped like a large tenant: 75k issues with a realistic spread
// of description lengths, documents with up to 100 versions, agent
// conversations with tool output, deleted documents in the trash and a year of
// domain events. Opt-in, one step per run:
//
//	FLOW_SCALE_TEST=1 FLOW_STORAGE_GROWTH_STEP=seed FLOW_SCALE_DATABASE_DRIVER=mysql FLOW_SCALE_DATABASE_URL='root:pw@tcp(127.0.0.1:3306)/flow' go test ./cmd/server -run TestStorageGrowthAtScale -v -timeout 60m
//
// Steps: seed (WIPES the database), open, events (90-day retention), trash,
// offload (migrates rows to offloaded content), check (verifies every body
// loads, times a reload). FLOW_STORAGE_GROWTH_OBJECTS names a directory that
// stands in for S3; FLOW_SEARCH_INDEX_MAX_CHARS sets the search cap. Measure
// between steps with scripts/db-size-report/.
func TestStorageGrowthAtScale(t *testing.T) {
	step := os.Getenv("FLOW_STORAGE_GROWTH_STEP")
	if os.Getenv("FLOW_SCALE_TEST") != "1" || step == "" {
		t.Skip("set FLOW_SCALE_TEST=1 and FLOW_STORAGE_GROWTH_STEP to run the storage growth measurement")
	}
	driver, url := os.Getenv("FLOW_SCALE_DATABASE_DRIVER"), os.Getenv("FLOW_SCALE_DATABASE_URL")
	config := store.DatabaseConfig{Driver: driver, URL: url, Path: os.Getenv("FLOW_SCALE_SQLITE_PATH"), FixtureProfile: "test", FixturePassword: "test-password", SearchIndexMaxChars: scaleEnvInt("FLOW_SEARCH_INDEX_MAX_CHARS", 0)}
	if dir := os.Getenv("FLOW_STORAGE_GROWTH_OBJECTS"); dir != "" {
		objects, err := objectstore.Open(context.Background(), objectstore.Config{Driver: "local", LocalPath: dir})
		if err != nil {
			t.Fatal(err)
		}
		config.ContentStore = flatObjects{objects}
	}
	if step == "seed" {
		if driver == "sqlite" {
			url = config.Path
		}
		db := openScaleDatabase(t, driver, url)
		defer db.Close()
		if driver != "sqlite" {
			resetScaleDatabase(t, db)
		}
		repository, err := store.OpenDatabase(config)
		if err != nil {
			t.Fatal(err)
		}
		repository.Close()
		seedStorageGrowthFixture(t, db, config)
		return
	}
	started := time.Now()
	repository, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	database, objects := repository.OffloadedContentReads()
	t.Logf("opened in %s; offloaded bodies read: %d from the database, %d from object storage", time.Since(started).Round(time.Millisecond), database, objects)
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	ctx := context.Background()
	started = time.Now()
	switch step {
	case "events":
		deleted, err := s.pruneDomainEvents(ctx, time.Now().Add(-90*24*time.Hour))
		t.Logf("pruned %d events in %s (%v)", deleted, time.Since(started).Round(time.Millisecond), err)
	case "trash":
		purged, err := s.purgeExpiredTrash(ctx, "test-workspace", time.Now().UTC())
		t.Logf("purged %d trash entries in %s (%v)", purged, time.Since(started).Round(time.Millisecond), err)
	case "offload":
		total := 0
		for {
			count, done, err := repository.MigrateOffloadedContent(ctx, 16)
			total += count
			if err != nil {
				t.Fatal(err)
			}
			if done {
				break
			}
		}
		t.Logf("offloaded %d records in %s", total, time.Since(started).Round(time.Millisecond))
	case "check":
		if err := repository.ReloadWorkspace(ctx, "test-workspace"); err != nil {
			t.Fatal(err)
		}
		database, objects := repository.OffloadedContentReads()
		t.Logf("reloaded in %s; offloaded bodies read: %d from the database, %d from object storage", time.Since(started).Round(time.Millisecond), database, objects)
		data, _ := repository.WorkspaceMetadataFields("test-workspace", "documents", "trash", "agentSessions")
		revisions, empty := 0, 0
		started = time.Now()
		for _, document := range data.Documents {
			loaded := slices.Clone(document.Revisions)
			if err := repository.LoadDocumentRevisionBodies(ctx, "test-workspace", loaded); err != nil {
				t.Fatal(err)
			}
			for _, revision := range loaded {
				revisions++
				if revision.Content == "" || len(revision.Offloaded) > 0 {
					empty++
				}
			}
		}
		results, missing := 0, 0
		for _, session := range data.AgentSessions {
			loaded, err := repository.LoadAgentSessionBodies(ctx, "test-workspace", session)
			if err != nil {
				t.Fatal(err)
			}
			for _, message := range loaded.Messages {
				for _, part := range message.Parts {
					if part.ToolCall != nil {
						results++
						if len(part.ToolCall.Result) == 0 {
							missing++
						}
					}
				}
			}
		}
		database, objects = repository.OffloadedContentReads()
		t.Logf("loaded every body in %s: %d versions (%d empty), %d tool results (%d missing); reads: %d database, %d object storage", time.Since(started).Round(time.Millisecond), revisions, empty, results, missing, database, objects)
		if empty > 0 || missing > 0 {
			t.Fatal("offloaded content is missing")
		}
		deleted, err := repository.SweepOffloadedContent(ctx, time.Now(), 24*time.Hour)
		t.Logf("sweep deleted %d unreferenced bodies (%v)", deleted, err)
	}
}

// flatObjects lets the local object store (flat keys) stand in for S3.
type flatObjects struct{ objectstore.Store }

func (f flatObjects) Put(ctx context.Context, key string, reader io.Reader, contentType string) (int64, error) {
	return f.Store.Put(ctx, strings.ReplaceAll(key, "/", "__"), reader, contentType)
}

func (f flatObjects) Open(ctx context.Context, key string) (io.ReadCloser, string, int64, error) {
	return f.Store.Open(ctx, strings.ReplaceAll(key, "/", "__"))
}

func (f flatObjects) Delete(ctx context.Context, key string) error {
	return f.Store.Delete(ctx, strings.ReplaceAll(key, "/", "__"))
}

func seedStorageGrowthFixture(t *testing.T, db scaleDB, config store.DatabaseConfig) {
	t.Helper()
	issues := scaleEnvInt("FLOW_SCALE_ISSUES", 75000)
	seedTeamScaleFixture(t, db, issues, 40000)
	text := newGrowthText(42)
	const workspace = "test-workspace"
	now := time.Now().UTC()

	// Issue descriptions with a realistic length spread.
	textBytes := 0
	for start := 1; start <= issues; start += 500 {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		rows, err := tx.Query(db.rebind(`SELECT id,data FROM issue_records WHERE workspace_key=? AND id IN (`+strings.TrimSuffix(strings.Repeat("?,", min(500, issues-start+1)), ",")+`)`), append([]any{workspace}, func() []any {
			ids := []any{}
			for n := start; n < start+500 && n <= issues; n++ {
				ids = append(ids, fmt.Sprintf("scale_%d", n))
			}
			return ids
		}()...)...)
		if err != nil {
			t.Fatal(err)
		}
		type pending struct {
			id  string
			raw []byte
		}
		batch := []pending{}
		for rows.Next() {
			var id string
			var raw []byte
			if err := rows.Scan(&id, &raw); err != nil {
				t.Fatal(err)
			}
			var issue map[string]any
			_ = json.Unmarshal(raw, &issue)
			description := text.markdown(text.issueLength())
			title := text.paragraph(40 + text.rng.IntN(40))
			textBytes += len(description) + len(title)
			issue["description"], issue["title"] = description, title
			encoded, _ := json.Marshal(issue)
			batch = append(batch, pending{id, encoded})
		}
		rows.Close()
		for _, item := range batch {
			if _, err := tx.Exec(db.rebind(`UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`), item.raw, workspace, item.id); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("issue text: %d MB", textBytes>>20)
	for _, table := range []string{"issue_search_migrations", "issue_list_migrations"} {
		_, _ = db.Exec(db.rebind(`DELETE FROM `+table+` WHERE workspace_key=?`), workspace)
	}
	_, _ = db.Exec(db.rebind(`DELETE FROM issue_search_documents WHERE workspace_key=?`), workspace)

	var rootRaw []byte
	if err := db.QueryRow(db.rebind(`SELECT data FROM workspace_states WHERE workspace_key=?`), workspace).Scan(&rootRaw); err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	_ = json.Unmarshal(rootRaw, &root)
	shapes := map[string]string{}
	_ = json.Unmarshal(root["_flowCollections"], &shapes)
	metadataSQL := &growthInserter{t: t, db: db, prefix: `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data)`, columns: 5}
	order := map[string]int{}
	put := func(field, key string, value any) {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if shapes[field] == "" {
			shapes[field] = "array"
		}
		order[field]++
		metadataSQL.add(workspace, field, key, 1000000+order[field], raw)
	}
	users := []domain.User{}
	for i := 0; i < 40; i++ {
		users = append(users, domain.User{ID: fmt.Sprintf("usr_scale_%d", i), Name: fmt.Sprintf("Scale user %d", i), DisplayName: fmt.Sprintf("scale%d", i), Email: fmt.Sprintf("scale%d@example.test", i), Active: true})
	}
	documentCount := scaleEnvInt("FLOW_STORAGE_GROWTH_DOCUMENTS", 1500)
	makeDocument := func(i int, created time.Time) domain.Document {
		id := fmt.Sprintf("document_growth_%d", i)
		length := text.documentLength()
		content := text.markdown(length)
		author := users[i%len(users)]
		document := domain.Document{ID: id, SlugID: fmt.Sprintf("doc-%d-%x", i, i*977), Title: text.paragraph(30), Content: content, ContentData: contentDataFor(content), Creator: author, TeamIDs: []string{"team_test"}, ProjectIDs: []string{}, SubscriberIDs: []string{author.ID}, CreatedAt: created, UpdatedAt: created, Version: 1}
		if i%2 == 0 {
			state := make([]byte, len(content)*6/10)
			for j := range state {
				state[j] = byte(text.rng.IntN(256))
			}
			document.ContentState = base64.StdEncoding.EncodeToString(state)
		}
		count := text.revisionCount()
		for r := 0; r < count; r++ {
			at := created.Add(time.Duration(count-r) * 3 * time.Hour)
			cut := len(content) * (count - r/2) / count
			revisionContent := content[:max(1, min(len(content), cut))]
			editor := users[(i+r)%len(users)]
			started := at.Add(-10 * time.Minute)
			revision := domain.DocumentRevision{ID: fmt.Sprintf("revision_%d_%d", i, r), DocumentID: id, Title: document.Title, Content: revisionContent, ContentData: contentDataFor(revisionContent), Author: editor, AuthorIDs: []string{editor.ID}, CreatedAt: at, StartedAt: &started}
			if document.ContentState != "" {
				revision.ContentState = document.ContentState[:min(len(document.ContentState), len(revisionContent)*8/10)]
			}
			document.Revisions = append(document.Revisions, revision)
		}
		return document
	}
	for i := 0; i < documentCount; i++ {
		document := makeDocument(i, now.Add(-time.Duration(text.rng.IntN(400*24))*time.Hour))
		put("documents", document.ID, document)
	}
	metadataSQL.flush()

	// Agent sessions with tool output.
	sessionCount := scaleEnvInt("FLOW_STORAGE_GROWTH_SESSIONS", 1500)
	for i := 0; i < sessionCount; i++ {
		owner := users[i%len(users)]
		created := now.Add(-time.Duration(text.rng.IntN(200*24)) * time.Hour)
		session := domain.AgentSession{ID: fmt.Sprintf("agent_session_growth_%d", i), SlugID: fmt.Sprintf("chat-%d", i), UserID: owner.ID, Title: text.paragraph(30), Location: "agent", IssueIDs: []string{}, SkillIDs: []string{}, CreatedAt: created, UpdatedAt: created}
		turns := 2 + text.rng.IntN(10)
		for turn := 0; turn < turns; turn++ {
			at := created.Add(time.Duration(turn) * time.Minute)
			session.Messages = append(session.Messages, domain.AgentMessage{ID: fmt.Sprintf("msg_%d_%d_u", i, turn), Role: "user", Content: text.paragraph(40 + text.rng.IntN(300)), CreatedAt: at})
			parts := []domain.AgentMessagePart{}
			for call := 0; call < text.rng.IntN(4); call++ {
				size := 200 + text.rng.IntN(3000)
				if text.rng.IntN(5) == 0 {
					size = 8000 + text.rng.IntN(80000)
				}
				result, _ := json.Marshal(map[string]any{"items": []any{map[string]any{"title": text.paragraph(40), "description": text.paragraph(size)}}})
				parts = append(parts, domain.AgentMessagePart{ID: fmt.Sprintf("part_%d_%d_%d", i, turn, call), Type: "tool", Status: "done", ToolCall: &domain.AgentToolCall{ID: fmt.Sprintf("call_%d_%d_%d", i, turn, call), Name: "search_issues", Arguments: json.RawMessage(`{"query":"sync"}`), Result: result, Status: "done"}})
			}
			reply := text.markdown(200 + text.rng.IntN(2500))
			parts = append(parts, domain.AgentMessagePart{ID: fmt.Sprintf("part_%d_%d_text", i, turn), Type: "text", Text: reply})
			session.Messages = append(session.Messages, domain.AgentMessage{ID: fmt.Sprintf("msg_%d_%d_a", i, turn), Role: "assistant", Content: reply, Parts: parts, CreatedAt: at.Add(20 * time.Second)})
		}
		put("agentSessions", session.ID, session)
	}
	metadataSQL.flush()

	// Trash: deleted documents (full snapshots) and issues over the last 60 days.
	trashCount := scaleEnvInt("FLOW_STORAGE_GROWTH_TRASH", 800)
	admin := domain.User{ID: "usr_admin", Name: "Test admin", Email: "admin@example.test", Active: true}
	for i := 0; i < trashCount; i++ {
		deleted := now.Add(-time.Duration(text.rng.IntN(60*24)) * time.Hour)
		entry := domain.TrashEntry{ID: fmt.Sprintf("trash_growth_%d", i), DeletedBy: admin, DeletedAt: deleted, ExpiresAt: deleted.AddDate(0, 0, 30)}
		if i%2 == 0 {
			document := makeDocument(documentCount+i, deleted.Add(-30*24*time.Hour))
			entry.ResourceType, entry.ResourceID, entry.Title, entry.TeamIDs = "document", document.ID, document.Title, document.TeamIDs
			entry.Payload, _ = json.Marshal(document)
		} else {
			issue := map[string]any{"id": fmt.Sprintf("trashed_issue_%d", i), "title": text.paragraph(50), "description": text.markdown(text.issueLength())}
			entry.ResourceType, entry.ResourceID, entry.Title = "issue", issue["id"].(string), issue["title"].(string)
			entry.Payload, _ = json.Marshal(issue)
		}
		put("trash", entry.ID, entry)
	}
	metadataSQL.flush()
	root["_flowCollections"], _ = json.Marshal(shapes)
	encoded, _ := json.Marshal(root)
	if _, err := db.Exec(db.rebind(`UPDATE workspace_states SET data=? WHERE workspace_key=?`), encoded, workspace); err != nil {
		t.Fatal(err)
	}

	// Domain events over ~400 days.
	eventCount := scaleEnvInt("FLOW_STORAGE_GROWTH_EVENTS", 600000)
	eventsSQL := &growthInserter{t: t, db: db, prefix: `INSERT INTO domain_events(id,event_type,aggregate_id,payload,previous_values,created_at)`, columns: 6}
	start := now.Add(-400 * 24 * time.Hour)
	for i := 0; i < eventCount; i++ {
		at := start.Add(time.Duration(float64(400*24*time.Hour) * float64(i) / float64(eventCount)))
		issueID := fmt.Sprintf("scale_%d", 1+text.rng.IntN(issues))
		var kind string
		var payload, previous []byte
		switch p := text.rng.IntN(100); {
		case p < 70:
			kind = "issue.updated"
			payload, _ = json.Marshal(map[string]any{"title": text.paragraph(50), "updatedAt": at, "version": i})
			if p%2 == 0 {
				previous, _ = json.Marshal(map[string]any{"title": text.paragraph(50), "updatedAt": at.Add(-time.Hour), "version": i - 1})
			}
		case p < 80:
			kind = "issue.created"
			payload, _ = json.Marshal(map[string]any{"id": issueID, "title": text.paragraph(50), "description": text.markdown(text.issueLength()), "createdAt": at})
		case p < 90:
			kind = "comment.created"
			payload, _ = json.Marshal(map[string]any{"issueId": issueID, "body": text.paragraph(300)})
		case p < 95:
			kind, issueID = "document.updated", fmt.Sprintf("document_growth_%d", text.rng.IntN(documentCount))
			payload, _ = json.Marshal(map[string]any{"content": text.paragraph(600)})
		default:
			kind = "notification.created"
			payload, _ = json.Marshal(map[string]any{"issueId": issueID, "type": "issueAssignedToYou"})
		}
		eventsSQL.add(fmt.Sprintf("evt_%d", at.UnixNano()+int64(i)), kind, issueID, payload, previous, at.Format(time.RFC3339Nano))
	}
	eventsSQL.flush()
	// Build derived indexes the way a deployment would on open.
	repository, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	repository.Close()
	t.Logf("seeded %d documents, %d sessions, %d trash, %d events", documentCount, sessionCount, trashCount, eventCount)
}

type growthInserter struct {
	t       *testing.T
	db      scaleDB
	prefix  string
	columns int
	rows    [][]any
	bytes   int
}

func (in *growthInserter) add(values ...any) {
	in.rows = append(in.rows, values)
	for _, value := range values {
		switch v := value.(type) {
		case []byte:
			in.bytes += len(v)
		case string:
			in.bytes += len(v)
		}
	}
	if len(in.rows) >= 400 || in.bytes >= 8<<20 {
		in.flush()
	}
}

func (in *growthInserter) flush() {
	if len(in.rows) == 0 {
		return
	}
	placeholder := "(" + strings.TrimSuffix(strings.Repeat("?,", in.columns), ",") + ")"
	groups := make([]string, len(in.rows))
	args := make([]any, 0, len(in.rows)*in.columns)
	for i, row := range in.rows {
		groups[i] = placeholder
		args = append(args, row...)
	}
	if _, err := in.db.Exec(in.prefix+" VALUES "+strings.Join(groups, ","), args...); err != nil {
		in.t.Fatal(err)
	}
	in.rows, in.bytes = in.rows[:0], 0
}

type growthText struct {
	rng   *rand.Rand
	words []string
}

func newGrowthText(seed uint64) *growthText {
	rng := rand.New(rand.NewPCG(seed, seed*7+1))
	syllables := []string{"ka", "lo", "mi", "ra", "ten", "sor", "vex", "plan", "dex", "ul", "ion", "ser", "ver", "cli", "ent", "api", "sync", "da", "ta", "bu", "fix", "re", "lease", "mod", "ule", "ope", "ra", "tion", "pro", "ject", "cus", "tom", "er", "on", "at", "in", "is", "as"}
	words := make([]string, 4000)
	for i := range words {
		n := 1 + rng.IntN(3)
		var b strings.Builder
		for j := 0; j < n; j++ {
			b.WriteString(syllables[rng.IntN(len(syllables))])
		}
		words[i] = b.String()
	}
	cjk := []string{"数据", "同步", "接口", "发布", "客户", "问题", "修复", "性能", "测试", "文档", "项目", "迭代"}
	words = append(words, cjk...)
	return &growthText{rng: rng, words: words}
}

func (g *growthText) paragraph(chars int) string {
	var b strings.Builder
	for b.Len() < chars {
		// Zipf-ish word choice.
		index := int(float64(len(g.words)) * g.rng.Float64() * g.rng.Float64())
		b.WriteString(g.words[index])
		if g.rng.IntN(14) == 0 {
			b.WriteString(". ")
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

func (g *growthText) markdown(chars int) string {
	var b strings.Builder
	for b.Len() < chars {
		switch g.rng.IntN(8) {
		case 0:
			b.WriteString("## " + g.paragraph(30) + "\n\n")
		case 1:
			for i := 0; i < 3; i++ {
				b.WriteString("- " + g.paragraph(60) + "\n")
			}
			b.WriteString("\n")
		default:
			b.WriteString(g.paragraph(200+g.rng.IntN(400)) + "\n\n")
		}
	}
	return b.String()
}

// contentData is a ProseMirror-like document for markdown.
func contentDataFor(markdown string) map[string]any {
	blocks := []any{}
	for _, block := range strings.Split(markdown, "\n\n") {
		if strings.TrimSpace(block) == "" {
			continue
		}
		blocks = append(blocks, map[string]any{"type": "paragraph", "attrs": map[string]any{"id": fmt.Sprintf("b%x", len(block)*31+len(blocks))}, "content": []any{map[string]any{"type": "text", "text": block}}})
	}
	return map[string]any{"type": "doc", "content": blocks}
}

func (g *growthText) issueLength() int {
	p := g.rng.Float64()
	switch {
	case p < 0.75:
		return 150 + g.rng.IntN(750)
	case p < 0.95:
		return 900 + g.rng.IntN(2100)
	case p < 0.996:
		return 3000 + g.rng.IntN(9000)
	default:
		return 20000 + g.rng.IntN(100000)
	}
}

func (g *growthText) documentLength() int {
	p := g.rng.Float64()
	switch {
	case p < 0.6:
		return 500 + g.rng.IntN(3000)
	case p < 0.95:
		return 3500 + g.rng.IntN(12000)
	default:
		return 20000 + g.rng.IntN(80000)
	}
}

func (g *growthText) revisionCount() int {
	p := g.rng.Float64()
	switch {
	case p < 0.4:
		return 1 + g.rng.IntN(5)
	case p < 0.75:
		return 6 + g.rng.IntN(25)
	case p < 0.95:
		return 31 + g.rng.IntN(69)
	default:
		return 100
	}
}
