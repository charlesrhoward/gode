package tool

import "testing"

func TestMatchGlobDoublestar(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"**/*.go", "main.go", true},
		{"**/*.go", "internal/agent/agent.go", true},
		{"**/*.go", "cmd/gode/main.go", true},
		{"**/*.go", "README.md", false},
		{"*.go", "main.go", true},
		{"*.go", "internal/agent/agent.go", false}, // no ** so only matches filename
		{"src/**/*.ts", "src/components/App.ts", true},
		{"src/**/*.ts", "src/App.ts", true},
		{"src/**/*.ts", "lib/App.ts", false},
	}

	for _, tt := range tests {
		got := matchGlob(tt.pattern, tt.path)
		if got != tt.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}
