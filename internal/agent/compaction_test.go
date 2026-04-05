package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tradecraft/gode/internal/permission"
	"github.com/tradecraft/gode/internal/provider"
	"github.com/tradecraft/gode/internal/session"
	"github.com/tradecraft/gode/internal/storage"
	"github.com/tradecraft/gode/internal/tool"
)

type fakeProvider struct{}

func (fakeProvider) ID() string          { return "fake" }
func (fakeProvider) SupportsTools() bool { return false }

func (fakeProvider) Stream(ctx context.Context, req *provider.Request) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, 4)
	go func() {
		defer close(ch)
		text := "assistant reply"
		if strings.Contains(req.System, "compress coding-assistant transcripts") {
			text = "## Goals\n- keep the important work\n"
		}
		ch <- provider.EventTextDelta{Text: text}
		ch <- provider.EventMessageComplete{StopReason: "end_turn"}
	}()
	return ch
}

func TestCompactStoresMemoryAndTrimsMessages(t *testing.T) {
	store := newTestStore(t)
	sess, err := session.NewSession(store, t.TempDir())
	if err != nil {
		t.Fatalf("new session: %v", err)
	}

	messages := make([]provider.Message, 0, 10)
	for i := 0; i < 5; i++ {
		messages = append(messages,
			provider.UserTextMessage(strings.Repeat("user context ", 80)),
			provider.Message{
				Role: "assistant",
				Content: []interface{}{
					provider.TextBlock{Type: "text", Text: strings.Repeat("assistant context ", 80)},
				},
			},
		)
	}
	if err := session.ReplaceMessages(store, sess.ID, messages); err != nil {
		t.Fatalf("replace messages: %v", err)
	}

	ag := New(Config{
		Provider:      fakeProvider{},
		Tools:         tool.NewRegistry(),
		Permissions:   permission.NewManager(nil),
		Store:         store,
		Session:       sess,
		Model:         "fake-model",
		ContextTokens: 512,
		MaxTokens:     128,
	})
	ag.messages = messages

	result, err := ag.Compact(context.Background())
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if result == nil {
		t.Fatal("expected compaction result")
	}
	if strings.TrimSpace(ag.Session().Memory) == "" {
		t.Fatal("expected session memory to be populated")
	}
	if len(ag.messages) >= len(messages) {
		t.Fatalf("expected message list to be trimmed, got %d >= %d", len(ag.messages), len(messages))
	}

	row, err := store.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if strings.TrimSpace(row.Memory) == "" {
		t.Fatal("expected persisted memory to be populated")
	}

	rows, err := store.GetMessages(sess.ID)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if len(rows) != len(ag.messages) {
		t.Fatalf("expected persisted messages to match compacted in-memory slice, got %d vs %d", len(rows), len(ag.messages))
	}
}

func TestChooseCompactionSplitKeepsRecentTail(t *testing.T) {
	messages := []provider.Message{
		provider.UserTextMessage(strings.Repeat("a", 400)),
		provider.UserTextMessage(strings.Repeat("b", 400)),
		provider.UserTextMessage(strings.Repeat("c", 400)),
		provider.UserTextMessage(strings.Repeat("d", 400)),
		provider.UserTextMessage(strings.Repeat("e", 400)),
		provider.UserTextMessage(strings.Repeat("f", 400)),
	}

	split := chooseCompactionSplit(messages, 256, false)
	if split <= 0 {
		t.Fatalf("expected some messages to be compacted, got split %d", split)
	}

	kept := len(messages) - split
	if kept < minMessagesToKeep {
		t.Fatalf("expected at least %d messages kept, got %d", minMessagesToKeep, kept)
	}
}

func newTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "gode.db"))
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
