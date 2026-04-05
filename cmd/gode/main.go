package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/tradecraft/gode/internal/agent"
	"github.com/tradecraft/gode/internal/config"
	"github.com/tradecraft/gode/internal/permission"
	"github.com/tradecraft/gode/internal/provider"
	"github.com/tradecraft/gode/internal/session"
	"github.com/tradecraft/gode/internal/storage"
	"github.com/tradecraft/gode/internal/tool"
	"github.com/tradecraft/gode/internal/tui"
)

var version = "0.1.0"

func main() {
	showVersion := flag.Bool("version", false, "print version")
	providerFlag := flag.String("provider", "", "provider to use (overrides config)")
	modelFlag := flag.String("model", "", "model to use (overrides config)")
	promptFlag := flag.String("p", "", "run a single prompt headless (no TUI)")
	flag.Parse()

	if *showVersion {
		fmt.Printf("gode v%s\n", version)
		os.Exit(0)
	}

	// Also accept prompt as remaining args
	prompt := *promptFlag
	if prompt == "" && flag.NArg() > 0 {
		prompt = strings.Join(flag.Args(), " ")
	}

	if err := run(*providerFlag, *modelFlag, prompt); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(providerOverride, modelOverride, prompt string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if providerOverride != "" {
		cfg.Provider = providerOverride
	}
	if modelOverride != "" {
		cfg.Model = modelOverride
	}

	if err := cfg.Validate(); err != nil {
		return err
	}

	provCfg := cfg.ProviderConfig(cfg.Provider)
	var prov provider.Provider
	var runtimeInfo []string
	switch cfg.Provider {
	case "mlx_vlm":
		prov, err = provider.NewMLXVLM(provider.MLXVLMConfig{
			PythonPath: provCfg.Python,
		})
	case "ollama":
		prov, err = provider.NewOllama(provider.OllamaConfig{
			BaseURL: provCfg.BaseURL,
		})
	case "anthropic":
		prov, err = provider.NewAnthropic(provider.AnthropicConfig{
			APIKey:  provCfg.APIKey,
			BaseURL: provCfg.BaseURL,
		})
	default:
		return fmt.Errorf("unsupported provider %q", cfg.Provider)
	}
	if err != nil {
		return fmt.Errorf("creating %s provider: %w", cfg.Provider, err)
	}
	runtimeInfo = providerRuntimeInfo(prov)

	store, err := storage.NewSQLiteStore(config.DBPath())
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer store.Close()

	registry := tool.NewRegistry()
	tool.RegisterBuiltins(registry)

	cwd, _ := os.Getwd()
	sess, err := session.GetOrCreate(store, cwd)
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

	perms := permission.NewManager(cfg.Permissions)

	ag := agent.New(agent.Config{
		Provider:      prov,
		Tools:         registry,
		Permissions:   perms,
		Store:         store,
		Session:       sess,
		Model:         cfg.Model,
		ContextTokens: cfg.ContextTokens,
		MaxTokens:     cfg.MaxTokens,
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Headless mode: single prompt, print output, exit
	if prompt != "" {
		return runHeadless(ctx, ag, perms, prompt, runtimeInfo)
	}

	// TUI mode
	app := tui.New(ag, sess, version, cfg.Provider, cfg.Model, runtimeInfo)
	app.SetPermissions(perms)
	return app.Run(ctx)
}

func runHeadless(ctx context.Context, ag *agent.Agent, perms *permission.Manager, prompt string, runtimeInfo []string) error {
	// Auto-allow all tools in headless mode
	perms.AcceptAll()

	if len(runtimeInfo) > 0 {
		fmt.Fprintln(os.Stderr, "startup check:")
		for _, line := range runtimeInfo {
			fmt.Fprintf(os.Stderr, "  %s\n", line)
		}
	}

	// Consume events and print
	go func() {
		for evt := range ag.Events() {
			switch e := evt.(type) {
			case agent.EventText:
				fmt.Print(e.Text)
			case agent.EventToolStart:
				fmt.Printf("\n── %s ──\n", e.Name)
			case agent.EventToolEnd:
				if e.Result != nil {
					output := e.Result.Output
					if len(output) > 2000 {
						output = output[:2000] + "..."
					}
					fmt.Printf("%s\n", output)
				}
				fmt.Println("────────")
			case agent.EventTurnDone:
				fmt.Printf("\n\n[tokens: %d in, %d out]\n", e.Usage.InputTokens, e.Usage.OutputTokens)
			case agent.EventError:
				fmt.Fprintf(os.Stderr, "error: %v\n", e.Err)
			}
		}
	}()

	ag.Run(ctx, prompt)
	return nil
}

func providerRuntimeInfo(prov provider.Provider) []string {
	mlx, ok := prov.(*provider.MLXVLM)
	if !ok {
		return nil
	}

	checkCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	info, err := mlx.RuntimeInfo(checkCtx)
	lines := []string{fmt.Sprintf("python: %s", info.PythonPath)}
	if err != nil {
		lines = append(lines, fmt.Sprintf("mlx_vlm: unavailable (%v)", err))
		return lines
	}
	lines = append(lines, fmt.Sprintf("mlx_vlm: %s", info.BackendPath))
	return lines
}
