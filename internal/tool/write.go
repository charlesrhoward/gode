package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tradecraft/gode/internal/permission"
)

type WriteTool struct{}

type writeInput struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

func (t *WriteTool) Name() string { return "write_file" }

func (t *WriteTool) Description() string {
	return "Write content to a file. Creates the file and any parent directories if they don't exist. Overwrites existing content."
}

func (t *WriteTool) InputSchema() json.RawMessage {
	return schema(`{
		"type": "object",
		"properties": {
			"file_path": {
				"type": "string",
				"description": "Path to the file to write"
			},
			"content": {
				"type": "string",
				"description": "Content to write to the file"
			}
		},
		"required": ["file_path", "content"]
	}`)
}

func (t *WriteTool) Permission() permission.Level {
	return permission.WorkspaceWrite
}

func (t *WriteTool) Execute(ctx context.Context, input json.RawMessage) (*Result, error) {
	var args writeInput
	if err := json.Unmarshal(input, &args); err != nil {
		return &Result{Output: fmt.Sprintf("invalid input: %v", err), IsError: true}, nil
	}

	dir := filepath.Dir(args.FilePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return &Result{Output: fmt.Sprintf("cannot create directory: %v", err), IsError: true}, nil
	}

	if err := os.WriteFile(args.FilePath, []byte(args.Content), 0644); err != nil {
		return &Result{Output: fmt.Sprintf("cannot write file: %v", err), IsError: true}, nil
	}

	lines := 1
	for _, c := range args.Content {
		if c == '\n' {
			lines++
		}
	}

	return &Result{Output: fmt.Sprintf("wrote %d bytes (%d lines) to %s", len(args.Content), lines, args.FilePath)}, nil
}
