package tool

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/tradecraft/gode/internal/permission"
	"github.com/tradecraft/gode/internal/provider"
)

// Tool defines the interface for all agent tools.
type Tool interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage
	Permission() permission.Level
	Execute(ctx context.Context, input json.RawMessage) (*Result, error)
}

// Result is the output of a tool execution.
type Result struct {
	Output  string
	IsError bool
}

// Registry manages available tools.
type Registry struct {
	tools map[string]Tool
	order []string
	mu    sync.RWMutex
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name()]; !exists {
		r.order = append(r.order, t.Name())
	}
	r.tools[t.Name()] = t
}

func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) Definitions() []provider.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]provider.ToolDefinition, 0, len(r.tools))
	for _, name := range r.order {
		t := r.tools[name]
		defs = append(defs, provider.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return defs
}

func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]Tool, 0, len(r.tools))
	for _, name := range r.order {
		list = append(list, r.tools[name])
	}
	return list
}

// RegisterBuiltins adds all built-in tools.
func RegisterBuiltins(r *Registry) {
	r.Register(&BashTool{})
	r.Register(&ReadTool{})
	r.Register(&WriteTool{})
	r.Register(&EditTool{})
	r.Register(&GlobTool{})
	r.Register(&GrepTool{})
}

// schema is a helper to create JSON Schema bytes.
func schema(s string) json.RawMessage {
	return json.RawMessage(s)
}
