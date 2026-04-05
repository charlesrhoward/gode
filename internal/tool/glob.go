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

// matchGlob extends filepath.Match with support for "**" recursive directory matching.
func matchGlob(pattern, path string) bool {
	if !strings.Contains(pattern, "**") {
		matched, _ := filepath.Match(pattern, path)
		return matched
	}

	// Split pattern on "**" and match each segment
	parts := strings.SplitN(pattern, "**", 2)
	prefix := strings.TrimSuffix(parts[0], string(filepath.Separator))
	suffix := strings.TrimPrefix(parts[1], string(filepath.Separator))

	// "**/*.go" — prefix is empty, suffix is "*.go"
	// Match suffix against the filename
	if prefix == "" && !strings.Contains(suffix, "**") {
		matched, _ := filepath.Match(suffix, filepath.Base(path))
		return matched
	}

	// "src/**/*.go" — prefix is "src", suffix is "*.go"
	if prefix != "" {
		rel := path
		if !strings.HasPrefix(rel, prefix+string(filepath.Separator)) && rel != prefix {
			return false
		}
		// Strip the prefix and recurse
		remaining := strings.TrimPrefix(rel, prefix+string(filepath.Separator))
		return matchGlob("**"+string(filepath.Separator)+suffix, remaining)
	}

	// Fallback: try filepath.Match
	matched, _ := filepath.Match(pattern, path)
	return matched
}

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

		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") && path != root {
				return filepath.SkipDir
			}
			switch name {
			case "node_modules", "vendor", "build", "dist",
				".next", "__pycache__", ".venv", "target":
				return filepath.SkipDir
			}
			return nil
		}

		rel, _ := filepath.Rel(root, path)
		matched := matchGlob(args.Pattern, rel)

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
