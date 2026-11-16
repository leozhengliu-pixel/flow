package store

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"slices"
	"sync"

	"flow/api/internal/domain"
)

type issueSuggestionCandidatesKey struct{}

type issueSuggestionCandidateRange struct {
	start, size int64
	maxNative   uint64
	maxEncoded  int
	number      int
	teams       []string
}

type issueSuggestionCandidateMutation struct {
	issue domain.Issue
	owner *ResourceRead
}

// IssueSuggestionCandidates owns the exact ordered search snapshots of one
// generation operation. Compressed groups retain duplicates without retaining
// every decoded issue or an ever-growing callback issue slice.
type IssueSuggestionCandidates struct {
	store     *SQLiteStore
	owner     *ResourceRead
	file      *os.File
	writer    *gzip.Writer
	encoder   *json.Encoder
	groups    map[string]issueSuggestionCandidateRange
	mutations map[string]issueSuggestionCandidateMutation
	key       string
	size      int64
	once      sync.Once
}

func (s *SQLiteStore) BeginIssueSuggestionCandidates(ctx context.Context) (*IssueSuggestionCandidates, error) {
	if !s.BoundedMetadataEnabled() {
		return nil, ErrBoundedMetadataCoverage
	}
	r := &IssueSuggestionCandidates{store: s, owner: &ResourceRead{store: s}, groups: map[string]issueSuggestionCandidateRange{}, mutations: map[string]issueSuggestionCandidateMutation{}}
	// gzip's reusable tables, encoder state and the operation's fixed buffers.
	if err := r.owner.reserve(ctx, 2<<20); err != nil {
		r.Close()
		return nil, err
	}
	scratch, _ := ctx.Value(metadataLogicalScratchKey{}).(metadataLogicalScratch)
	var err error
	r.file, err = os.CreateTemp(scratch.directory, "flow-p21-triage-*")
	if err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}

func (r *IssueSuggestionCandidates) Context(ctx context.Context) context.Context {
	return context.WithValue(ctx, issueSuggestionCandidatesKey{}, r)
}

func issueSuggestionCandidateOwner(ctx context.Context, s *SQLiteStore) *IssueSuggestionCandidates {
	r, _ := ctx.Value(issueSuggestionCandidatesKey{}).(*IssueSuggestionCandidates)
	if r != nil && r.store == s {
		return r
	}
	return nil
}

func (r *IssueSuggestionCandidates) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		if r.writer != nil {
			_ = r.writer.Close()
		}
		if r.file != nil {
			name := r.file.Name()
			_ = r.file.Close()
			_ = os.Remove(name)
		}
		metadataLogicalDiskUsed.Add(-r.size)
		for _, mutation := range r.mutations {
			mutation.owner.Close()
		}
		r.mutations = nil
		r.groups, r.encoder, r.writer, r.file = nil, nil, nil, nil
		r.owner.Close()
	})
}

func (r *IssueSuggestionCandidates) Write(p []byte) (int, error) {
	if !reserveMetadataLogicalBytes(&metadataLogicalDiskUsed, int64(len(p)), metadataLogicalDiskBytes) {
		return 0, ErrMetadataWorkBudget
	}
	n, err := r.file.Write(p)
	r.size += int64(n)
	metadataLogicalDiskUsed.Add(-int64(len(p) - n))
	return n, err
}

func (r *IssueSuggestionCandidates) Start(ctx context.Context, issueID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.file == nil || r.encoder != nil {
		return ErrBoundedMetadataCoverage
	}
	if err := r.owner.reserve(ctx, uint64(len(issueID))*2+256); err != nil {
		return err
	}
	r.key = issueID
	r.groups[issueID] = issueSuggestionCandidateRange{start: r.size}
	if r.writer == nil {
		var err error
		r.writer, err = gzip.NewWriterLevel(r, gzip.BestSpeed)
		if err != nil {
			return err
		}
	} else {
		r.writer.Reset(r)
	}
	r.encoder = json.NewEncoder(r.writer)
	return nil
}

