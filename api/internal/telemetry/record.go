package telemetry

import (
	"context"
	"crypto/rand"
	randv2 "math/rand/v2"
	"sync"
	"sync/atomic"
	"time"
)

type requestKey struct{}
type workKey struct{}
type recorderKey struct{}

type request struct {
	recorder      *Recorder
	route, method string
	start         time.Time
	sampled       bool
	inFlight      *flight
	readBytes     atomic.Int64
	workBytes     atomic.Int64
	mu            sync.Mutex
	finished      bool
	stages        [stageCount]stageSample
}

type stageSample struct {
	total, longest time.Duration
	count          int64
	start          time.Time
	kind, detail   string
}

func fromContext(ctx context.Context) *request {
	if !Enabled() || ctx == nil {
		return nil
	}
	r, _ := ctx.Value(requestKey{}).(*request)
	return r
}

// FromContext retrieves the enabled recorder attached to a request or worker.
func FromContext(ctx context.Context) *Recorder {
	if !Enabled() || ctx == nil {
		return nil
	}
	if r := fromContext(ctx); r != nil {
		return r.recorder
	}
	rec, _ := ctx.Value(recorderKey{}).(*Recorder)
	return rec
}

func WithRecorder(ctx context.Context, r *Recorder) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, recorderKey{}, r)
}

func Route(ctx context.Context) string {
	if r := fromContext(ctx); r != nil {
		return r.route
	}
	return Unmatched
}

func WithWork(ctx context.Context, kind WorkKind) context.Context {
	if FromContext(ctx) == nil {
		return ctx
	}
	return context.WithValue(ctx, workKey{}, kind)
}

func Work(ctx context.Context) WorkKind {
	kind, _ := ctx.Value(workKey{}).(WorkKind)
	if kind >= WorkKindCount {
		return WorkOther
	}
	return kind
}

// BeginRequest binds only the route returned by Flow's static route inventory.
func (rec *Recorder) BeginRequest(ctx context.Context, route, verb string) context.Context {
	route = rec.safeRoute(route)
	verb = method(verb)
	inFlight := rec.inFlight[requestKeyLabels{route, verb}]
	if inFlight == nil {
		inFlight = rec.inFlight[requestKeyLabels{Unmatched, verb}]
	}
	inFlight.value.Add(1)
	inFlight.seen.Store(true)
	r := &request{
		recorder: rec, route: route, method: verb, start: time.Now(), inFlight: inFlight,
		sampled: rec.sampled(),
	}
	return context.WithValue(ctx, requestKey{}, r)
}

func (rec *Recorder) sampled() bool {
	rate := rec.options.SampleRate
	return rate >= 1 || rate > 0 && randv2.Float64() < rate
}

func (rec *Recorder) EndRequest(ctx context.Context, code int, busy bool, written int64) {
	r, _ := ctx.Value(requestKey{}).(*request)
	r.inFlight.value.Add(-1)
	read := r.readBytes.Load()
	rec.bodyBytes.Add(-read)
	end := time.Now()
	elapsed := end.Sub(r.start)
	labels := labels{route: r.route, method: r.method, status: statusClass(code)}
	rec.requests(labels, elapsed, read, written, code == 503, busy)
	if rec.legacyHTTP != nil {
		rec.legacyHTTP.record(ctx, labels, code, elapsed, read, written)
	}
	slow := elapsed >= rec.options.SlowRequest && !longLivedRoute(r.route)
	failed := code >= 500
	r.mu.Lock()
	r.finished = true
	if !slow && !failed && !r.sampled {
		r.mu.Unlock()
		return
	}
	stages := r.stages
	r.mu.Unlock()

	record := traceRecord{labels: labels, start: r.start, end: end, stages: stages, failed: failed}
	if slow || failed {
		record.tail = true
	}
	rec.submitTrace(&record)
	if slow {
		rec.queueEvent(event{kind: slowRequest, trace: record})
	}
}

func (rec *Recorder) submitTrace(record *traceRecord) {
	if rec.targets[traces].url == "" {
		return
	}
	if len(rec.traceQueue) == cap(rec.traceQueue) {
		rec.dropRecords(traces, 1)
		return
	}
	_, _ = rand.Read(record.id[:])
	_, _ = rand.Read(record.root[:])
	rec.queueTrace(*record)
}

type JobRun struct {
	request *request
	kind    string
}

func (rec *Recorder) BeginJob(ctx context.Context, kind string) (context.Context, JobRun) {
	if rec == nil {
		return ctx, JobRun{}
	}
	r := &request{recorder: rec, route: Unmatched, start: time.Now(), sampled: rec.sampled()}
	if parent := fromContext(ctx); parent != nil {
		r.route, r.method = parent.route, parent.method
	}
	ctx = context.WithValue(ctx, requestKey{}, r)
	return WithWork(ctx, WorkBackground), JobRun{request: r, kind: enum(kind, jobKinds)}
}

