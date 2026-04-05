package session

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tradecraft/gode/internal/storage"
)

func TestGetOrCreateReturnsLookupError(t *testing.T) {
	store := newTestStore(t)
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	_, err := GetOrCreate(store, t.TempDir())
	if err == nil {
		t.Fatal("expected lookup error")
	}
	if !strings.Contains(err.Error(), "finding session") {
		t.Fatalf("expected lookup context, got %v", err)
	}
}

func TestLoadMessagesReturnsDecodeError(t *testing.T) {
	store := newTestStore(t)
	sess, err := NewSession(store, t.TempDir())
	if err != nil {
		t.Fatalf("new session: %v", err)
	}

	if err := store.AddMessage(&storage.MessageRow{
		ID:        "msg-1",
		SessionID: sess.ID,
		Role:      "user",
		Content:   json.RawMessage("{"),
	}); err != nil {
		t.Fatalf("add invalid message: %v", err)
	}

	_, err = LoadMessages(store, sess.ID)
	if err == nil {
		t.Fatal("expected decode error")
	}
	if !strings.Contains(err.Error(), "decoding stored message") {
		t.Fatalf("expected decode context, got %v", err)
	}
}

func newTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "gode.db"))
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}
