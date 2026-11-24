package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"example.com/minimal-agent/internal/session"
	"example.com/minimal-agent/internal/tool/protocol"
)

type registeredTool struct {
	tool Tool
	spec protocol.ToolSpec
}

// Registry 管理工具定义。零值可以使用。
type Registry struct {
	mu    sync.RWMutex
	tools map[string]registeredTool
}

var (
	_ Tools        = (*Registry)(nil)
	_ ToolRegistry = (*Registry)(nil)
)

var validToolName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

// NewBuiltinTools 创建包含 calculator、search 和 todo 的注册表。
func NewBuiltinTools() (*Registry, error) {
	registry := &Registry{}
	for _, tool := range []Tool{CalculatorTool{}, SearchTool{}, TodoTool{}} {
		if err := registry.Register(tool); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func (r *Registry) Register(tool Tool) error {
	if tool == nil {
		return errors.New("tool is required")
	}
	spec := tool.Spec()
	if !validToolName.MatchString(spec.Name) || strings.TrimSpace(spec.Description) == "" {
		return fmt.Errorf("invalid definition for tool %q", spec.Name)
	}
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(spec.Parameters, &schema); err != nil || schema == nil {
		return fmt.Errorf("tool %q parameters must be a JSON Schema object", spec.Name)
	}
	spec.Parameters = bytes.Clone(spec.Parameters)

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[spec.Name]; exists {
		return fmt.Errorf("tool %q is already registered", spec.Name)
	}
	if r.tools == nil {
		r.tools = make(map[string]registeredTool)
	}
	r.tools[spec.Name] = registeredTool{tool: tool, spec: spec}
	return nil
}

func (r *Registry) Specs() []protocol.ToolSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	specs := make([]protocol.ToolSpec, 0, len(r.tools))
	for _, entry := range r.tools {
		spec := entry.spec
		spec.Parameters = bytes.Clone(spec.Parameters)
		specs = append(specs, spec)
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	return specs
}

func (r *Registry) Defs() []protocol.ToolSpec {
	return r.Specs()
}

func (r *Registry) Lookup(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, exists := r.tools[name]
	return entry.tool, exists
}

func (r *Registry) Invoke(ctx context.Context, name string, arguments json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	tool, exists := r.Lookup(name)
	if !exists {
		return "", fmt.Errorf("tool %q is unavailable", name)
	}
	return tool.Execute(ctx, session.ToolStateFromContext(ctx), arguments)
}
