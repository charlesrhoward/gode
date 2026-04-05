package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/tradecraft/gode/internal/config"
	"github.com/tradecraft/gode/internal/permission"
	"github.com/tradecraft/gode/internal/provider"
	"github.com/tradecraft/gode/internal/session"
	"github.com/tradecraft/gode/internal/storage"
	"github.com/tradecraft/gode/internal/tool"
)

// Event types emitted by the agent to the TUI.
type Event interface {
	agentEvent()
}

type EventText struct{ Text string }
type EventToolStart struct{ ID, Name, Detail string }
type EventToolEnd struct {
	ID     string
	Name   string
	Result *tool.Result
}
type EventTurnDone struct {
	Usage      provider.Usage
	TotalUsage provider.Usage
	Duration   time.Duration
}
type EventPermission struct {
	Tool   string
	Detail string
	Result chan permission.Action
}
type EventCompacted struct {
	Trigger           string
	BeforeTokens      int
	AfterTokens       int
	BudgetTokens      int
	CompactedMessages int
	RetainedMessages  int
}
type EventError struct{ Err error }

func (EventText) agentEvent()       {}
func (EventToolStart) agentEvent()  {}
func (EventToolEnd) agentEvent()    {}
func (EventTurnDone) agentEvent()   {}
func (EventPermission) agentEvent() {}
func (EventCompacted) agentEvent()  {}
func (EventError) agentEvent()      {}

type Config struct {
	Provider      provider.Provider
	Tools         *tool.Registry
	Permissions   *permission.Manager
	Store         *storage.SQLiteStore
	Session       *session.Session
	Model         string
	ContextTokens int
	MaxTokens     int
}

type Agent struct {
	cfg        Config
	events     chan Event
	messages   []provider.Message
	totalUsage provider.Usage
}

// EventHistory is emitted on startup to replay prior conversation messages to the TUI.
type EventHistory struct {
	Messages []HistoryMessage
}

type HistoryMessage struct {
	Role    string
	Content string
	Tools   []HistoryTool
}

type HistoryTool struct {
	Name   string
	Output string
}

func (EventHistory) agentEvent() {}

func New(cfg Config) *Agent {
	a := &Agent{
		cfg:    cfg,
		events: make(chan Event, 64),
	}

	// Restore conversation history from prior session
	if msgs, err := session.LoadMessages(cfg.Store, cfg.Session.ID); err == nil && len(msgs) > 0 {
		a.messages = msgs
		a.emitHistory(msgs)
	}

	return a
}

// emitHistory converts stored messages into EventHistory for the TUI to display.
// Handles both concrete types (fresh messages) and map[string]interface{} (from JSON roundtrip).
func (a *Agent) emitHistory(msgs []provider.Message) {
	var history []HistoryMessage
	lastAssistantIdx := -1

	for _, msg := range msgs {
		hm := HistoryMessage{Role: msg.Role}

		for _, block := range msg.Content {
			// Handle concrete struct types (fresh, in-memory messages)
			switch b := block.(type) {
			case provider.TextBlock:
				hm.Content += b.Text
				continue
			case provider.ToolUseBlock:
				hm.Tools = append(hm.Tools, HistoryTool{Name: b.Name})
				continue
			case provider.ToolResultBlock:
				lastAssistantIdx = attachHistoryToolOutput(history, lastAssistantIdx, b.Content)
				continue
			}

			// Handle map types (from JSON roundtrip via SQLite)
			if b, ok := block.(map[string]interface{}); ok {
				blockType, _ := b["type"].(string)
				switch blockType {
				case "text":
					if text, ok := b["text"].(string); ok {
						hm.Content += text
					}
				case "tool_use":
					name, _ := b["name"].(string)
					hm.Tools = append(hm.Tools, HistoryTool{Name: name})
				case "tool_result":
					content, _ := b["content"].(string)
					lastAssistantIdx = attachHistoryToolOutput(history, lastAssistantIdx, content)
				}
			}
		}

		if hm.Content != "" || len(hm.Tools) > 0 {
			history = append(history, hm)
			if hm.Role == "assistant" {
				lastAssistantIdx = len(history) - 1
			}
		}
	}

	if len(history) > 0 {
		a.events <- EventHistory{Messages: history}
	}
}

