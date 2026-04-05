package config

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/tradecraft/gode/internal/permission"
)

type Config struct {
	Model         string                    `json:"model"`
	Provider      string                    `json:"provider"`
	ContextTokens int                       `json:"context_tokens"`
	MaxTokens     int                       `json:"max_tokens"`
	Providers     map[string]ProviderConfig `json:"providers"`
	Permissions   permission.Ruleset        `json:"permissions"`
}

type ProviderConfig struct {
	APIKey  string `json:"api_key,omitempty"`
	BaseURL string `json:"base_url,omitempty"`
	Python  string `json:"python,omitempty"`
}

func (c *Config) ProviderConfig(name string) ProviderConfig {
	if pc, ok := c.Providers[name]; ok {
		return pc
	}
	return ProviderConfig{}
}

func Load() (*Config, error) {
	cfg := &Config{
		Model:         "mlx-community/gemma-4-31b-8bit",
		Provider:      "mlx_vlm",
		ContextTokens: 32768,
		MaxTokens:     4096,
		Providers:     make(map[string]ProviderConfig),
	}

	// 1. Global config
	loadFile(GlobalConfigPath(), cfg)

	// 2. Project config
	loadFile(ProjectConfigPath(), cfg)

	// 3. Environment overrides
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		pc := cfg.Providers["anthropic"]
		pc.APIKey = key
		cfg.Providers["anthropic"] = pc
	}
	if baseURL := os.Getenv("ANTHROPIC_BASE_URL"); baseURL != "" {
		pc := cfg.Providers["anthropic"]
		pc.BaseURL = baseURL
		cfg.Providers["anthropic"] = pc
	}
	if host := os.Getenv("OLLAMA_HOST"); host != "" {
		pc := cfg.Providers["ollama"]
		pc.BaseURL = host
		cfg.Providers["ollama"] = pc
	}
	if pythonPath := os.Getenv("GODE_MLX_PYTHON"); pythonPath != "" {
		pc := cfg.Providers["mlx_vlm"]
		pc.Python = pythonPath
		cfg.Providers["mlx_vlm"] = pc
	}

	return cfg, nil
}

// Validate checks that the config has required fields and returns helpful errors.
func (c *Config) Validate() error {
	if c.Model == "" {
		return fmt.Errorf("no model configured — set \"model\" in %s", GlobalConfigPath())
	}
	if c.ContextTokens <= 0 {
		return fmt.Errorf("context_tokens must be greater than zero")
	}
	if c.MaxTokens <= 0 {
		return fmt.Errorf("max_tokens must be greater than zero")
	}
	switch c.Provider {
	case "mlx_vlm":
		return nil
	case "ollama":
		return nil
	case "anthropic":
		pc := c.ProviderConfig("anthropic")
		if pc.APIKey == "" {
			return fmt.Errorf("Anthropic API key not configured.\n\n  Set the ANTHROPIC_API_KEY environment variable:\n    export ANTHROPIC_API_KEY=sk-ant-...\n\n  Or add it to %s:\n    {\n      \"provider\": \"anthropic\",\n      \"providers\": {\n        \"anthropic\": {\n          \"api_key\": \"sk-ant-...\"\n        }\n      }\n    }", GlobalConfigPath())
		}
		return nil
	default:
		return fmt.Errorf("unsupported provider %q — supported providers: mlx_vlm, ollama, anthropic", c.Provider)
	}
}

func loadFile(path string, cfg *Config) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var overlay Config
	if err := json.Unmarshal(data, &overlay); err != nil {
		return
	}

	if overlay.Model != "" {
		cfg.Model = overlay.Model
	}
	if overlay.Provider != "" {
		cfg.Provider = overlay.Provider
	}
	if overlay.ContextTokens > 0 {
		cfg.ContextTokens = overlay.ContextTokens
	}
	if overlay.MaxTokens > 0 {
		cfg.MaxTokens = overlay.MaxTokens
	}
	for k, v := range overlay.Providers {
		cfg.Providers[k] = v
	}
	if len(overlay.Permissions) > 0 {
		cfg.Permissions = overlay.Permissions
	}
}
