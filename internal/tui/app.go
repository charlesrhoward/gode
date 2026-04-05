package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wrap"
	"github.com/tradecraft/gode/internal/agent"
	"github.com/tradecraft/gode/internal/permission"
	"github.com/tradecraft/gode/internal/session"
)

type appState int

const (
	stateReady appState = iota
	stateStreaming
	statePermission
)

// agentEventMsg wraps agent events for bubbletea.
type agentEventMsg struct{ event agent.Event }
type compactionDoneMsg struct {
	result *agent.EventCompacted
	err    error
}

// App is the main TUI application.
type App struct {
	agent    *agent.Agent
	session  *session.Session
	perms    *permission.Manager
	version  string
	provider string
	model    string
	runtime  []string
}

func New(ag *agent.Agent, sess *session.Session, version, providerName, modelName string, runtimeInfo []string) *App {
	return &App{
		agent:    ag,
		session:  sess,
		version:  version,
		provider: providerName,
		model:    modelName,
		runtime:  append([]string(nil), runtimeInfo...),
	}
}

func (a *App) SetPermissions(perms *permission.Manager) {
	a.perms = perms
}

func (a *App) Run(ctx context.Context) error {
	m := newModel(a.agent, a.session, a.perms, a.version, a.provider, a.model, a.runtime)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	// Set up permission asking — blocks agent goroutine, sends event to TUI
	if a.perms != nil {
		a.perms.SetAskFunc(func(toolName, detail string) (permission.Action, error) {
			resultCh := make(chan permission.Action, 1)
			p.Send(agentEventMsg{event: agent.EventPermission{
				Tool:   toolName,
				Detail: detail,
				Result: resultCh,
			}})
			action := <-resultCh // blocks until user responds in TUI
			return action, nil
		})
	}

	// Forward agent events to bubbletea program
	go func() {
		for evt := range a.agent.Events() {
			p.Send(agentEventMsg{event: evt})
		}
	}()

	_, err := p.Run()
	return err
}

type model struct {
	agent     *agent.Agent
	session   *session.Session
	perms     *permission.Manager
	version   string
	provider  string
	modelName string
	runtime   []string

	state    appState
	input    textarea.Model
	viewport viewport.Model
	spinner  spinner.Model
	mdRender *glamour.TermRenderer

	messages     []messageView
	streamingBuf *strings.Builder
	toolViews    map[string]*toolView

	permDialog *permDialogState

	runCancel context.CancelFunc // cancels the current agent run

	width  int
	height int

	totalUsage agent.EventTurnDone
}

type messageView struct {
	role    string
	content string
	tools   []*toolView
}

type toolView struct {
	id     string
	name   string
	status string // "running", "done", "error"
	output string
}

type permDialogState struct {
	tool   string
	detail string
	result chan permission.Action
}

