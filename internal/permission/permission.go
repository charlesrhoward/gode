package permission

import (
	"path/filepath"
	"sync"
)

type Level int

const (
	ReadOnly Level = iota
	WorkspaceWrite
	FullAccess
)

func (l Level) String() string {
	switch l {
	case ReadOnly:
		return "read-only"
	case WorkspaceWrite:
		return "workspace-write"
	case FullAccess:
		return "full-access"
	default:
		return "unknown"
	}
}

type Action string

const (
	Allow Action = "allow"
	Deny  Action = "deny"
	Ask   Action = "ask"
)

type Rule struct {
	Tool    string `json:"tool"`
	Pattern string `json:"pattern"`
	Action  Action `json:"action"`
}

type Ruleset []Rule

type AskFunc func(toolName string, detail string) (Action, error)

type Manager struct {
	rules        Ruleset
	sessionRules Ruleset
	askFn        AskFunc
	mu           sync.Mutex
}

func NewManager(rules Ruleset) *Manager {
	return &Manager{rules: rules}
}

func (m *Manager) SetAskFunc(fn AskFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.askFn = fn
}

func (m *Manager) Check(toolName, detail string, level Level) (Action, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check session-scoped rules first
	for _, r := range m.sessionRules {
		if m.matches(r, toolName, detail) {
			return r.Action, nil
		}
	}

	// Check configured rules
	for _, r := range m.rules {
		if m.matches(r, toolName, detail) {
			return r.Action, nil
		}
	}

	// Read-only tools always allowed
	if level == ReadOnly {
		return Allow, nil
	}

	// Ask user
	if m.askFn != nil {
		return m.askFn(toolName, detail)
	}

	return Deny, nil
}

func (m *Manager) AcceptSession(toolName, pattern string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionRules = append(m.sessionRules, Rule{
		Tool:    toolName,
		Pattern: pattern,
		Action:  Allow,
	})
}

func (m *Manager) AcceptAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionRules = append(m.sessionRules, Rule{
		Tool:   "*",
		Action: Allow,
	})
}

func (m *Manager) matches(r Rule, toolName, detail string) bool {
	if r.Tool != "*" && r.Tool != toolName {
		return false
	}
	if r.Pattern == "" {
		return true
	}
	matched, _ := filepath.Match(r.Pattern, detail)
	return matched
}
