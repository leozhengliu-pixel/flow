package store

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// The search indexes are rebuilt once without stopwords. Versions in the
// data-migration range keep them apart from the schema list; they are applied
// by the index ensure stages, not runDataMigrations.
const (
	issueSearchStopwordsMigrationVersion    = 1004
	metadataSearchStopwordsMigrationVersion = 1005
)

// ensureMySQLNgramIndex creates a FULLTEXT ngram index without InnoDB's
// stopword list. The default list holds "a" and "i", and the ngram parser drops
// every bigram that contains a stopword, so with it most English words
// ("UAT", "data", "billing") have no indexed tokens and a phrase match finds
// nothing. InnoDB records the stopword setting of the session that creates
// the table's first FULLTEXT index and keeps it for later reads, so it is
// turned off only on that session.
//
// An index created by an earlier release still uses the stopwords; it is
// dropped and rebuilt once, recorded under version. The drop and the create
// must be separate statements: a combined ALTER keeps the old setting. Any
// later statement that rebuilds the table (ALTER ... FORCE, ADD COLUMN,
// OPTIMIZE TABLE) must run through this path too, or the stopwords return.
//
// optional tolerates a user without the INDEX privilege, for indexes whose
// search has a fallback.
func (s *SQLiteStore) ensureMySQLNgramIndex(ctx context.Context, table, index string, version int, optional bool) error {
	applied, err := s.migrationApplied(ctx, version)
	if err != nil {
		return err
	}
	conn, err := s.db.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SET SESSION innodb_ft_enable_stopword=OFF`); err != nil {
		return fmt.Errorf("disable full-text stopwords: %w", err)
	}
	// The connection returns to the pool afterwards.
	defer conn.ExecContext(context.WithoutCancel(ctx), `SET SESSION innodb_ft_enable_stopword=DEFAULT`)
	began := time.Now()
	if !applied {
		if _, err := conn.ExecContext(ctx, `DROP INDEX `+index+` ON `+table); err != nil && !isMissingIndex(err) {
			if isMySQLSearchPermissionError(err) {
				slog.Warn("full-text index keeps InnoDB stopwords; grant INDEX on the table to rebuild it", "table", table, "error", err)
				return nil
			}
			return err
		}
	}
	if _, err := conn.ExecContext(ctx, `CREATE FULLTEXT INDEX `+index+` ON `+table+`(content) WITH PARSER ngram`); err != nil && !isDuplicateIndex(err) {
		if optional && isMySQLSearchPermissionError(err) {
			return nil
		}
		return err
	}
	if applied {
		return nil
	}
	if elapsed := time.Since(began); elapsed >= time.Second {
		slog.Info("rebuilt full-text index without stopwords", "table", table, "ms", elapsed.Milliseconds())
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO schema_migrations(version,name,applied_at) VALUES(?,?,?)`, version, table+" full-text index without stopwords", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		// Another instance starting at the same time may have recorded it.
		if applied, checkErr := s.migrationApplied(ctx, version); checkErr == nil && applied {
			return nil
		}
		return err
	}
	return nil
}

// InnoDB refuses a full-text phrase of more than 128 tokens with Error 191
// "Too many words in a FTS phrase or proximity search"; with the ngram parser
// a CJK run of n characters is n-1 tokens, so a 130-character title is
// already too long. A phrase here holds at most mysqlPhraseMaxRunes
// characters that are not spaces (an upper bound on its tokens for every
// ngram_token_size), and a longer text is searched as at most
// mysqlPhraseMaxParts such phrases that must all match.
const (
	mysqlPhraseMaxRunes = 64
	mysqlPhraseMaxParts = 16
)

// mysqlBooleanPhrase is the BOOLEAN MODE query that finds text: the quoted
// phrase when it fits, otherwise its first mysqlPhraseMaxParts distinct
// pieces, cut at spaces where it can and inside long words (a CJK run)
// where it must, each required (+"…" +"…"). Every row holding the whole text
// still matches; the pieces need not be adjacent.
func mysqlBooleanPhrase(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\"", " "))
	parts := mysqlPhraseParts(text)
	if len(parts) == 1 {
		return "\"" + parts[0] + "\""
	}
	quoted := make([]string, len(parts))
	for index, part := range parts {
		quoted[index] = "+\"" + part + "\""
	}
	return strings.Join(quoted, " ")
}

// mysqlPhraseParts splits text into distinct pieces of at most
// mysqlPhraseMaxRunes non-space characters (at most mysqlPhraseMaxParts of
// them); a text that fits is one piece, unchanged.
func mysqlPhraseParts(text string) []string {
	words := strings.Fields(text)
	total := 0
	for _, word := range words {
		total += utf8.RuneCountInString(word)
	}
	if total <= mysqlPhraseMaxRunes {
		return []string{text}
	}
	parts := []string{}
	var current []string
	size := 0
	flush := func() {
		if len(current) == 0 {
			return
		}
		part := strings.Join(current, " ")
		current, size = nil, 0
		if !slices.Contains(parts, part) {
			parts = append(parts, part)
		}
	}
	for _, word := range words {
		runes := []rune(word)
		for len(runes) > 0 && len(parts) < mysqlPhraseMaxParts {
			room := mysqlPhraseMaxRunes - size
			if len(runes) <= room {
				current, size = append(current, string(runes)), size+len(runes)
				break
			}
			if size > 0 && len(runes) <= mysqlPhraseMaxRunes {
				// The word fits a piece of its own.
				flush()
				continue
			}
			// A word longer than a piece (a CJK run) fills this one.
			current = append(current, string(runes[:room]))
			runes = runes[room:]
			size = mysqlPhraseMaxRunes
			flush()
		}
		if len(parts) >= mysqlPhraseMaxParts {
			break
		}
	}
	if len(parts) < mysqlPhraseMaxParts {
		flush()
	}
	// A last piece of one character is shorter than an ngram token and
	// could match nothing; it is dropped (the pieces before it remain).
	if last := parts[len(parts)-1]; len(parts) > 1 && utf8.RuneCountInString(strings.ReplaceAll(last, " ", "")) < 2 {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func isMissingIndex(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "check that column/key exists") || strings.Contains(msg, "error 1091")
}
