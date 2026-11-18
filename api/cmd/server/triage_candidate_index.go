package main

import (
	"crypto/sha256"
	"encoding/binary"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

// triageCandidateIndexOff makes generation scan every candidate as before
// the index (tests compare both).
var triageCandidateIndexOff bool

// triageCandidateIndex is the scan stream of one triage generation: the
// positions similarTriageIssues (legacy) or the bounded visit walks in order,
// each with the title and truncated-description tokens of its value. Values
// with equal token content share one entry. A query returns, in stream order,
// only the positions whose value shares a title or description token with the
// scored issue: every other position scores at most the 0.12 of its team,
// project and label bonuses and is below the 0.14 cut-off, so skipping it
// leaves the scan's accepted sequence unchanged. Titles and descriptions do
// not change during a generation (suggestion actions change assignees,
// projects, labels, teams, states and relations), so a position's tokens stay
// valid while its other fields are read when it is scored.
type triageCandidateIndex struct {
	tokens      map[string]int32
	title, desc [][]int32
	entries     []triageCandidateEntry
	byKey       map[[32]byte]int32
	entryOf     []int32
	firsts      map[string]int32
	// Per-query counters, zero between queries.
	titleHits, descHits []int32
	touched             []int32
	// reserve admits the index's retained memory (bounded); nil for legacy.
	reserve func(uint64) error
}

type triageCandidateEntry struct {
	titleLen, descLen int
	positions         []int32
	value             domain.Issue
}

func newTriageCandidateIndex(reserve func(uint64) error) *triageCandidateIndex {
	return &triageCandidateIndex{tokens: map[string]int32{}, byKey: map[[32]byte]int32{}, firsts: map[string]int32{}, reserve: reserve}
}

func (x *triageCandidateIndex) length() int { return len(x.entryOf) }

// add appends the next stream position, valued issue. value is what the
// bounded scan retains for it (keyed with its token content); legacy reads
// positions from data.Issues and passes the zero value with byValue false.
func (x *triageCandidateIndex) add(issue domain.Issue, value domain.Issue, byValue bool) error {
	key := triageCandidateKey(issue, value, byValue)
	position := int32(len(x.entryOf))
	entry, found := x.byKey[key]
	if !found {
		titleTokens := triageTokens(issue.Title)
		descTokens := triageTokens(truncateTriageText(issue.Description))
		if x.reserve != nil {
			cost := uint64(reflect.TypeFor[triageCandidateEntry]().Size())*2 + 128
			for _, set := range []map[string]bool{titleTokens, descTokens} {
				for token := range set {
					cost += 8
					if _, known := x.tokens[token]; !known {
						cost += uint64(len(token))*2 + 96
					}
				}
			}
			if byValue {
				cost += triageCandidateValueCost(value)
			}
			if err := x.reserve(cost); err != nil {
				return err
			}
		}
		entry = int32(len(x.entries))
		x.entries = append(x.entries, triageCandidateEntry{titleLen: len(titleTokens), descLen: len(descTokens), value: value})
		x.byKey[key] = entry
		for token := range titleTokens {
			id := x.tokenID(token)
			x.title[id] = append(x.title[id], entry)
		}
		for token := range descTokens {
			id := x.tokenID(token)
			x.desc[id] = append(x.desc[id], entry)
		}
		x.titleHits = append(x.titleHits, 0)
		x.descHits = append(x.descHits, 0)
	}
	if x.reserve != nil {
		cost := uint64(16)
		if _, seen := x.firsts[issue.ID]; !seen {
			cost += uint64(len(issue.ID))*2 + 64
		}
		if err := x.reserve(cost); err != nil {
			return err
		}
	}
	x.entries[entry].positions = append(x.entries[entry].positions, position)
	x.entryOf = append(x.entryOf, entry)
	if _, seen := x.firsts[issue.ID]; !seen {
		x.firsts[issue.ID] = position
	}
	return nil
}

func (x *triageCandidateIndex) tokenID(token string) int32 {
	id, found := x.tokens[token]
	if !found {
		id = int32(len(x.title))
		x.tokens[token] = id
		x.title = append(x.title, nil)
		x.desc = append(x.desc, nil)
	}
	return id
}

// first returns the first stream position of issue id.
func (x *triageCandidateIndex) first(id string) (int32, bool) {
	position, found := x.firsts[id]
	return position, found
}

// match returns, ascending, every position whose value shares a title token
// with title or a description token with description, plus extra positions.
func (x *triageCandidateIndex) match(title, description map[string]bool, extra ...int32) []int32 {
	for token := range title {
		if id, found := x.tokens[token]; found {
			for _, entry := range x.title[id] {
				if x.titleHits[entry] == 0 && x.descHits[entry] == 0 {
					x.touched = append(x.touched, entry)
				}
				x.titleHits[entry]++
			}
		}
	}
	for token := range description {
		if id, found := x.tokens[token]; found {
			for _, entry := range x.desc[id] {
				if x.titleHits[entry] == 0 && x.descHits[entry] == 0 {
					x.touched = append(x.touched, entry)
				}
				x.descHits[entry]++
			}
		}
	}
	positions := slices.Clone(extra)
	for _, entry := range x.touched {
		positions = append(positions, x.entries[entry].positions...)
	}
	slices.Sort(positions)
	return slices.Compact(positions)
}

// similarity returns the title and description similarities of position
// after match, exactly as triageSimilarity computes them from the token sets.
func (x *triageCandidateIndex) similarity(position int32, title, description map[string]bool) (float64, float64) {
	entry := x.entryOf[position]
	values := &x.entries[entry]
	return triageSimilarityCount(int(x.titleHits[entry]), len(title), values.titleLen),
		triageSimilarityCount(int(x.descHits[entry]), len(description), values.descLen)
}

// done clears the counters of the last match.
func (x *triageCandidateIndex) done() {
	for _, entry := range x.touched {
		x.titleHits[entry], x.descHits[entry] = 0, 0
	}
	x.touched = x.touched[:0]
}

func (x *triageCandidateIndex) value(position int32) domain.Issue {
	return x.entries[x.entryOf[position]].value
}

// similarTriageIssuesIndexed is similarTriageIssues over data.Issues, whose
// positions it indexes as they are appended.
func (x *triageCandidateIndex) similarTriageIssues(data *domain.Bootstrap, issue *domain.Issue) []triageSuggestionCandidate {
	if issue == nil {
		return nil
	}
	if x == nil || triageCandidateIndexOff || len(data.Issues) < x.length() {
		return similarTriageIssues(data, issue)
	}
	for position := x.length(); position < len(data.Issues); position++ {
		if err := x.add(data.Issues[position], domain.Issue{}, false); err != nil {
			return similarTriageIssues(data, issue)
		}
	}
	s := newTriageIssueScore(issue)
	positions := x.match(s.title, s.description)
	defer x.done()
	candidates := make([]triageSuggestionCandidate, 0, min(len(positions), maxTriageSuggestionCandidates))
	for _, position := range positions {
		if len(candidates) >= maxTriageSuggestionCandidates {
			break
		}
		candidate := data.Issues[position]
		if !s.eligible(candidate) {
			continue
		}
		titleScore, descriptionScore := x.similarity(position, s.title, s.description)
		if scored, ok := s.finish(candidate, titleScore, descriptionScore); ok {
			candidates = append(candidates, scored)
		}
	}
	sortTriageSuggestionCandidates(candidates)
	return candidates
}

// triageCandidateKey identifies a position's token content and, for the
// bounded scan, every field of its retained value.
func triageCandidateKey(issue, value domain.Issue, byValue bool) [32]byte {
	hash := sha256.New()
	write := func(text string) {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(text)))
		hash.Write(size[:])
		hash.Write([]byte(text))
	}
	write(issue.Title)
	write(truncateTriageText(issue.Description))
	if byValue {
		write("value")
		write(value.ID)
		write(value.Identifier)
		write(value.Title)
		stamp, _ := value.UpdatedAt.MarshalBinary()
		write(string(stamp))
		write(value.Team.ID)
		write(value.State.Type)
		if value.ArchivedAt != nil {
			write("archived")
		} else {
			write("")
		}
		for _, item := range []*string{projectID(value.Project), assigneeID(value.Assignee)} {
			if item == nil {
				write("\x00nil")
			} else {
				write(*item)
			}
		}
		for _, label := range value.Labels {
			write(label.ID)
		}
	}
	var key [32]byte
	copy(key[:], hash.Sum(nil))
	return key
}

func projectID(project *domain.ProjectSummary) *string {
	if project == nil {
		return nil
	}
	return &project.ID
}

func assigneeID(user *domain.User) *string {
	if user == nil {
		return nil
	}
	return &user.ID
}

func triageCandidateValueCost(value domain.Issue) uint64 {
	cost := uint64(reflect.TypeFor[domain.Issue]().Size())*2 + 512
	for _, text := range []string{value.ID, value.Identifier, value.Title, value.Team.ID, value.State.Type} {
		cost += uint64(len(text))*2 + 64
	}
	if value.Project != nil {
		cost += uint64(reflect.TypeFor[domain.ProjectSummary]().Size())*2 + uint64(len(value.Project.ID))*2 + 128
	}
	if value.Assignee != nil {
		cost += uint64(reflect.TypeFor[domain.User]().Size())*2 + uint64(len(value.Assignee.ID))*2 + 128
	}
	for _, label := range value.Labels {
		cost += uint64(reflect.TypeFor[domain.IssueLabel]().Size())*2 + uint64(len(label.ID))*2 + 128
	}
	return cost
}
