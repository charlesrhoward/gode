package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBuildMLXVLMPromptIncludesTranscript(t *testing.T) {
	messages := []Message{
		UserTextMessage("Summarize this repo."),
		{
			Role: "assistant",
			Content: []interface{}{
				TextBlock{Type: "text", Text: "I can help with that."},
			},
		},
	}

	prompt := buildMLXVLMPrompt(messages)

	if !strings.Contains(prompt, "User:\nSummarize this repo.") {
		t.Fatalf("expected user message in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "Assistant:\nI can help with that.") {
		t.Fatalf("expected assistant message in prompt, got %q", prompt)
	}
	if !strings.HasSuffix(prompt, "Assistant:\n") {
		t.Fatalf("expected prompt to end with assistant cue, got %q", prompt)
	}
}

func TestConvertMessagesForOllamaMapsToolResults(t *testing.T) {
	callInput := json.RawMessage(`{"path":"main.go"}`)
	messages := []Message{
		UserTextMessage("Read main.go"),
		{
			Role: "assistant",
			Content: []interface{}{
				TextBlock{Type: "text", Text: "I'll inspect it."},
				ToolUseBlock{Type: "tool_use", ID: "tool-1", Name: "read_file", Input: callInput},
			},
		},
		ToolResultMessage("tool-1", "package main", false),
	}

	out := convertMessagesForOllama("system prompt", messages)

	if len(out) != 4 {
		t.Fatalf("expected 4 translated messages, got %d", len(out))
	}
	if out[0].Role != "system" {
		t.Fatalf("expected first message to be system, got %q", out[0].Role)
	}
	if len(out[2].ToolCalls) != 1 || out[2].ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("expected assistant tool call to be preserved, got %+v", out[2].ToolCalls)
	}
	if out[3].Role != "tool" || out[3].ToolName != "read_file" || out[3].Content != "package main" {
		t.Fatalf("expected tool result message, got %+v", out[3])
	}
}

func TestBuildMLXVLMArgsDisablesVerboseDump(t *testing.T) {
	args := buildMLXVLMArgs(&Request{
		Model:     "mlx-community/gemma-4-31b-8bit",
		Messages:  []Message{UserTextMessage("hello")},
		MaxTokens: 128,
	})

	if !slices.Contains(args, "--verbose") {
		t.Fatalf("expected MLX args to include --verbose to disable CLI debug output, got %v", args)
	}
}

func TestMLXOutputFilterStopsAtNextTurn(t *testing.T) {
	filter := newMLXOutputFilter()
	input := "Gravity keeps us anchored to Earth.\n\nUser:\nwhat is mass\n"

	var out strings.Builder
	for _, r := range input {
		out.WriteString(filter.PushRune(r))
	}
	out.WriteString(filter.Flush())

	got := out.String()
	if strings.Contains(got, "User:") {
		t.Fatalf("expected filter to stop before next user turn, got %q", got)
	}
	if !strings.Contains(got, "Gravity keeps us anchored to Earth.") {
		t.Fatalf("expected assistant text to be preserved, got %q", got)
	}
}

func TestMLXVLMStreamDisablesVerboseAndStopsAtNextTurn(t *testing.T) {
	dir := t.TempDir()
	fakePython := filepath.Join(dir, "fake-python")
	script := `#!/bin/sh
found=0
for arg in "$@"; do
  if [ "$arg" = "--verbose" ]; then
    found=1
  fi
done
if [ "$found" -ne 1 ]; then
  echo "missing --verbose" >&2
  exit 1
fi
printf 'Gravity is what pulls us down.\n\nUser:\nwhat is mass\n'
`
	if err := os.WriteFile(fakePython, []byte(script), 0755); err != nil {
		t.Fatalf("write fake python: %v", err)
	}

	prov, err := NewMLXVLM(MLXVLMConfig{PythonPath: fakePython})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	stream := prov.Stream(context.Background(), &Request{
		Model:     "mlx-community/gemma-4-31b-8bit",
		Messages:  []Message{UserTextMessage("hello")},
		MaxTokens: 64,
	})

	var out strings.Builder
	for evt := range stream {
		switch e := evt.(type) {
		case EventTextDelta:
			out.WriteString(e.Text)
		case EventError:
			t.Fatalf("stream error: %v", e.Err)
		}
	}

	got := out.String()
	if strings.Contains(got, "User:") {
		t.Fatalf("expected streamed output to stop before next turn, got %q", got)
	}
	if !strings.Contains(got, "Gravity is what pulls us down.") {
		t.Fatalf("expected assistant text to be preserved, got %q", got)
	}
}

func TestMLXVLMRuntimeInfoReportsResolvedPaths(t *testing.T) {
	dir := t.TempDir()
	fakePython := filepath.Join(dir, "fake-python")
	script := `#!/bin/sh
printf '/tmp/site-packages/mlx_vlm/generate.py\n'
`
	if err := os.WriteFile(fakePython, []byte(script), 0755); err != nil {
		t.Fatalf("write fake python: %v", err)
	}

	prov, err := NewMLXVLM(MLXVLMConfig{PythonPath: fakePython})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	info, err := prov.RuntimeInfo(context.Background())
	if err != nil {
		t.Fatalf("runtime info: %v", err)
	}
	if info.PythonPath != fakePython {
		t.Fatalf("expected python path %q, got %q", fakePython, info.PythonPath)
	}
	if info.BackendPath != "/tmp/site-packages/mlx_vlm/generate.py" {
		t.Fatalf("expected backend path to be reported, got %q", info.BackendPath)
	}
}