func attachHistoryToolOutput(history []HistoryMessage, lastAssistantIdx int, output string) int {
	if lastAssistantIdx >= 0 && lastAssistantIdx < len(history) {
		for i := range history[lastAssistantIdx].Tools {
			if history[lastAssistantIdx].Tools[i].Output == "" {
				history[lastAssistantIdx].Tools[i].Output = output
				return lastAssistantIdx
			}
		}
		history[lastAssistantIdx].Tools = append(history[lastAssistantIdx].Tools, HistoryTool{
			Name:   "tool",
			Output: output,
		})
		return lastAssistantIdx
	}
	return lastAssistantIdx
}

// Events returns the channel to receive agent events.
func (a *Agent) Events() <-chan Event {
	return a.events
}

// Session returns the current session.
func (a *Agent) Session() *session.Session {
	return a.cfg.Session
}

// SetSession sets a new session.
func (a *Agent) SetSession(s *session.Session) {
	a.cfg.Session = s
	a.messages = nil
}

// NewSession creates a fresh session for the given directory and switches to it.
func (a *Agent) NewSession(dir string) (*session.Session, error) {
	s, err := session.NewSession(a.cfg.Store, dir)
	if err != nil {
		return nil, err
	}
	a.SetSession(s)
	return s, nil
}

// ListSessions returns recent sessions from storage.
func (a *Agent) ListSessions() ([]*storage.SessionRow, error) {
	return a.cfg.Store.ListSessions()
}