func (r *IssueSuggestionCandidates) Append(ctx context.Context, issue domain.Issue) error {
	if r.encoder == nil {
		return ErrBoundedMetadataCoverage
	}
	work := &ResourceRead{store: r.store}
	defer work.Close()
	if err := work.reserve(ctx, 64<<10); err != nil {
		return err
	}
	native, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(issue), 0)
	if err != nil {
		return err
	}
	encoding, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(issue), 0)
	if err != nil {
		return err
	}
	if err := work.reserve(ctx, native+uint64(encoding)); err != nil {
		return err
	}
	if err := r.encoder.Encode(issue); err != nil {
		return err
	}
	group := r.groups[r.key]
	group.maxNative = max(group.maxNative, native)
	group.maxEncoded = max(group.maxEncoded, encoding)
	group.number = max(group.number, issue.Number)
	if issue.Team.ID != "" && !slices.Contains(group.teams, issue.Team.ID) {
		if err := r.owner.reserve(ctx, uint64(len(issue.Team.ID))*2+128); err != nil {
			return err
		}
		group.teams = append(group.teams, issue.Team.ID)
	}
	r.groups[r.key] = group
	return nil
}

func (r *IssueSuggestionCandidates) Finish() error {
	if r.encoder == nil {
		return ErrBoundedMetadataCoverage
	}
	r.encoder = nil
	if err := r.writer.Close(); err != nil {
		return err
	}
	group := r.groups[r.key]
	group.size = r.size - group.start
	r.groups[r.key] = group
	return nil
}

func (r *IssueSuggestionCandidates) Teams(ids []string) []string {
	var teams []string
	for _, id := range ids {
		for _, team := range r.groups[id].teams {
			if !slices.Contains(teams, team) {
				teams = append(teams, team)
			}
		}
	}
	return teams
}

func (r *IssueSuggestionCandidates) Number(id string) int {
	return r.groups[id].number
}

func (r *IssueSuggestionCandidates) Mutation(id string) (domain.Issue, bool) {
	mutation, found := r.mutations[id]
	return mutation.issue, found
}

// SetMutation retains only the first candidate occurrence that the literal
// callback mutates through its ID lookup. Other duplicate occurrences remain
// their original immutable snapshots.
func (r *IssueSuggestionCandidates) SetMutation(ctx context.Context, issue domain.Issue) error {
	if r.file == nil {
		return errMetadataLogicalClosed
	}
	work := &ResourceRead{store: r.store}
	cost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(issue), 0)
	if err == nil {
		err = work.reserve(ctx, cost*2+1024)
	}
	if err != nil {
		work.Close()
		return err
	}
	prior, found := r.mutations[issue.ID]
	r.mutations[issue.ID] = issueSuggestionCandidateMutation{issue: issue, owner: work}
	if found {
		prior.owner.Close()
	}
	return nil
}

func (r *IssueSuggestionCandidates) KeepResult(ctx context.Context, issue domain.Issue) error {
	if r.file == nil {
		return errMetadataLogicalClosed
	}
	work := &ResourceRead{store: r.store}
	cost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(issue), 0)
	if err == nil {
		err = work.reserve(ctx, cost*2+1024)
	}
	if err != nil {
		work.Close()
		return err
	}
	if !registerMetadataOutputClose(ctx, work.Close) {
		work.Close()
		return ErrBoundedMetadataCoverage
	}
	return nil
}

// Walk releases one decoded record's scratch after visit. A visitor retaining
// that value must first admit it through its independent score owner.
func (r *IssueSuggestionCandidates) Walk(ctx context.Context, id string, visit func(domain.Issue) (bool, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.file == nil {
		return errMetadataLogicalClosed
	}
	group, found := r.groups[id]
	if !found || group.size == 0 {
		return nil
	}
	work := &ResourceRead{store: r.store}
	defer work.Close()
	if err := work.reserve(ctx, group.maxNative*2+uint64(group.maxEncoded)+128<<10); err != nil {
		return err
	}
	reader, err := gzip.NewReader(io.NewSectionReader(r.file, group.start, group.size))
	if err != nil {
		return err
	}
	defer reader.Close()
	decoder := json.NewDecoder(reader)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var issue domain.Issue
		if err := decoder.Decode(&issue); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		more, err := visit(issue)
		if err != nil || !more {
			return err
		}
	}
}