func (job JobRun) End() {
	r := job.request
	if r == nil {
		return
	}
	end := time.Now()
	elapsed := end.Sub(r.start)
	rec := r.recorder
	rec.observe(jobDuration, labels{kind: job.kind}, elapsed.Seconds())
	r.mu.Lock()
	r.finished = true
	slow := elapsed >= rec.options.SlowRequest
	if !r.sampled && !slow {
		r.mu.Unlock()
		return
	}
	record := traceRecord{labels: labels{route: r.route, method: r.method, kind: job.kind},
		start: r.start, end: end, stages: r.stages, job: true, tail: slow}
	r.mu.Unlock()
	rec.submitTrace(&record)
}

func (rec *Recorder) BodyBytes(ctx context.Context, delta int64) {
	rec.bodyBytes.Add(delta)
	r, _ := ctx.Value(requestKey{}).(*request)
	r.readBytes.Add(delta)
}

type workOwner struct {
	route string
	kind  WorkKind
}

type workStats struct{ used, peak, waiting int64 }

type Admission struct {
	rec     *Recorder
	request *request
	owner   workOwner
	timing  Timing
	waiting bool
}

func (fallback *Recorder) BeginAdmission(ctx context.Context) Admission {
	rec := FromContext(ctx)
	if rec == nil {
		rec = fallback
	}
	if rec == nil {
		return Admission{}
	}
	r := fromContext(ctx)
	kind := Work(ctx)
	route := Unmatched
	if r != nil {
		route = r.route
		if kind == WorkOther {
			kind = WorkRead
			if r.method != "GET" && r.method != "HEAD" {
				kind = WorkWrite
			}
		}
	}
	return Admission{rec: rec, request: r, owner: workOwner{route, kind}, timing: begin(ctx, WorkAdmission, kind.String(), "")}
}

func (a *Admission) Wait() {
	if a.rec != nil && !a.waiting {
		a.waiting = true
		a.rec.activeMu.Lock()
		a.rec.workKinds[a.owner.kind].waiting++
		a.rec.activeMu.Unlock()
	}
}

func (a Admission) End() {
	if a.waiting {
		a.rec.activeMu.Lock()
		a.rec.workKinds[a.owner.kind].waiting--
		a.rec.activeMu.Unlock()
	}
	a.timing.End()
}

func (a Admission) Active() bool { return a.rec != nil }

func (a Admission) Refused(contended bool) {
	if a.rec != nil {
		a.rec.WorkRefused(a.owner.kind, contended)
	}
}

// Charge retains the owner and request counters without retaining the context.
func (a Admission) Charge(delta int64) {
	rec := a.rec
	if rec == nil {
		return
	}
	rec.activeMu.Lock()
	defer rec.activeMu.Unlock()
	k := &rec.workKinds[a.owner.kind]
	k.used += delta
	k.peak = max(k.peak, k.used)
	rec.workHolders[a.owner] += delta
	if rec.workHolders[a.owner] == 0 {
		delete(rec.workHolders, a.owner)
	}
	if r := a.request; r != nil && r.inFlight != nil {
		after := r.workBytes.Add(delta)
		switch {
		case after == 0:
			delete(rec.active, r)
		case after == delta && len(rec.active) < 512:
			rec.active[r] = struct{}{}
		}
	}
}

func (rec *Recorder) topWork() []WorkHolder {
	rec.activeMu.Lock()
	defer rec.activeMu.Unlock()
	top := make([]WorkHolder, 0, 8)
	for owner, bytes := range rec.workHolders {
		h := WorkHolder{owner.kind, owner.route, bytes}
		top = keepTop(top, h, bytes, func(x WorkHolder) int64 { return x.Bytes })
	}
	return top
}

type requestHolder struct {
	Route, Method        string
	WorkBytes, BodyBytes int64
	ElapsedMillis        int64
}

func (rec *Recorder) topRequests(now time.Time) []requestHolder {
	top := make([]requestHolder, 0, 8)
	rec.activeMu.Lock()
	defer rec.activeMu.Unlock()
	for r := range rec.active {
		h := requestHolder{r.route, r.method, r.workBytes.Load(), r.readBytes.Load(), max(0, now.Sub(r.start).Milliseconds())}
		top = keepTop(top, h, h.WorkBytes, func(x requestHolder) int64 { return x.WorkBytes })
	}
	return top
}

func longLivedRoute(route string) bool {
	switch route {
	case "/api/events", "/api/realtime/events", "/api/realtime/socket",
		"/api/agent/sessions/stream", "/api/agent/sessions/{id}/messages/stream",
		"/api/agent/sessions/{id}/messages/{messageId}/stream", "/api/coding/environments/{id}/terminal":
		return true
	}
	return false
}

// Begin and End use fixed storage on the request. The longest instance of
// each stage becomes a span. The totals and counts include repeated stages.
func Begin(ctx context.Context, stage Stage) Timing {
	return begin(ctx, stage, "", "")
}