func newModel(ag *agent.Agent, sess *session.Session, perms *permission.Manager, version, providerName, modelName string, runtimeInfo []string) *model {
	ta := textarea.New()
	ta.Placeholder = "Ask Gode to inspect, edit, or explain this project..."
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetHeight(1)
	ta.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorPrimary)

	vp := viewport.New(80, 20)

	mdR, _ := newMarkdownRenderer(80)

	return &model{
		agent:        ag,
		session:      sess,
		perms:        perms,
		version:      version,
		provider:     providerName,
		modelName:    modelName,
		runtime:      append([]string(nil), runtimeInfo...),
		state:        stateReady,
		input:        ta,
		viewport:     vp,
		spinner:      sp,
		mdRender:     mdR,
		streamingBuf: &strings.Builder{},
		toolViews:    make(map[string]*toolView),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.spinner.Tick,
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.SetWidth(maxInt(msg.Width-6, 20))
		// Recreate markdown renderer with new width
		if r, err := newMarkdownRenderer(maxInt(msg.Width-6, 20)); err == nil {
			m.mdRender = r
		}
		m.recalcViewport()
		m.viewport.SetContent(m.renderMessages())
		return m, nil

	case agentEventMsg:
		return m.handleAgentEvent(msg.event)

	case compactionDoneMsg:
		m.state = stateReady
		m.runCancel = nil
		m.input.Focus()
		m.recalcViewport()
		if msg.err != nil {
			m.messages = append(m.messages, messageView{
				role:    "system",
				content: fmt.Sprintf("compaction error: %v", msg.err),
			})
		} else if msg.result == nil {
			m.messages = append(m.messages, messageView{
				role:    "system",
				content: "compaction skipped — transcript is already within the memory budget",
			})
		}
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, textarea.Blink

	case spinner.TickMsg:
		if m.state == stateStreaming {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	// Update textarea
	if m.state == stateReady {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Scrollback navigation — when input is empty and we're in ready state
	if m.state == stateReady && strings.TrimSpace(m.input.Value()) == "" {
		switch msg.Type {
		case tea.KeyUp:
			m.viewport.LineUp(1)
			return m, nil
		case tea.KeyDown:
			m.viewport.LineDown(1)
			return m, nil
		case tea.KeyPgUp:
			m.viewport.HalfViewUp()
			return m, nil
		case tea.KeyPgDown:
			m.viewport.HalfViewDown()
			return m, nil
		case tea.KeyRunes:
			switch string(msg.Runes) {
			case "k":
				m.viewport.LineUp(1)
				return m, nil
			case "j":
				m.viewport.LineDown(1)
				return m, nil
			case "G":
				m.viewport.GotoBottom()
				return m, nil
			case "g":
				m.viewport.GotoTop()
				return m, nil
			}
		}
		// Ctrl+U / Ctrl+D for half-page scroll
		if msg.Type == tea.KeyCtrlU {
			m.viewport.HalfViewUp()
			return m, nil
		}
		if msg.Type == tea.KeyCtrlD {
			m.viewport.HalfViewDown()
			return m, nil
		}
	}

	switch msg.Type {
	case tea.KeyCtrlC:
		if m.runCancel != nil {
			m.runCancel()
		}
		return m, tea.Quit

	case tea.KeyEsc:
		if m.state == statePermission && m.permDialog != nil {
			m.permDialog.result <- permission.Deny
			m.permDialog = nil
			m.state = stateStreaming
			m.recalcViewport()
			m.viewport.SetContent(m.renderMessages())
			return m, m.spinner.Tick
		}
		if m.state == stateStreaming && m.runCancel != nil {
			m.runCancel()
			return m, nil
		}

	case tea.KeyEnter:
		if m.state == stateReady {
			// Alt+Enter or Ctrl+J: insert newline
			if msg.Alt {
				m.input.InsertString("\n")
				m.resizeInput()
				return m, nil
			}
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			if strings.HasPrefix(text, "/") {
				return m.handleCommand(text)
			}
			return m.submitMessage(text)
		}

	case tea.KeyRunes:
		if m.state == statePermission && m.permDialog != nil {
			switch string(msg.Runes) {
			case "y":
				m.permDialog.result <- permission.Allow
				m.permDialog = nil
				m.state = stateStreaming
				m.recalcViewport()
				m.viewport.SetContent(m.renderMessages())
				return m, m.spinner.Tick
			case "n":
				m.permDialog.result <- permission.Deny
				m.permDialog = nil
				m.state = stateStreaming
				m.recalcViewport()
				m.viewport.SetContent(m.renderMessages())
				return m, m.spinner.Tick
			case "a":
				if m.perms != nil {
					m.perms.AcceptAll()
				}
				m.permDialog.result <- permission.Allow
				m.permDialog = nil
				m.state = stateStreaming
				m.recalcViewport()
				m.viewport.SetContent(m.renderMessages())
				return m, m.spinner.Tick
			}
			return m, nil
		}
	}

	if m.state == stateReady {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.resizeInput()
		return m, cmd
	}

	return m, nil
}

// resizeInput adjusts the textarea height based on content (max 5 lines).
func (m *model) resizeInput() {
	lines := strings.Count(m.input.Value(), "\n") + 1
	if lines > 5 {
		lines = 5
	}
	if lines < 1 {
		lines = 1
	}
	m.input.SetHeight(lines)
	m.recalcViewport()
}

// recalcViewport adjusts viewport dimensions based on current terminal size and input height.
func (m *model) recalcViewport() {
	headerH := 2
	inputH := m.inputAreaHeight()
	m.viewport.Width = maxInt(m.width-2, 1)
	vpHeight := m.height - headerH - inputH
	if vpHeight < 1 {
		vpHeight = 1
	}
	m.viewport.Height = vpHeight
}

func (m *model) inputAreaHeight() int {
	switch m.state {
	case statePermission:
		return lipgloss.Height(m.renderPermissionDialog())
	case stateStreaming:
		return 2
	default:
		panelWidth := maxInt(m.width-2, 24)
		panel := inputBorderStyle.Width(panelWidth).Render(m.input.View())
		hints := mutedStyle.Render("Enter send · Alt+Enter newline · Esc cancel run · /help commands")
		return lipgloss.Height(lipgloss.JoinVertical(lipgloss.Left, inputLabelStyle.Render("Prompt"), panel, hints))
	}
}

func (m *model) submitMessage(text string) (tea.Model, tea.Cmd) {
	// Cancel any in-flight agent run
	if m.runCancel != nil {
		m.runCancel()
	}

	m.input.Reset()
	m.input.SetHeight(1)
	m.state = stateStreaming
	m.streamingBuf.Reset()
	m.toolViews = make(map[string]*toolView)
	m.recalcViewport()

	m.messages = append(m.messages, messageView{
		role:    "user",
		content: text,
	})

	// Start assistant message placeholder
	m.messages = append(m.messages, messageView{
		role: "assistant",
	})

	m.viewport.SetContent(m.renderMessages())
	m.viewport.GotoBottom()

	ctx, cancel := context.WithCancel(context.Background())
	m.runCancel = cancel

	userText := text
	return m, tea.Batch(
		m.spinner.Tick,
		func() tea.Msg {
			m.agent.Run(ctx, userText)
			return nil
		},
	)
}

func (m *model) handleCommand(cmd string) (tea.Model, tea.Cmd) {
	m.input.Reset()
	m.input.SetHeight(1)
	m.recalcViewport()

	parts := strings.Fields(cmd)

	switch {
	case cmd == "/quit" || cmd == "/q":
		return m, tea.Quit
	case cmd == "/clear":
		m.messages = nil
		m.viewport.SetContent(m.renderMessages())
		return m, nil
	case cmd == "/help":
		help := `Commands:
  /help                show commands and shortcuts
  /status              show provider, model, and session details
  /compact             compact old transcript history into session memory
  /clear               clear the current transcript view
  /new                 start a fresh session in this directory
  /sessions            list recent sessions
  /switch <id>         switch to a session by ID prefix
  /rename <name>       rename the current session
  /quit, /q            exit

Shortcuts:
  Enter                send prompt
  Alt+Enter            insert newline
  Esc                  cancel current run
  j/k or arrows        scroll history
  Ctrl+U / Ctrl+D      half-page scroll`
		m.messages = append(m.messages, messageView{role: "system", content: help})
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil
	case cmd == "/status":
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("provider: %s\nmodel: %s\nsession: %s\ndirectory: %s\nmemory budget: ~%d tokens\nestimated transcript: ~%d tokens\ncompacted memory: %s",
			m.provider,
			m.modelName,
			m.sessionLabel(),
			m.session.Directory,
			m.agent.MemoryBudgetTokens(),
			m.agent.EstimatedTokens(),
			yesNo(strings.TrimSpace(m.session.Memory) != ""),
		))
		if len(m.runtime) > 0 {
			sb.WriteString("\n\nruntime:\n")
			for _, line := range m.runtime {
				sb.WriteString("  " + line + "\n")
			}
		}
		return m.showSystemMsg(strings.TrimSpace(sb.String()))
	case cmd == "/compact":
		return m.startCompaction()
	case cmd == "/new":
		newSess, err := m.agent.NewSession(m.session.Directory)
		if err != nil {
			return m.showSystemMsg(fmt.Sprintf("error creating session: %v", err))
		}
		m.session = newSess
		m.messages = nil
		m.viewport.SetContent(m.renderMessages())
		return m, nil

	case cmd == "/sessions":
		sessions, err := m.agent.ListSessions()
		if err != nil {
			return m.showSystemMsg(fmt.Sprintf("error listing sessions: %v", err))
		}
		if len(sessions) == 0 {
			return m.showSystemMsg("no sessions found")
		}
		var sb strings.Builder
		sb.WriteString("Recent sessions:\n")
		for _, s := range sessions {
			id := s.ID
			if len(id) > 8 {
				id = id[:8]
			}
			title := s.Title
			if title == "" {
				title = s.Directory
			}
			age := time.Since(s.UpdatedAt).Truncate(time.Minute)
			current := ""
			if s.ID == m.session.ID {
				current = " ◀"
			}
			sb.WriteString(fmt.Sprintf("  %s  %s  (%v ago)%s\n", id, title, age, current))
		}
		return m.showSystemMsg(sb.String())

	case parts[0] == "/switch":
		if len(parts) < 2 {
			return m.showSystemMsg("usage: /switch <session-id>")
		}
		prefix := parts[1]
		sessions, err := m.agent.ListSessions()
		if err != nil {
			return m.showSystemMsg(fmt.Sprintf("error: %v", err))
		}
		// Find session by ID prefix
		var matchID string
		for _, s := range sessions {
			if strings.HasPrefix(s.ID, prefix) {
				matchID = s.ID
				break
			}
		}
		if matchID == "" {
			return m.showSystemMsg(fmt.Sprintf("no session found matching '%s'", prefix))
		}
		m.messages = nil
		newSess, err := m.agent.SwitchSession(matchID)
		if err != nil {
			return m.showSystemMsg(fmt.Sprintf("error switching: %v", err))
		}
		m.session = newSess
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil

	case parts[0] == "/rename":
		if len(parts) < 2 {
			return m.showSystemMsg("usage: /rename <name>")
		}
		name := strings.Join(parts[1:], " ")
		if err := m.agent.RenameSession(name); err != nil {
			return m.showSystemMsg(fmt.Sprintf("error: %v", err))
		}
		m.session.Title = name
		return m.showSystemMsg(fmt.Sprintf("session renamed to '%s'", name))

	default:
		m.messages = append(m.messages, messageView{
			role:    "system",
			content: fmt.Sprintf("unknown command: %s — type /help for commands", cmd),
		})
		m.viewport.SetContent(m.renderMessages())
		return m, nil
	}
}

// showSystemMsg appends a system message and refreshes the viewport.
func (m *model) showSystemMsg(text string) (tea.Model, tea.Cmd) {
	m.messages = append(m.messages, messageView{role: "system", content: text})
	m.viewport.SetContent(m.renderMessages())
	m.viewport.GotoBottom()
	return m, nil
}

func (m *model) handleAgentEvent(evt agent.Event) (tea.Model, tea.Cmd) {
	switch e := evt.(type) {
	case agent.EventText:
		m.streamingBuf.WriteString(e.Text)
		if last := m.lastAssistant(); last != nil {
			last.content = m.streamingBuf.String()
		}
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil

	case agent.EventToolStart:
		tv := &toolView{id: e.ID, name: e.Name, status: "running"}
		m.toolViews[e.ID] = tv
		if last := m.lastAssistant(); last != nil {
			last.tools = append(last.tools, tv)
		}
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, m.spinner.Tick

	case agent.EventToolEnd:
		if tv, ok := m.toolViews[e.ID]; ok {
			if e.Result.IsError {
				tv.status = "error"
			} else {
				tv.status = "done"
			}
			tv.output = truncate(e.Result.Output, 500)
		}
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil

	case agent.EventTurnDone:
		m.state = stateReady
		m.totalUsage = e
		m.streamingBuf.Reset()
		m.runCancel = nil
		m.input.Focus()
		m.recalcViewport()
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, textarea.Blink

	case agent.EventPermission:
		m.state = statePermission
		m.permDialog = &permDialogState{
			tool:   e.Tool,
			detail: e.Detail,
			result: e.Result,
		}
		m.recalcViewport()
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil

	case agent.EventCompacted:
		m.session.Memory = strings.TrimSpace(m.agent.Session().Memory)
		m.messages = append(m.messages, messageView{
			role: "system",
			content: fmt.Sprintf("compacted history (%s): ~%d -> ~%d tokens, kept %d messages, folded %d into memory",
				e.Trigger, e.BeforeTokens, e.AfterTokens, e.RetainedMessages, e.CompactedMessages),
		})
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil

	case agent.EventHistory:
		// Replay prior conversation into the message view
		for _, hm := range e.Messages {
			switch hm.Role {
			case "user":
				m.messages = append(m.messages, messageView{
					role:    "user",
					content: hm.Content,
				})
			case "assistant":
				mv := messageView{role: "assistant", content: hm.Content}
				for _, ht := range hm.Tools {
					mv.tools = append(mv.tools, &toolView{
						name:   ht.Name,
						status: "done",
						output: truncate(ht.Output, 200),
					})
				}
				m.messages = append(m.messages, mv)
			}
		}
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil

	case agent.EventError:
		m.state = stateReady
		m.runCancel = nil
		m.recalcViewport()
		if errors.Is(e.Err, context.Canceled) {
			if last := m.lastAssistant(); last != nil && strings.TrimSpace(last.content) == "" && len(last.tools) == 0 {
				m.messages = m.messages[:len(m.messages)-1]
			}
			m.input.Focus()
			m.viewport.SetContent(m.renderMessages())
			m.viewport.GotoBottom()
			return m, textarea.Blink
		}
		if last := m.lastAssistant(); last != nil {
			if last.content != "" {
				last.content += "\n"
			}
			last.content += errorStyle.Render(fmt.Sprintf("Error: %v", e.Err))
		}
		m.input.Focus()
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, textarea.Blink
	}

	return m, nil
}

func (m *model) lastAssistant() *messageView {
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].role == "assistant" {
			return &m.messages[i]
		}
	}
	return nil
}

