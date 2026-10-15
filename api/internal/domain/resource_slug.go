package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"time"
	"unicode"
)

// Project and initiative URLs read `<name-slug>-<12 hex>` (as the reference app's do):
// the hex suffix is stable for the record's life, so a link keeps working when
// the name (and so the prefix) changes, and two records with the same name get
// distinct URLs. Links are resolved by the suffix; the prefix is cosmetic.

// SlugSuffixLength is the number of hex characters in a stable slug suffix.
const SlugSuffixLength = 12

// slugPrefixMaxRunes bounds the name-derived part of a slug.
const slugPrefixMaxRunes = 60

// previousSlugLimit bounds the old slugs a record remembers.
const previousSlugLimit = 20

// Slugify lowercases a name and joins its letter and digit runs with dashes.
func Slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			builder.WriteRune(character)
			lastDash = false
		} else if builder.Len() > 0 && !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

// NewSlugSuffix returns a random 12-hex slug suffix.
func NewSlugSuffix() string {
	random := make([]byte, SlugSuffixLength/2)
	if _, err := rand.Read(random); err != nil {
		return DerivedSlugSuffix(time.Now().String())
	}
	return hex.EncodeToString(random)
}

// DerivedSlugSuffix is a deterministic 12-hex suffix for a record id: the data
// migration uses it so concurrent instances assign the same slug.
func DerivedSlugSuffix(id string) string {
	sum := sha256.Sum256([]byte("flow-slug:" + id))
	return hex.EncodeToString(sum[:SlugSuffixLength/2])
}

// SlugWithSuffix builds `<name-slug>-<suffix>`; an empty name becomes
// `untitled-<suffix>`.
func SlugWithSuffix(name, suffix string) string {
	base := Slugify(name)
	if runes := []rune(base); len(runes) > slugPrefixMaxRunes {
		base = strings.Trim(string(runes[:slugPrefixMaxRunes]), "-")
	}
	if base == "" {
		base = "untitled"
	}
	return base + "-" + suffix
}

func isSlugSuffix(value string) bool {
	if len(value) != SlugSuffixLength {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

// SlugSuffix is the stable 12-hex suffix of a slug (`name-1a2b3c4d5e6f`, or a
// bare `1a2b3c4d5e6f`), or "" when the slug has none (a legacy name-only slug).
func SlugSuffix(slugID string) string {
	slugID = strings.ToLower(strings.TrimSpace(slugID))
	if isSlugSuffix(slugID) {
		return slugID
	}
	index := strings.LastIndex(slugID, "-")
	if index < 0 || !isSlugSuffix(slugID[index+1:]) {
		return ""
	}
	return slugID[index+1:]
}

// RenamedSlug moves a slug to follow a new name, keeping its stable suffix
// (or giving a legacy slug one), and remembers the old slug in previous so
// existing links keep resolving.
func RenamedSlug(current string, previous []string, name string) (string, []string) {
	suffix := SlugSuffix(current)
	if suffix == "" {
		suffix = NewSlugSuffix()
	}
	next := SlugWithSuffix(name, suffix)
	return next, RememberSlug(previous, current, next)
}

// RememberSlug adds old to previous (bounded, without next).
func RememberSlug(previous []string, old, next string) []string {
	if old != "" && old != next && !slices.Contains(previous, old) {
		previous = append(slices.Clone(previous), old)
		if len(previous) > previousSlugLimit {
			previous = previous[len(previous)-previousSlugLimit:]
		}
	}
	if slices.Contains(previous, next) {
		previous = slices.DeleteFunc(slices.Clone(previous), func(value string) bool { return value == next })
	}
	return previous
}

// SlugMatch says how a reference names a record (best first).
type SlugMatch int

const (
	SlugNoMatch SlugMatch = iota
	// SlugMatchPrevious: a slug the record used before (a legacy name-only
	// slug kept by the migration, or a pre-rename slug without a suffix).
	SlugMatchPrevious
	// SlugMatchSuffix: the stable suffix matches (stale or missing prefix).
	SlugMatchSuffix
	// SlugMatchExact: the id or the current slug.
	SlugMatchExact
)

// MatchSlugRef reports how ref (an id, a current or stale slug, or a bare
// suffix) names a record with the given id, slug and previous slugs.
func MatchSlugRef(ref, id, slugID string, previous []string) SlugMatch {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return SlugNoMatch
	}
	if ref == id || ref == slugID {
		return SlugMatchExact
	}
	if suffix := SlugSuffix(ref); suffix != "" && suffix == SlugSuffix(slugID) {
		return SlugMatchSuffix
	}
	if slices.Contains(previous, ref) {
		return SlugMatchPrevious
	}
	return SlugNoMatch
}

// IndexBySlugRef returns the index of the record ref names best (the first
// one among equally good matches), or -1.
func IndexBySlugRef[T any](items []T, ref string, key func(T) (id, slugID string, previous []string)) int {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return -1
	}
	suffix := SlugSuffix(ref)
	best, bestMatch := -1, SlugNoMatch
	for index, item := range items {
		id, slugID, previous := key(item)
		match := SlugNoMatch
		switch {
		case ref == id || ref == slugID:
			return index
		case suffix != "" && bestMatch < SlugMatchSuffix && strings.HasSuffix(slugID, suffix) && SlugSuffix(slugID) == suffix:
			match = SlugMatchSuffix
		case bestMatch < SlugMatchPrevious && slices.Contains(previous, ref):
			match = SlugMatchPrevious
		}
		if match > bestMatch {
			best, bestMatch = index, match
		}
	}
	return best
}

