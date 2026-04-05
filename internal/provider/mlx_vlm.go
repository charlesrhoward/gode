package provider

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

const defaultMLXPythonPath = "/usr/local/bin/python3"

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
var mlxTurnBoundaryMarkers = []string{"\nUser:", "\nSystem:", "\nAssistant:"}

type MLXVLMConfig struct {
	PythonPath string
}

type MLXVLM struct {
	pythonPath string
}

type MLXVLMRuntimeInfo struct {
	PythonPath  string
	BackendPath string
}

func NewMLXVLM(cfg MLXVLMConfig) (*MLXVLM, error) {
	pythonPath := strings.TrimSpace(cfg.PythonPath)
	if pythonPath == "" {
		if _, err := os.Stat(defaultMLXPythonPath); err == nil {
			pythonPath = defaultMLXPythonPath
		} else {
			pythonPath = "python3"
		}
	}
	return &MLXVLM{pythonPath: pythonPath}, nil
}

func (m *MLXVLM) ID() string          { return "mlx_vlm" }
func (m *MLXVLM) SupportsTools() bool { return false }

func (m *MLXVLM) RuntimeInfo(ctx context.Context) (MLXVLMRuntimeInfo, error) {
	info := MLXVLMRuntimeInfo{PythonPath: m.pythonPath}

	cmd := exec.CommandContext(ctx, m.pythonPath, "-c", `import inspect, mlx_vlm.generate; print(inspect.getsourcefile(mlx_vlm.generate) or "")`)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errText := strings.TrimSpace(stripANSI(stderr.String()))
		if errText == "" {
			errText = err.Error()
		}
		return info, fmt.Errorf("mlx_vlm import failed: %s", errText)
	}

	info.BackendPath = strings.TrimSpace(stdout.String())
	if info.BackendPath == "" {
		return info, fmt.Errorf("mlx_vlm import succeeded but did not report a module path")
	}

	return info, nil
}

func (m *MLXVLM) Stream(ctx context.Context, req *Request) <-chan StreamEvent {
	ch := make(chan StreamEvent, 32)

	go func() {
		defer close(ch)

		args := buildMLXVLMArgs(req)

		cmd := exec.CommandContext(ctx, m.pythonPath, args...)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			ch <- EventError{Err: fmt.Errorf("creating stdout pipe: %w", err)}
			return
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			ch <- EventError{Err: fmt.Errorf("creating stderr pipe: %w", err)}
			return
		}

		var stderrBuf bytes.Buffer
		if err := cmd.Start(); err != nil {
			ch <- EventError{Err: fmt.Errorf("starting MLX VLM command with %s: %w", m.pythonPath, err)}
			return
		}

		doneStderr := make(chan struct{})
		go func() {
			defer close(doneStderr)
			_, _ = io.Copy(&stderrBuf, stderr)
		}()

		reader := bufio.NewReader(stdout)
		filter := newMLXOutputFilter()
		for {
			r, _, err := reader.ReadRune()
			if err != nil {
				if err == io.EOF {
					break
				}
				if ctx.Err() != nil {
					ch <- EventError{Err: ctx.Err()}
					return
				}
				ch <- EventError{Err: fmt.Errorf("reading MLX VLM output: %w", err)}
				return
			}
			if text := filter.PushRune(r); text != "" {
				ch <- EventTextDelta{Text: text}
			}
		}

		if text := filter.Flush(); text != "" {
			ch <- EventTextDelta{Text: text}
		}

		<-doneStderr

		if err := cmd.Wait(); err != nil {
			if ctx.Err() != nil {
				ch <- EventError{Err: ctx.Err()}
				return
			}
			stderrText := strings.TrimSpace(stripANSI(stderrBuf.String()))
			if stderrText == "" {
				stderrText = err.Error()
			}
			ch <- EventError{Err: fmt.Errorf("MLX VLM command failed: %s", stderrText)}
			return
		}

		ch <- EventMessageComplete{
			StopReason: "end_turn",
			Usage:      Usage{},
		}
	}()

	return ch
}

func buildMLXVLMArgs(req *Request) []string {
	args := []string{
		"-m", "mlx_vlm", "generate",
		"--model", req.Model,
		"--max-tokens", strconv.Itoa(req.MaxTokens),
		"--temperature", "0.0",
		// mlx_vlm's CLI has inverted semantics here: passing --verbose disables
		// the default debug dump so stdout contains only generated text.
		"--verbose",
		"--prompt", buildMLXVLMPrompt(req.Messages),
	}
	if req.System != "" {
		args = append(args, "--system", req.System)
	}
	return args
}

func buildMLXVLMPrompt(messages []Message) string {
	var b strings.Builder
	b.WriteString("Continue this conversation as the assistant. Give only the next assistant reply.\n\n")

	for _, msg := range messages {
		role := "User"
		switch msg.Role {
		case "assistant":
			role = "Assistant"
		case "system":
			role = "System"
		}

		parts := splitContentBlocks(msg.Content)
		b.WriteString(role + ":\n")
		if text := strings.TrimSpace(parts.text); text != "" {
			b.WriteString(text + "\n")
		}
		for _, call := range parts.toolUses {
			b.WriteString(fmt.Sprintf("[tool requested: %s %s]\n", call.Name, strings.TrimSpace(string(call.Input))))
		}
		for _, result := range parts.toolResults {
			line := strings.TrimSpace(result.Content)
			if line == "" {
				line = "<empty>"
			}
			b.WriteString(fmt.Sprintf("[tool result %s]\n%s\n", result.ToolUseID, line))
		}
		b.WriteString("\n")
	}

	b.WriteString("Assistant:\n")
	return b.String()
}

func stripANSI(s string) string {
	return ansiEscapePattern.ReplaceAllString(s, "")
}

type mlxOutputFilter struct {
	pending []rune
	stopped bool
}

func newMLXOutputFilter() *mlxOutputFilter {
	return &mlxOutputFilter{}
}

func (f *mlxOutputFilter) PushRune(r rune) string {
	if f.stopped {
		return ""
	}

	f.pending = append(f.pending, r)
	if idx := findTurnBoundary(f.pending); idx >= 0 {
		f.stopped = true
		out := string(f.pending[:idx])
		f.pending = nil
		return out
	}

	maxHold := maxTurnBoundaryRunes()
	if len(f.pending) <= maxHold {
		return ""
	}

	flushCount := len(f.pending) - maxHold
	out := string(f.pending[:flushCount])
	f.pending = f.pending[flushCount:]
	return out
}

func (f *mlxOutputFilter) Flush() string {
	if f.stopped || len(f.pending) == 0 {
		return ""
	}
	out := string(f.pending)
	f.pending = nil
	return out
}

func findTurnBoundary(pending []rune) int {
	best := -1
	text := string(pending)
	for _, marker := range mlxTurnBoundaryMarkers {
		if idx := strings.Index(text, marker); idx >= 0 && (best == -1 || idx < best) {
			best = idx
		}
	}
	return best
}

func maxTurnBoundaryRunes() int {
	maxLen := 0
	for _, marker := range mlxTurnBoundaryMarkers {
		if n := len([]rune(marker)); n > maxLen {
			maxLen = n
		}
	}
	return maxLen
}