func (m model) View() string {
	if m.width == 0 {
		return "loading..."
	}
	header := m.renderHeader()
	content := m.viewport.View()
	input := m.renderInput()
	return lipgloss.JoinVertical(lipgloss.Left, header, content, input)
}

func (m *model) renderHeader() string {
	title := headerStyle.Render(fmt.Sprintf("gode v%s", m.version))
	sessionLabel := sessionStyle.Render(m.sessionLabel())
	runtimeLabel := mutedStyle.Render(fmt.Sprintf("%s · %s", m.provider, m.modelName))
	dirLabel := mutedStyle.Render(shortenMiddle(m.session.Directory, maxInt(m.width/2, 24)))

	rightTop := mutedStyle.Render(m.stateLabel())
	if m.totalUsage.Usage.InputTokens > 0 {
		rightTop = mutedStyle.Render(fmt.Sprintf("%s · %d↓ %d↑",
			m.stateLabel(), m.totalUsage.Usage.InputTokens, m.totalUsage.Usage.OutputTokens))
	}

	line1 := justifyLine(m.width, lipgloss.JoinHorizontal(lipgloss.Top, title, " ", sessionLabel), rightTop)
	line2 := justifyLine(m.width, runtimeLabel, dirLabel)
	return line1 + "\n" + line2
}

