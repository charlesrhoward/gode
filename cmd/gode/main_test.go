package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/tradecraft/gode/internal/agent"
	"github.com/tradecraft/gode/internal/permission"
	"github.com/tradecraft/gode/internal/provider"
	"github.com/tradecraft/gode/internal/session"
	"github.com/tradecraft/gode/internal/storage"
	"github.com/tradecraft/gode/internal/tool"
)

func TestRunHeadlessRequiresExplicitAutoApprove(t *testing.T) {
	tool := &recordTool{}
	toolCalls := runHeadlessWithTool(t, tool, false)
	if toolCalls != 0 {
		t.Fatalf("expected tool to be blocked without auto-approve, got %d calls", toolCalls)
	}
}

func TestRunHeadlessAutoApproveExecutesTools(t *testing.T) {
	tool := &recordTool{}
	toolCalls := runHeadlessWithTool(t, tool, true)
	if toolCalls != 1 {
		t.Fatalf("expected tool to run once with auto-approve, got %d calls", toolCalls)
	}
}

func runHeadlessWithTool(t *testing.T, record *recordTool, autoApprove bool) int {
	t.Helper()

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "gode.db"))
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	sess, err := session.NewSession(store, t.TempDir())
	if err != nil {
		t.Fatalf("new session: %v", err)
	}

	registry := tool.NewRegistry()
	registry.Register(record)

	perms := permission.NewManager(nil)
	ag := agent.New(agent.Config{
		Provider:      scriptedToolProvider{},
		Tools:         registry,
		Permissions:   perms,
		Store:         store,
		Session:       sess,
		Model:         "fake-model",
		ContextTokens: 2048,
		MaxTokens:     256,
	})

	if err := runHeadless(context.Background(), ag, perms, "run the tool", nil, autoApprove); err != nil {
		t.Fatalf("run headless: %v", err)
	}

	return record.calls
}

type scriptedToolProvider struct{}

func (scriptedToolProvider) ID() string          { return "scripted" }
func (scriptedToolProvider) SupportsTools() bool { return true }

func (scriptedToolProvider) Stream(ctx context.Context, req *provider.Request) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, 4)
	go func() {
		defer close(ch)
		if hasToolResult(req.Messages) {
			ch <- provider.EventTextDelta{Text: "done"}
			ch <- provider.EventMessageComplete{StopReason: "end_turn"}
			return
		}
		ch <- provider.EventToolUseStart{ID: "tool-1", Name: "record"}
		ch <- provider.EventToolUseEnd{
			ID:    "tool-1",
			Name:  "record",
			Input: json.RawMessage(`{"value":"ok"}`),
		}
		ch <- provider.EventMessageComplete{StopReason: "tool_use"}
	}()
	return ch
}

func hasToolResult(messages []provider.Message) bool {
	for _, msg := range messages {
		for _, block := range msg.Content {
			switch b := block.(type) {
			case provider.ToolResultBlock:
				return true
			case map[string]interface{}:
				if blockType, _ := b["type"].(string); blockType == "tool_result" {
					return true
				}
			}
		}
	}
	return false
}

type recordTool struct {
	calls int
}

func (t *recordTool) Name() string { return "record" }

func (t *recordTool) Description() string {
	return "Records whether the tool was executed."
}

func (t *recordTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}

func (t *recordTool) Permission() permission.Level {
	return permission.WorkspaceWrite
}

func (t *recordTool) Execute(ctx context.Context, input json.RawMessage) (*tool.Result, error) {
	t.calls++
	return &tool.Result{Output: "executed"}, nil
}
