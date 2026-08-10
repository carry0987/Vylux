package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"Vylux/internal/db/dbq"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type tombstoneLookupDB struct {
	exactSource string
	seenSources []string
}

func (d *tombstoneLookupDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec")
}

func (d *tombstoneLookupDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("unexpected Query")
}

func (d *tombstoneLookupDB) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	if len(args) != 2 {
		return tombstoneLookupRow{err: fmt.Errorf("expected hash and source arguments, got %d", len(args))}
	}
	source, ok := args[1].(string)
	if !ok {
		return tombstoneLookupRow{err: fmt.Errorf("source argument has type %T", args[1])}
	}
	d.seenSources = append(d.seenSources, source)
	return tombstoneLookupRow{exists: source == d.exactSource}
}

type tombstoneLookupRow struct {
	exists bool
	err    error
}

func (r tombstoneLookupRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 1 {
		return fmt.Errorf("expected one scan destination, got %d", len(dest))
	}
	exists, ok := dest[0].(*bool)
	if !ok {
		return fmt.Errorf("scan destination has type %T", dest[0])
	}
	*exists = r.exists
	return nil
}

func TestRejectTombstonedKeepsWhitespaceDistinctSourcesSeparate(t *testing.T) {
	hash := strings.Repeat("a", 64)
	rawSource := "uploads/ " + hash + "-upload-id.png "
	canonicalSource := "uploads/" + hash + "-upload-id.png"
	database := &tombstoneLookupDB{exactSource: rawSource}
	queries := dbq.New(database)

	if err := RejectTombstoned(t.Context(), queries, hash, rawSource); !errors.Is(err, ErrTombstoned) {
		t.Fatalf("exact tombstone lookup returned %v", err)
	}
	if err := RejectTombstoned(t.Context(), queries, hash, canonicalSource); err != nil {
		t.Fatalf("whitespace-distinct lookup matched the exact tombstone: %v", err)
	}
	want := []string{rawSource, canonicalSource}
	if len(database.seenSources) != len(want) {
		t.Fatalf("lookup sources = %#v, want %#v", database.seenSources, want)
	}
	for index, source := range want {
		if database.seenSources[index] != source {
			t.Fatalf("lookup source %d = %q, want exact %q", index, database.seenSources[index], source)
		}
	}
}