func (m *model) renderInput() string {
	if m.state == statePermission && m.permDialog != nil {
		return m.renderPermissionDialog()
	}

	if m.state == stateStreaming {
		return lipgloss.JoinVertical(
			lipgloss.Left,
			m.spinner.View()+mutedStyle.Render(" running local model..."),
			mutedStyle.Render("Esc cancels the current run"),
		)
	}

	panelWidth := maxInt(m.width-2, 24)
	panel := inputBorderStyle.Width(panelWidth).Render(m.input.View())
	hints := mutedStyle.Render("Enter send · Alt+Enter newline · Esc cancel run · /help commands")
	return lipgloss.JoinVertical(lipgloss.Left, inputLabelStyle.Render("Prompt"), panel, hints)
}

func (m *model) renderPermissionDialog() string {
	label := permLabelStyle.Render("⚠ Permission required")
	tool := toolNameStyle.Render(m.permDialog.tool)
	detail := m.permDialog.detail
	if len(detail) > 80 {
		detail = detail[:80] + "..."
	}

	content := fmt.Sprintf("%s\nTool: %s\nDetail: %s\n\n%s",
		label, tool, detail,
		mutedStyle.Render("[y]es  [n]o  [a]llow all"))

	w := m.width - 4
	if w < 40 {
		w = 40
	}
	return permBorderStyle.Width(w).Render(content)
}

