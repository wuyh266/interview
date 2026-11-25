package tool

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"example.com/minimal-agent/internal/session"
	"example.com/minimal-agent/internal/tool/protocol"
)

func TestBuiltinToolSchemas(t *testing.T) {
	registry, err := NewBuiltinTools()
	if err != nil {
		t.Fatal(err)
	}
	expected := []struct {
		name     string
		fields   []string
		required []string
	}{
		{name: CalculatorToolName, fields: []string{"expression"}, required: []string{"expression"}},
		{name: SearchToolName, fields: []string{"query"}, required: []string{"query"}},
		{name: TodoToolName, fields: []string{"action", "text"}, required: []string{"action"}},
	}
	specs := registry.Defs()
	for _, test := range expected {
		t.Run(test.name, func(t *testing.T) {
			index := slices.IndexFunc(specs, func(spec protocol.ToolSpec) bool { return spec.Name == test.name })
			if index == -1 {
				t.Fatal("tool definition is missing")
			}
			spec := specs[index]
			if strings.TrimSpace(spec.Description) == "" {
				t.Fatal("tool description is missing")
			}
			var schema struct {
				Type       string `json:"type"`
				Properties map[string]struct {
					Type string   `json:"type"`
					Enum []string `json:"enum"`
				} `json:"properties"`
				Required []string `json:"required"`
			}
			if err := json.Unmarshal(spec.Parameters, &schema); err != nil {
				t.Fatal(err)
			}
			if schema.Type != "object" || len(schema.Properties) != len(test.fields) || !slices.Equal(schema.Required, test.required) {
				t.Fatalf("unexpected parameter schema: %s", spec.Parameters)
			}
			for _, field := range test.fields {
				if schema.Properties[field].Type != "string" {
					t.Errorf("field %q must be a string in %s", field, spec.Parameters)
				}
			}
			if spec.Name == TodoToolName && !slices.Equal(schema.Properties["action"].Enum, []string{"add", "list"}) {
				t.Errorf("unexpected todo actions in %s", spec.Parameters)
			}
		})
	}
}

type registryTestTool struct{ spec protocol.ToolSpec }

func (tool registryTestTool) Spec() protocol.ToolSpec { return tool.spec }

func (registryTestTool) Execute(_ context.Context, _ *session.ToolState, _ json.RawMessage) (string, error) {
	return "pong", nil
}

func TestRegistryRegistersCustomToolAndRejectsInvalidDefinitions(t *testing.T) {
	var registry Registry
	ping := registryTestTool{spec: protocol.ToolSpec{
		Name: "ping", Description: "Return pong", Parameters: json.RawMessage(`{"type":"object"}`),
	}}
	if err := registry.Register(ping); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		tool Tool
	}{
		{name: "duplicate", tool: ping},
		{name: "missing tool"},
		{name: "invalid name", tool: registryTestTool{spec: protocol.ToolSpec{
			Name: "bad name", Description: "Return pong", Parameters: ping.spec.Parameters,
		}}},
		{name: "missing description", tool: registryTestTool{spec: protocol.ToolSpec{
			Name: "missing_description", Parameters: ping.spec.Parameters,
		}}},
		{name: "non-object schema", tool: registryTestTool{spec: protocol.ToolSpec{
			Name: "bad_schema", Description: "Return pong", Parameters: json.RawMessage(`[]`),
		}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := registry.Register(test.tool); err == nil {
				t.Fatal("expected registration to fail")
			}
		})
	}
	if specs := registry.Defs(); len(specs) != 1 || specs[0].Name != ping.spec.Name ||
		specs[0].Description != ping.spec.Description || string(specs[0].Parameters) != string(ping.spec.Parameters) {
		t.Fatalf("unexpected registered tools: %+v", specs)
	}
	if output, err := registry.Invoke(context.Background(), ping.spec.Name, json.RawMessage(`{}`)); err != nil || output != "pong" {
		t.Fatalf("invoking the registered tool returned %q, %v", output, err)
	}
}
