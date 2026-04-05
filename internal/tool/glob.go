package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tradecraft/gode/internal/permission"
)

type GlobTool struct{}

type globInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
}

func (t *GlobTool) Name() string { return "glob" }

func (t *GlobTool) Description() string {
	return "Find files matching a glob pattern. Supports ** for recursive matching. Returns matching file paths sorted by modification time."
}

func (t *GlobTool) InputSchema() json.RawMessage {
	return schema(`{
		"type": "object",
		"properties": {
			"pattern": {
				"type": "string",
				"description": "Glob pattern (e.g. '**/*.go', 'src/**/*.ts')"
			},
			"path": {
				"type": "string",
				"description": "Directory to search in (defaults to current directory)"
			}
		},
		"required": ["pattern"]
	}`)
}

func (t *GlobTool) Permission() permission.Level {
	return permission.ReadOnly
}

func (t *GlobTool) Execute(ctx context.Context, input json.RawMessage) (*Result, error) {
	var args globInput
	if err := json.Unmarshal(input, &args); err != nil {
		return &Result{Output: fmt.Sprintf("invalid input: %v", err), IsError: true}, nil
	}

	root := args.Path
	if root == "" {
		root = "."
	}

	type fileEntry struct {
		path    string
		modTime int64
	}

	var matches []fileEntry

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Skip hidden directories
		if info.IsDir() && strings.HasPrefix(info.Name(), ".") && path != root {
			return filepath.SkipDir
		}
		// Skip node_modules
		if info.IsDir() && info.Name() == "node_modules" {
			return filepath.SkipDir
		}

		if info.IsDir() {
			return nil
		}

		matched, _ := filepath.Match(args.Pattern, info.Name())
		if !matched {
			// Try matching full relative path for ** patterns
			rel, _ := filepath.Rel(root, path)
			matched, _ = filepath.Match(args.Pattern, rel)
		}

		if matched {
			matches = append(matches, fileEntry{path: path, modTime: info.ModTime().Unix()})
		}

		return nil
	})

	if err != nil {
		return &Result{Output: fmt.Sprintf("glob error: %v", err), IsError: true}, nil
	}

	// Sort by modification time (newest first)
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].modTime > matches[j].modTime
	})

	if len(matches) == 0 {
		return &Result{Output: "no files matched"}, nil
	}

	var b strings.Builder
	limit := 250
	for i, m := range matches {
		if i >= limit {
			b.WriteString(fmt.Sprintf("... and %d more files\n", len(matches)-limit))
			break
		}
		b.WriteString(m.path)
		b.WriteByte('\n')
	}

	return &Result{Output: fmt.Sprintf("%d files matched:\n%s", len(matches), b.String())}, nil
}
