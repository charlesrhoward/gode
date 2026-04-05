package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/tradecraft/gode/internal/permission"
)

type ReadTool struct{}

type readInput struct {
	FilePath string `json:"file_path"`
	Offset   int    `json:"offset,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

func (t *ReadTool) Name() string { return "read_file" }

func (t *ReadTool) Description() string {
	return "Read the contents of a file. Returns the file content with line numbers. Supports offset and limit for reading portions of large files."
}

func (t *ReadTool) InputSchema() json.RawMessage {
	return schema(`{
		"type": "object",
		"properties": {
			"file_path": {
				"type": "string",
				"description": "Absolute or relative path to the file"
			},
			"offset": {
				"type": "integer",
				"description": "Line number to start reading from (0-based)"
			},
			"limit": {
				"type": "integer",
				"description": "Maximum number of lines to read (default: 2000)"
			}
		},
		"required": ["file_path"]
	}`)
}

func (t *ReadTool) Permission() permission.Level {
	return permission.ReadOnly
}

func (t *ReadTool) Execute(ctx context.Context, input json.RawMessage) (*Result, error) {
	var args readInput
	if err := json.Unmarshal(input, &args); err != nil {
		return &Result{Output: fmt.Sprintf("invalid input: %v", err), IsError: true}, nil
	}

	f, err := os.Open(args.FilePath)
	if err != nil {
		return &Result{Output: fmt.Sprintf("cannot read file: %v", err), IsError: true}, nil
	}
	defer f.Close()

	limit := 2000
	if args.Limit > 0 {
		limit = args.Limit
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var b strings.Builder
	lineNum := 0
	linesRead := 0

	for scanner.Scan() {
		lineNum++
		if lineNum <= args.Offset {
			continue
		}
		if linesRead >= limit {
			b.WriteString(fmt.Sprintf("... (%d+ more lines)\n", lineNum-args.Offset-limit))
			break
		}
		b.WriteString(fmt.Sprintf("%4d\t%s\n", lineNum, scanner.Text()))
		linesRead++
	}

	if err := scanner.Err(); err != nil {
		return &Result{Output: fmt.Sprintf("error reading file: %v", err), IsError: true}, nil
	}

	if linesRead == 0 {
		return &Result{Output: "(empty file)"}, nil
	}

	return &Result{Output: b.String()}, nil
}
