package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMergesProviderConfigAcrossFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	globalDir := filepath.Join(home, ".config", "gode")
	if err := os.MkdirAll(globalDir, 0755); err != nil {
		t.Fatalf("mkdir global config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "gode.json"), []byte(`{
		"providers": {
			"anthropic": {
				"api_key": "global-key"
			}
		}
	}`), 0644); err != nil {
		t.Fatalf("write global config: %v", err)
	}

	projectDir := t.TempDir()
	t.Chdir(projectDir)
	if err := os.MkdirAll(".gode", 0755); err != nil {
		t.Fatalf("mkdir project config dir: %v", err)
	}
	if err := os.WriteFile(".gode/gode.json", []byte(`{
		"providers": {
			"anthropic": {
				"base_url": "https://example.invalid"
			}
		}
	}`), 0644); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	pc := cfg.ProviderConfig("anthropic")
	if pc.APIKey != "global-key" {
		t.Fatalf("expected merged API key, got %q", pc.APIKey)
	}
	if pc.BaseURL != "https://example.invalid" {
		t.Fatalf("expected merged base URL, got %q", pc.BaseURL)
	}
}

func TestLoadReturnsErrorOnMalformedProjectConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	projectDir := t.TempDir()
	t.Chdir(projectDir)
	if err := os.MkdirAll(".gode", 0755); err != nil {
		t.Fatalf("mkdir project config dir: %v", err)
	}
	if err := os.WriteFile(".gode/gode.json", []byte(`{"provider":`), 0644); err != nil {
		t.Fatalf("write malformed project config: %v", err)
	}

	_, err := Load()
	if err == nil {
		t.Fatal("expected config load error")
	}
	if !strings.Contains(err.Error(), "loading project config") {
		t.Fatalf("expected project config error context, got %v", err)
	}
}