// ProjectSlugKey is the IndexBySlugRef key for projects.
func ProjectSlugKey(project Project) (string, string, []string) {
	return project.ID, project.SlugID, project.PreviousSlugIDs
}

// InitiativeSlugKey is the IndexBySlugRef key for initiatives.
func InitiativeSlugKey(initiative Initiative) (string, string, []string) {
	return initiative.ID, initiative.SlugID, initiative.PreviousSlugIDs
}

// ProjectIndexBySlugRef finds the project an id or (current, stale or legacy)
// slug names.
func ProjectIndexBySlugRef(projects []Project, ref string) int {
	return IndexBySlugRef(projects, ref, ProjectSlugKey)
}

// InitiativeIndexBySlugRef finds the initiative an id or (current, stale or
// legacy) slug names.
func InitiativeIndexBySlugRef(initiatives []Initiative, ref string) int {
	return IndexBySlugRef(initiatives, ref, InitiativeSlugKey)
}

// LoopSlugID is a loop's URL slug, `<name-slug>-<12 hex>`. The suffix is
// derived from the loop id, so it is stable for the loop's life without being
// stored; only the name prefix follows renames.
func LoopSlugID(loop Loop) string {
	return SlugWithSuffix(loop.Name, DerivedSlugSuffix(loop.ID))
}

// LoopIndexBySlugRef finds the loop an id or a (current or stale-prefix) slug
// names, or -1.
func LoopIndexBySlugRef(loops []Loop, ref string) int {
	return IndexBySlugRef(loops, ref, func(loop Loop) (string, string, []string) {
		return loop.ID, LoopSlugID(loop), nil
	})
}

// StableSlug gives a slug without a stable suffix (a legacy name-only slug)
// one derived from the record id, keeping the old slug in previous so links
// made with it still resolve. changed is false when the slug already has one.
func StableSlug(id, name, slugID string, previous []string) (next string, nextPrevious []string, changed bool) {
	if SlugSuffix(slugID) != "" {
		return slugID, previous, false
	}
	next = SlugWithSuffix(name, DerivedSlugSuffix(id))
	return next, RememberSlug(previous, slugID, next), true
}