func (m *model) renderMessages() string {
	if len(m.messages) == 0 {
		return m.renderEmptyState()
	}

	var b strings.Builder

	lastAssistantIdx := -1
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].role == "assistant" {
			lastAssistantIdx = i
			break
		}
	}

	for i, msg := range m.messages {
		switch msg.role {
		case "user":
			b.WriteString(userLabelStyle.Render("You") + "\n")
			b.WriteString(m.wrapPlainText(msg.content) + "\n\n")

		case "assistant":
			b.WriteString(assistantLabelStyle.Render("Gode") + "\n")
			if msg.content != "" {
				// Render markdown for completed messages, plain text while streaming
				isStreaming := m.state == stateStreaming && i == lastAssistantIdx
				if !isStreaming {
					b.WriteString(m.renderMarkdown(msg.content))
				} else {
					b.WriteString(m.wrapPlainText(msg.content) + "\n")
				}
			}
			for _, tv := range msg.tools {
				b.WriteString(m.renderToolCard(tv) + "\n")
			}
			b.WriteByte('\n')

		case "system":
			b.WriteString(mutedStyle.Render(m.wrapPlainText(msg.content)) + "\n\n")
		}
	}

	return b.String()
}

func (m *model) renderEmptyState() string {
	var b strings.Builder
	b.WriteString(emptyTitleStyle.Render("Local coding session ready") + "\n")
	b.WriteString(mutedStyle.Render("Backend") + ": " + mutedStyle.Render(fmt.Sprintf("%s · %s", m.provider, m.modelName)) + "\n\n")
	if len(m.runtime) > 0 {
		b.WriteString(systemHintStyle.Render("Runtime") + "\n")
		for _, line := range m.runtime {
			b.WriteString("  - " + line + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(systemHintStyle.Render("Try") + "\n")
	b.WriteString("  - summarize this repository and point out obvious cleanup work\n")
	b.WriteString("  - explain the event loop in the current TUI\n")
	b.WriteString("  - draft the patch I should apply for a specific file\n\n")
	b.WriteString(systemHintStyle.Render("Shortcuts") + "\n")
	b.WriteString("  - Enter send\n")
	b.WriteString("  - Alt+Enter newline\n")
	b.WriteString("  - Esc cancel run\n")
	b.WriteString("  - /help command list")
	return emptyStateStyle.Width(maxInt(m.width-4, 36)).Render(b.String())
}

func newMarkdownRenderer(width int) (*glamour.TermRenderer, error) {
	return glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
}

// renderMarkdown renders content through glamour, falling back to plain text.
func (m *model) renderMarkdown(content string) string {
	if m.mdRender == nil {
		return m.wrapPlainText(content) + "\n"
	}
	rendered, err := m.mdRender.Render(content)
	if err != nil {
		return m.wrapPlainText(content) + "\n"
	}
	return rendered
}

func (m *model) wrapPlainText(content string) string {
	return wrap.String(content, m.transcriptWidth())
}

func (m *model) transcriptWidth() int {
	if m.viewport.Width > 0 {
		return maxInt(m.viewport.Width, 20)
	}
	return maxInt(m.width-2, 20)
}

func (m *model) renderToolCard(tv *toolView) string {
	var statusIcon string
	var statusStyle lipgloss.Style

	switch tv.status {
	case "running":
		statusIcon = m.spinner.View()
		statusStyle = toolStatusRunning
	case "done":
		statusIcon = "✓"
		statusStyle = toolStatusDone
	case "error":
		statusIcon = "✗"
		statusStyle = toolStatusError
	}

	header := toolNameStyle.Render(tv.name) + " " + statusStyle.Render(statusIcon)
	content := header
	if tv.output != "" {
		output := tv.output
		if len(output) > 200 {
			output = output[:200] + "..."
		}
		content += "\n" + mutedStyle.Render(output)
	}

	w := m.width - 6
	if w < 30 {
		w = 30
	}
	return toolBorderStyle.Width(w).Render(content)
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

func (m *model) startCompaction() (tea.Model, tea.Cmd) {
	if m.runCancel != nil {
		m.runCancel()
	}
	m.state = stateStreaming
	m.recalcViewport()
	m.viewport.SetContent(m.renderMessages())

	ctx, cancel := context.WithCancel(context.Background())
	m.runCancel = cancel

	return m, tea.Batch(
		m.spinner.Tick,
		func() tea.Msg {
			result, err := m.agent.Compact(ctx)
			return compactionDoneMsg{result: result, err: err}
		},
	)
}

func (m *model) sessionLabel() string {
	if strings.TrimSpace(m.session.Title) != "" {
		return m.session.Title
	}
	base := filepath.Base(m.session.Directory)
	if base == "." || base == "" {
		return m.session.Directory
	}
	return base
}

func (m *model) stateLabel() string {
	switch m.state {
	case stateStreaming:
		return "running"
	case statePermission:
		return "permission"
	default:
		return "ready"
	}
}

func justifyLine(width int, left, right string) string {
	if width <= 0 {
		return left + " " + right
	}
	usedWidth := lipgloss.Width(left) + lipgloss.Width(right) + 1
	if usedWidth >= width {
		return left + " " + right
	}
	return left + strings.Repeat(" ", width-usedWidth) + right
}

func shortenMiddle(s string, max int) string {
	if max <= 0 || lipgloss.Width(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	head := (max - 1) / 2
	tail := max - head - 1
	return s[:head] + "…" + s[len(s)-tail:]
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
