package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/tradecraft/gode/internal/permission"
)

type BashTool struct{}

type bashInput struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

func (t *BashTool) Name() string { return "bash" }

func (t *BashTool) Description() string {
	return "Execute a bash command and return its output. Use for system commands, running tests, installing packages, git operations, and other shell tasks."
}

func (t *BashTool) InputSchema() json.RawMessage {
	return schema(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "The bash command to execute"
			},
			"timeout": {
				"type": "integer",
				"description": "Timeout in seconds (default 120)"
			}
		},
		"required": ["command"]
	}`)
}

func (t *BashTool) Permission() permission.Level {
	return permission.FullAccess
}

func (t *BashTool) Execute(ctx context.Context, input json.RawMessage) (*Result, error) {
	var args bashInput
	if err := json.Unmarshal(input, &args); err != nil {
		return &Result{Output: fmt.Sprintf("invalid input: %v", err), IsError: true}, nil
	}

	timeout := 120
	if args.Timeout > 0 {
		timeout = args.Timeout
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", args.Command)
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()

	result := strings.TrimRight(string(output), "\n")
	if len(result) > 100000 {
		result = result[:100000] + "\n... (output truncated at 100K chars)"
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return &Result{Output: fmt.Sprintf("command timed out after %ds\n%s", timeout, result), IsError: true}, nil
		}
		// Show the output first, then the exit code on its own line
		if result != "" {
			return &Result{Output: fmt.Sprintf("%s\n\nexit status: %v", result, err), IsError: true}, nil
		}
		return &Result{Output: fmt.Sprintf("exit status: %v", err), IsError: true}, nil
	}

	return &Result{Output: result}, nil
}
