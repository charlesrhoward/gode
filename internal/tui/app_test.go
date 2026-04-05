package tui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/tradecraft/gode/internal/session"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestRenderMessagesWrapsPlainText(t *testing.T) {
	m := newModel(nil, &session.Session{Directory: "/tmp/project"}, nil, "0.1.0", "mlx_vlm", "test-model", nil)
	m.width = 32
	m.height = 20
	m.recalcViewport()
	m.state = stateStreaming
	m.messages = []messageView{
		{role: "user", content: strings.Repeat("wrapme", 8)},
		{role: "assistant", content: strings.Repeat("stream", 8)},
		{role: "system", content: strings.Repeat("system", 8)},
	}

	rendered := ansiRE.ReplaceAllString(m.renderMessages(), "")
	for _, line := range strings.Split(rendered, "\n") {
		if lipgloss.Width(line) > m.viewport.Width {
			t.Fatalf("line exceeded viewport width (%d > %d): %q", lipgloss.Width(line), m.viewport.Width, line)
		}
	}
}