func begin(ctx context.Context, stage Stage, kind, detail string) Timing {
	r := fromContext(ctx)
	if r == nil {
		return Timing{}
	}
	return Timing{request: r, stage: stage, start: time.Now(), kind: kind, detail: detail}
}

type Timing struct {
	request      *request
	stage        Stage
	start        time.Time
	kind, detail string
}

func (t Timing) End() {
	if t.request != nil {
		t.request.addStage(t.stage, t.start, time.Since(t.start), t.kind, t.detail)
	}
}

func (r *request) addStage(stage Stage, start time.Time, elapsed time.Duration, kind, detail string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	s := &r.stages[stage]
	s.count++
	s.total += elapsed
	if s.count == 1 || elapsed > s.longest {
		s.start, s.longest, s.kind, s.detail = start, elapsed, kind, detail
	}
}

func MutationStage(ctx context.Context, stage, event string, start time.Time, elapsed time.Duration) {
	r := fromContext(ctx)
	if r == nil {
		return
	}
	which := Mutation
	switch stage {
	case "lock_wait":
		which = MutationLock
	case "load", "clone", "reload", "load_metadata", "prepare":
		which = MutationLoad
	case "mutate", "callback":
		which = MutationApply
	case "persist", "records", "progress", "metadata", "ordinal-resident-publication":
		which = MutationPersist
	case "commit", "ordinal-commit":
		which = MutationCommit
	case "work-wait":
		which = WorkAdmission
	case "generation", "image", "ordinal-install":
		which = MetadataInstall
	default:
		return
	}
	r.addStage(which, start, elapsed, enum(event, mutationKinds), "")
}

func BeginMutation(ctx context.Context, event string) Timing {
	if !Enabled() {
		return Timing{}
	}
	return begin(ctx, Mutation, enum(event, mutationKinds), "")
}

type Operation struct {
	rec          *Recorder
	ctx          context.Context
	stage        Stage
	start        time.Time
	kind, detail string
}

func BeginSQL(ctx context.Context, statement string) Operation {
	rec := FromContext(ctx)
	if rec == nil {
		return Operation{}
	}
	kind, table := sqlIdentity(statement)
	return Operation{rec, ctx, SQL, time.Now(), kind, table}
}

func BeginOperation(ctx context.Context, stage Stage, name string) Operation {
	rec := FromContext(ctx)
	if rec == nil {
		return Operation{}
	}
	kind := ""
	switch stage {
	case Job:
		kind = enum(name, jobKinds)
	case MCP:
		kind = rec.safeTool(name)
	case External:
		switch name {
		case "webhook", "model":
			kind = name
		}
	}
	return Operation{rec, ctx, stage, time.Now(), kind, ""}
}

func BeginExternal(ctx context.Context, kind, host string) Operation {
	op := BeginOperation(ctx, External, kind)
	if op.rec != nil {
		// A destination is eligible only when it was allowlisted at startup.
		op.detail = op.rec.safeHost(host)
	}
	return op
}

func (op Operation) End() {
	if op.rec == nil {
		return
	}
	elapsed := time.Since(op.start)
	l := labels{route: Route(op.ctx), kind: op.kind}
	if r := fromContext(op.ctx); r != nil {
		r.addStage(op.stage, op.start, elapsed, op.kind, op.detail)
	}
	switch op.stage {
	case SQL:
		// Tables and hosts are trace-only. This keeps series cardinality
		// independent of the number of tables each route touches.
		op.rec.observe(sqlDuration, l, elapsed.Seconds())
	case External:
		op.rec.observe(externalDuration, l, elapsed.Seconds())
	case MCP:
		op.rec.observe(mcpDuration, l, elapsed.Seconds())
	case Job:
		op.rec.observe(jobDuration, labels{kind: op.kind}, elapsed.Seconds())
	}
}

// DatabaseRetry distinguishes failed transactions and actual retry attempts.
func DatabaseRetry(ctx context.Context, backend, reason string, retry bool) {
	if rec := FromContext(ctx); rec != nil {
		if backend == "" {
			backend = rec.options.DatabaseDriver
		}
		backend = enum(backend, []string{"sqlite", "mysql", "postgres"})
		reason = enum(reason, []string{"deadlock", "serialization", "lock_wait", "busy", "concurrent_write"})
		m := dbFailures
		if retry {
			m = dbRetries
		}
		rec.add(m, labels{route: Route(ctx), kind: backend, status: reason}, 1)
	}
}

func RealtimeMessage(rec *Recorder, transport, kind string, size int) {
	if rec == nil {
		return
	}
	transport = enum(transport, []string{"sse", "sync", "collaboration_in", "collaboration_out"})
	l := labels{kind: enum(kind, messageKinds), method: transport}
	rec.observe(realtimeFrames, l, float64(size))
}

func RecordDocumentBody(ctx context.Context) {
	if r := fromContext(ctx); r != nil {
		r.recorder.observe(documentSize, labels{route: r.route}, float64(r.readBytes.Load()))
	}
}
