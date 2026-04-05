package storage

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestListSessionsReturnsTimestampError(t *testing.T) {
	store := newTestStore(t)

	_, err := store.db.Exec(
		"INSERT INTO sessions (id, title, memory, directory, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		"sess-1", "title", "", t.TempDir(), "not-a-time", "still-not-a-time",
	)
	if err != nil {
		t.Fatalf("insert session row: %v", err)
	}

	_, err = store.ListSessions()
	if err == nil {
		t.Fatal("expected timestamp parse error")
	}
	if !strings.Contains(err.Error(), "parsing session") {
		t.Fatalf("expected session parse context, got %v", err)
	}
}

func TestGetMessagesReturnsTimestampError(t *testing.T) {
	store := newTestStore(t)

	if err := store.CreateSession(&SessionRow{
		ID:        "sess-1",
		Directory: t.TempDir(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err := store.db.Exec(
		"INSERT INTO messages (id, session_id, role, content, usage_input, usage_output, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		"msg-1", "sess-1", "user", "[]", 0, 0, "bad-timestamp",
	)
	if err != nil {
		t.Fatalf("insert message row: %v", err)
	}

	_, err = store.GetMessages("sess-1")
	if err == nil {
		t.Fatal("expected timestamp parse error")
	}
	if !strings.Contains(err.Error(), "parsing message") {
		t.Fatalf("expected message parse context, got %v", err)
	}
}

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "gode.db"))
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}