// SwitchSession loads an existing session by ID.
func (a *Agent) SwitchSession(id string) (*session.Session, error) {
	row, err := a.cfg.Store.GetSession(id)
	if err != nil {
		return nil, err
	}
	s := &session.Session{
		ID:        row.ID,
		Title:     row.Title,
		Memory:    row.Memory,
		Directory: row.Directory,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
	a.SetSession(s)

	// Restore messages for the switched session
	if msgs, err := session.LoadMessages(a.cfg.Store, s.ID); err == nil && len(msgs) > 0 {
		a.messages = msgs
		a.emitHistory(msgs)
	}

	return s, nil
}

// RenameSession updates the title of the current session.
func (a *Agent) RenameSession(title string) error {
	a.cfg.Session.Title = title
	return a.cfg.Store.UpdateSessionTitle(a.cfg.Session.ID, title)
}

// Run executes a full agent turn with the given user message.
func (a *Agent) Run(ctx context.Context, userMessage string) {
	turnStart := time.Now()

	// Add user message
	userMsg := provider.UserTextMessage(userMessage)
	a.messages = append(a.messages, userMsg)
	session.SaveMessage(a.cfg.Store, a.cfg.Session.ID, "user", userMsg.Content, nil)

	var totalUsage provider.Usage

	// Agent loop: stream → tool calls → execute → repeat
	for {
		if ctx.Err() != nil {
			a.events <- EventError{Err: ctx.Err()}
			return
		}

		if _, err := a.maybeCompact(ctx, "auto", false); err != nil {
			a.events <- EventError{Err: err}
			return
		}

		var toolDefs []provider.ToolDefinition
		if a.cfg.Provider.SupportsTools() {
			toolDefs = a.cfg.Tools.Definitions()
		}

		req := &provider.Request{
			Model:     a.cfg.Model,
			Messages:  a.messages,
			System:    a.systemPrompt(),
			Tools:     toolDefs,
			MaxTokens: a.cfg.MaxTokens,
		}

		// Stream LLM response
		var textBuf strings.Builder
		var toolCalls []toolCall
		var usage provider.Usage

		stream := a.cfg.Provider.Stream(ctx, req)

		for evt := range stream {
			switch e := evt.(type) {
			case provider.EventTextDelta:
				textBuf.WriteString(e.Text)
				a.events <- EventText{Text: e.Text}

			case provider.EventToolUseStart:
				a.events <- EventToolStart{ID: e.ID, Name: e.Name, Detail: ""}

			case provider.EventToolUseDelta:
				// Accumulate (handled by provider)

			case provider.EventToolUseEnd:
				tc := toolCall{ID: e.ID, Name: e.Name, Input: e.Input}
				toolCalls = append(toolCalls, tc)

			case provider.EventMessageComplete:
				usage = e.Usage
				totalUsage.InputTokens += e.Usage.InputTokens
				totalUsage.OutputTokens += e.Usage.OutputTokens

			case provider.EventError:
				a.events <- EventError{Err: e.Err}
				return
			}
		}

		// Build and save assistant message
		assistantContent := buildAssistantContent(textBuf.String(), toolCalls)
		a.messages = append(a.messages, provider.Message{Role: "assistant", Content: assistantContent})
		session.SaveMessage(a.cfg.Store, a.cfg.Session.ID, "assistant", assistantContent, &usage)

		// No tool calls → turn complete
		if len(toolCalls) == 0 {
			a.totalUsage.InputTokens += totalUsage.InputTokens
			a.totalUsage.OutputTokens += totalUsage.OutputTokens
			a.events <- EventTurnDone{
				Usage:      totalUsage,
				TotalUsage: a.totalUsage,
				Duration:   time.Since(turnStart),
			}
			a.autoTitle(userMessage)
			return
		}

		// Execute each tool call
		for _, tc := range toolCalls {
			result := a.executeTool(ctx, tc)

			// Add tool result to messages
			resultMsg := provider.ToolResultMessage(tc.ID, result.Output, result.IsError)
			a.messages = append(a.messages, resultMsg)
			session.SaveMessage(a.cfg.Store, a.cfg.Session.ID, "user", resultMsg.Content, nil)
		}

		// Loop continues — LLM will see tool results
	}
}

type toolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

func (a *Agent) executeTool(ctx context.Context, tc toolCall) *tool.Result {
	t, ok := a.cfg.Tools.Get(tc.Name)
	if !ok {
		result := &tool.Result{Output: fmt.Sprintf("unknown tool: %s", tc.Name), IsError: true}
		a.events <- EventToolEnd{ID: tc.ID, Name: tc.Name, Result: result}
		return result
	}

	// Emit tool detail (command, file path) for TUI display
	detail := extractDetail(tc.Name, tc.Input)
	a.events <- EventToolStart{ID: tc.ID, Name: tc.Name, Detail: detail}
	action, err := a.cfg.Permissions.Check(tc.Name, detail, t.Permission())
	if err != nil {
		result := &tool.Result{Output: fmt.Sprintf("permission error: %v", err), IsError: true}
		a.events <- EventToolEnd{ID: tc.ID, Name: tc.Name, Result: result}
		return result
	}

	if action == permission.Deny {
		result := &tool.Result{Output: "permission denied by user", IsError: true}
		a.events <- EventToolEnd{ID: tc.ID, Name: tc.Name, Result: result}
		return result
	}

	// Execute the tool
	result, err := t.Execute(ctx, tc.Input)
	if err != nil {
		result = &tool.Result{Output: fmt.Sprintf("tool error: %v", err), IsError: true}
	}

	a.events <- EventToolEnd{ID: tc.ID, Name: tc.Name, Result: result}
	return result
}

// SetModel changes the model used for future requests.
func (a *Agent) SetModel(model string) {
	a.cfg.Model = model
}

// DeleteSession removes a session and its messages from storage.
func (a *Agent) DeleteSession(id string) error {
	return a.cfg.Store.DeleteSession(id)
}

// TotalUsage returns cumulative token usage across all turns.
func (a *Agent) TotalUsage() provider.Usage {
	return a.totalUsage
}

func (a *Agent) Compact(ctx context.Context) (*EventCompacted, error) {
	return a.maybeCompact(ctx, "manual", true)
}

func (a *Agent) MemoryBudgetTokens() int {
	return a.memoryBudgetTokens()
}

func (a *Agent) EstimatedTokens() int {
	return a.estimateConversationTokens()
}

func (a *Agent) systemPrompt() string {
	cwd, _ := os.Getwd()
	hostname, _ := os.Hostname()

	var b strings.Builder

	b.WriteString(fmt.Sprintf(`You are Gode, an agentic coding assistant running in the terminal.

You help users with software engineering tasks: writing code, debugging, running commands, exploring codebases, and more.

# Environment
- Working directory: %s
- Platform: %s/%s
- Hostname: %s
`, cwd, runtime.GOOS, runtime.GOARCH, hostname))

	if gc := gitContext(); gc != "" {
		b.WriteString(gc)
	}
	b.WriteByte('\n')

	if strings.TrimSpace(a.cfg.Session.Memory) != "" {
		b.WriteString("# Compacted Memory\n")
		b.WriteString(a.cfg.Session.Memory)
		b.WriteString("\n\n")
	}

	if a.cfg.Provider.SupportsTools() {
		b.WriteString(`# Tool Usage

## bash
- Execute shell commands for system operations, git, package management, tests, builds
- Always quote file paths that contain spaces
- Use absolute paths when possible to avoid ambiguity
- Default timeout is 120s; set "timeout" for long-running operations
- Output is truncated at 100K characters

## read_file
- Always read a file before editing it
- Use "offset" and "limit" for large files to read specific sections
- Returns line-numbered output

## edit_file
- The "old_string" must appear exactly once in the file — include enough surrounding context to make it unique
- Prefer edit_file over write_file for modifications to existing files
- Returns the line number where the edit was applied

## write_file
- Creates the file if it doesn't exist (including parent directories)
- Completely overwrites existing content — use edit_file for partial changes

## glob
- Use glob patterns like "**/*.go" to find files by name
- Results are sorted by modification time (newest first)

## grep
- Search file contents with regex patterns
- Use the "glob" parameter to filter by file type (e.g., "*.ts")
- Skips binary files

# Guidelines
- Be concise and direct — lead with action, not explanation
- Use tools to take action rather than just suggesting what to do
- Read files before editing them to understand existing code
- Prefer editing existing files over creating new ones
- Use early returns and clean code patterns
- Ask for clarification when requirements are ambiguous
- Don't add features or refactoring beyond what was asked
- When running commands that may fail, check the output and adapt`)
	} else {
		b.WriteString(`# Execution Limits

- This backend does not support tool calling or shell execution
- Do not claim that you ran commands or edited files
- You can still inspect the prior transcript and provide direct code or debugging guidance

# Guidelines
- Be concise and direct — lead with action, not explanation
- Work from the conversation context that is already present
- If the user asks for changes, provide the concrete patch or code you would apply
- Be explicit about uncertainty because you cannot inspect files or run tools with this backend`)
	}

	// Load project instructions (GODE.md files)
	instructions := config.LoadInstructions(cwd, 20480)
	if instructions != "" {
		b.WriteString("\n\n# Project Instructions\n\n")
		b.WriteString(instructions)
	}

	return b.String()
}

// autoTitle sets the session title from the first user message if not already titled.
func (a *Agent) autoTitle(userMessage string) {
	if strings.TrimSpace(a.cfg.Session.Title) != "" {
		return
	}
	title := strings.ReplaceAll(strings.TrimSpace(userMessage), "\n", " ")
	if len(title) > 60 {
		title = title[:57] + "..."
	}
	a.cfg.Session.Title = title
	if err := a.cfg.Store.UpdateSessionTitle(a.cfg.Session.ID, title); err != nil {
		a.events <- EventError{Err: fmt.Errorf("updating session title: %w", err)}
	}
}

// gitContext returns git branch and status info, or empty string if not in a git repo.
func gitContext() string {
	branch, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("- Git branch: %s\n", strings.TrimSpace(string(branch))))

	status, err := exec.Command("git", "status", "--porcelain").Output()
	if err == nil {
		trimmed := strings.TrimSpace(string(status))
		if trimmed == "" {
			b.WriteString("- Git status: clean\n")
		} else {
			lines := strings.Split(trimmed, "\n")
			b.WriteString(fmt.Sprintf("- Git status: %d changed files\n", len(lines)))
		}
	}

	return b.String()
}

func buildAssistantContent(text string, toolCalls []toolCall) []interface{} {
	var content []interface{}
	if text != "" {
		content = append(content, provider.TextBlock{Type: "text", Text: text})
	}
	for _, tc := range toolCalls {
		content = append(content, provider.ToolUseBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Name,
			Input: tc.Input,
		})
	}
	if len(content) == 0 {
		content = append(content, provider.TextBlock{Type: "text", Text: ""})
	}
	return content
}

func extractDetail(toolName string, input json.RawMessage) string {
	var m map[string]interface{}
	if err := json.Unmarshal(input, &m); err != nil {
		return ""
	}

	switch toolName {
	case "bash":
		if cmd, ok := m["command"].(string); ok {
			return cmd
		}
	case "read_file", "write_file", "edit_file":
		if fp, ok := m["file_path"].(string); ok {
			return fp
		}
	case "glob":
		if p, ok := m["pattern"].(string); ok {
			return p
		}
	case "grep":
		if p, ok := m["pattern"].(string); ok {
			return p
		}
	}
	return ""
}