// IssueSuggestionScores owns the at-most-2000 retained qualifying snapshots of
// one issue's scoring pass. It ends before the next issue is scored.
type IssueSuggestionScores struct {
	owner *ResourceRead
}

func (r *IssueSuggestionCandidates) BeginScores(ctx context.Context) (*IssueSuggestionScores, error) {
	if r.file == nil {
		return nil, errMetadataLogicalClosed
	}
	owner := &ResourceRead{store: r.store}
	if err := owner.reserve(ctx, 128<<10); err != nil {
		owner.Close()
		return nil, err
	}
	return &IssueSuggestionScores{owner: owner}, nil
}

func (r *IssueSuggestionScores) Keep(ctx context.Context, issue domain.Issue) error {
	if r == nil || r.owner == nil {
		return errMetadataLogicalClosed
	}
	cost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(issue), 0)
	if err != nil {
		return err
	}
	if cost > (metadataWorkBytes-1024)/2 {
		return ErrMetadataWorkBudget
	}
	return r.owner.reserve(ctx, cost*2+1024)
}

// Project retains the fields read after similarity scoring: identity/display,
// the stable sort clock, team/project/assignee IDs and ordered label IDs.
// Other fields have already contributed to the numeric scores. The allowance
// counts concrete Go containers and their selected strings rather than the
// decoder work of a complete Issue for every retained candidate.
func (r *IssueSuggestionScores) Project(ctx context.Context, issue domain.Issue) (domain.Issue, error) {
	if r == nil || r.owner == nil {
		return domain.Issue{}, errMetadataLogicalClosed
	}
	if err := ctx.Err(); err != nil {
		return domain.Issue{}, err
	}
	cost := uint64(reflect.TypeFor[domain.Issue]().Size())*2 + 1024
	add := func(n uint64) error {
		if n > metadataWorkBytes || cost > metadataWorkBytes-n {
			return ErrMetadataWorkBudget
		}
		cost += n
		return nil
	}
	text := func(value string) error { return add(uint64(len(value))*2 + 64) }
	title := issue.Title
	if issue.Identifier != "" {
		title = ""
	}
	for _, value := range []string{issue.ID, issue.Identifier, title, issue.Team.ID} {
		if err := text(value); err != nil {
			return domain.Issue{}, err
		}
	}
	if issue.Project != nil {
		if err := add(uint64(reflect.TypeFor[domain.ProjectSummary]().Size())*2 + 128); err != nil {
			return domain.Issue{}, err
		}
		if err := text(issue.Project.ID); err != nil {
			return domain.Issue{}, err
		}
	}
	if issue.Assignee != nil {
		if err := add(uint64(reflect.TypeFor[domain.User]().Size())*2 + 128); err != nil {
			return domain.Issue{}, err
		}
		if err := text(issue.Assignee.ID); err != nil {
			return domain.Issue{}, err
		}
	}
	for _, label := range issue.Labels {
		if err := add(uint64(reflect.TypeFor[domain.IssueLabel]().Size())*2 + 128); err != nil {
			return domain.Issue{}, err
		}
		if err := text(label.ID); err != nil {
			return domain.Issue{}, err
		}
	}
	if err := r.owner.reserve(ctx, cost); err != nil {
		return domain.Issue{}, err
	}
	value := domain.Issue{ID: issue.ID, Identifier: issue.Identifier, Title: title, UpdatedAt: issue.UpdatedAt, Team: domain.Team{ID: issue.Team.ID}}
	if issue.Project != nil {
		value.Project = &domain.ProjectSummary{ID: issue.Project.ID}
	}
	if issue.Assignee != nil {
		value.Assignee = &domain.User{ID: issue.Assignee.ID}
	}
	if len(issue.Labels) != 0 {
		value.Labels = make([]domain.IssueLabel, len(issue.Labels))
		for index, label := range issue.Labels {
			value.Labels[index].ID = label.ID
		}
	}
	return value, nil
}

func (r *IssueSuggestionScores) Close() {
	if r != nil && r.owner != nil {
		r.owner.Close()
		r.owner = nil
	}
}
