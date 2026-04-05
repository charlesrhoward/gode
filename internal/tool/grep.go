package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tradecraft/gode/internal/permission"
)

type GrepTool struct{}

type grepInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
	Glob    string `json:"glob,omitempty"`
}

func (t *GrepTool) Name() string { return "grep" }

func (t *GrepTool) Description() string {
	return "Search file contents using a regular expression pattern. Returns matching lines with file paths and line numbers."
}

func (t *GrepTool) InputSchema() json.RawMessage {
	return schema(`{
		"type": "object",
		"properties": {
			"pattern": {
				"type": "string",
				"description": "Regular expression pattern to search for"
			},
			"path": {
				"type": "string",
				"description": "File or directory to search in (defaults to current directory)"
			},
			"glob": {
				"type": "string",
				"description": "File glob filter (e.g. '*.go', '*.ts')"
			}
		},
		"required": ["pattern"]
	}`)
}

func (t *GrepTool) Permission() permission.Level {
	return permission.ReadOnly
}

func (t *GrepTool) Execute(ctx context.Context, input json.RawMessage) (*Result, error) {
	var args grepInput
	if err := json.Unmarshal(input, &args); err != nil {
		return &Result{Output: fmt.Sprintf("invalid input: %v", err), IsError: true}, nil
	}

	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return &Result{Output: fmt.Sprintf("invalid regex: %v", err), IsError: true}, nil
	}

	root := args.Path
	if root == "" {
		root = "."
	}

	var b strings.Builder
	matchCount := 0
	maxMatches := 250

	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() {
				name := info.Name()
				if strings.HasPrefix(name, ".") && path != root {
					return filepath.SkipDir
				}
				if name == "node_modules" || name == "vendor" || name == ".git" {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if matchCount >= maxMatches {
			return filepath.SkipAll
		}

		// Apply glob filter
		if args.Glob != "" {
			matched, _ := filepath.Match(args.Glob, info.Name())
			if !matched {
				return nil
			}
		}

		// Skip binary files (check first 512 bytes)
		if info.Size() > 10*1024*1024 {
			return nil // skip files > 10MB
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		lineNum := 0

		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if re.MatchString(line) {
				matchCount++
				if matchCount > maxMatches {
					break
				}
				b.WriteString(fmt.Sprintf("%s:%d: %s\n", path, lineNum, line))
			}
		}

		return nil
	})

	if err != nil && err != filepath.SkipAll {
		return &Result{Output: fmt.Sprintf("search error: %v", err), IsError: true}, nil
	}

	if matchCount == 0 {
		return &Result{Output: "no matches found"}, nil
	}

	output := b.String()
	if matchCount > maxMatches {
		output += fmt.Sprintf("\n... (truncated, %d+ matches)\n", maxMatches)
	}

	return &Result{Output: output}, nil
}
