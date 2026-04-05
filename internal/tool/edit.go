package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/tradecraft/gode/internal/permission"
)

type EditTool struct{}

type editInput struct {
	FilePath  string `json:"file_path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

func (t *EditTool) Name() string { return "edit_file" }

func (t *EditTool) Description() string {
	return "Edit a file by replacing an exact string match. The old_string must be unique within the file. Use this for targeted modifications to existing files."
}

func (t *EditTool) InputSchema() json.RawMessage {
	return schema(`{
		"type": "object",
		"properties": {
			"file_path": {
				"type": "string",
				"description": "Path to the file to edit"
			},
			"old_string": {
				"type": "string",
				"description": "The exact string to find and replace (must be unique in file)"
			},
			"new_string": {
				"type": "string",
				"description": "The replacement string"
			}
		},
		"required": ["file_path", "old_string", "new_string"]
	}`)
}

func (t *EditTool) Permission() permission.Level {
	return permission.WorkspaceWrite
}

func (t *EditTool) Execute(ctx context.Context, input json.RawMessage) (*Result, error) {
	var args editInput
	if err := json.Unmarshal(input, &args); err != nil {
		return &Result{Output: fmt.Sprintf("invalid input: %v", err), IsError: true}, nil
	}

	content, err := os.ReadFile(args.FilePath)
	if err != nil {
		return &Result{Output: fmt.Sprintf("cannot read file: %v", err), IsError: true}, nil
	}

	fileContent := string(content)
	count := strings.Count(fileContent, args.OldString)

	if count == 0 {
		return &Result{Output: "old_string not found in file", IsError: true}, nil
	}
	if count > 1 {
		return &Result{Output: fmt.Sprintf("old_string found %d times — must be unique. Provide more context.", count), IsError: true}, nil
	}

	matchIdx := strings.Index(fileContent, args.OldString)
	lineNum := strings.Count(fileContent[:matchIdx], "\n") + 1
	newContent := strings.Replace(fileContent, args.OldString, args.NewString, 1)

	// Preserve original file permissions
	info, _ := os.Stat(args.FilePath)
	perm := info.Mode().Perm()
	if err := os.WriteFile(args.FilePath, []byte(newContent), perm); err != nil {
		return &Result{Output: fmt.Sprintf("cannot write file: %v", err), IsError: true}, nil
	}

	// Build a concise diff showing what changed
	oldLines := strings.Split(args.OldString, "\n")
	newLines := strings.Split(args.NewString, "\n")

	var diff strings.Builder
	diff.WriteString(fmt.Sprintf("edited %s at line %d\n", args.FilePath, lineNum))
	for _, l := range oldLines {
		diff.WriteString(fmt.Sprintf("- %s\n", l))
	}
	for _, l := range newLines {
		diff.WriteString(fmt.Sprintf("+ %s\n", l))
	}

	return &Result{Output: diff.String()}, nil
}
