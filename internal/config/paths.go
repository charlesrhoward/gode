package config

import (
	"os"
	"path/filepath"
	"strings"
)

func ConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "gode")
}

func GlobalConfigPath() string {
	return filepath.Join(ConfigDir(), "gode.json")
}

func ProjectConfigPath() string {
	return ".gode/gode.json"
}

func DBPath() string {
	dir := ConfigDir()
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "gode.db")
}

func DataDir() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".local", "share", "gode")
	os.MkdirAll(dir, 0755)
	return dir
}

// InstructionFiles returns the ordered list of instruction file paths to check.
// Project-level files come first, global last.
func InstructionFiles(cwd string) []string {
	paths := []string{
		filepath.Join(cwd, ".gode", "GODE.md"),
		filepath.Join(cwd, "GODE.md"),
		filepath.Join(ConfigDir(), "GODE.md"),
	}
	return paths
}

// LoadInstructions reads and concatenates all found instruction files.
// Returns empty string if none exist. Truncates at maxBytes.
func LoadInstructions(cwd string, maxBytes int) string {
	var parts []string
	totalSize := 0

	for _, path := range InstructionFiles(cwd) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)
		if totalSize+len(content) > maxBytes {
			remaining := maxBytes - totalSize
			if remaining > 0 {
				content = content[:remaining] + "\n... (truncated)"
			} else {
				break
			}
		}
		parts = append(parts, content)
		totalSize += len(content)
	}

	if len(parts) == 0 {
		return ""
	}

	return strings.Join(parts, "\n\n---\n\n")
}

