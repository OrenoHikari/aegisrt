package planner

import (
	"strings"
	"testing"
)

func TestArgumentSchemaValidation(t *testing.T) {
	schema := ArgumentSchema{Fields: map[string]ArgumentField{
		"path":  {Type: ArgumentString, Required: true},
		"limit": {Type: ArgumentNumber},
	}}
	tests := []struct {
		name      string
		arguments map[string]any
		want      string
	}{
		{name: "valid", arguments: map[string]any{"path": "data.json", "limit": float64(3)}},
		{name: "missing", arguments: map[string]any{}, want: "required argument"},
		{name: "wrong type", arguments: map[string]any{"path": true}, want: "must be string"},
		{name: "unknown", arguments: map[string]any{"path": "data.json", "command": "rm"}, want: "not registered"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := schema.Validate(test.arguments)
			if test.want == "" && err != nil {
				t.Fatalf("validate arguments: %v", err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("expected %q error, got %v", test.want, err)
			}
		})
	}
}

func TestValidateStructuredCapabilityArguments(t *testing.T) {
	capabilities := []Capability{{
		Name: "file.inspect",
		InputSchema: ArgumentSchema{Fields: map[string]ArgumentField{
			"path": {Type: ArgumentString, Required: true},
		}},
	}}
	plan := Plan{Goal: "inspect", Tasks: []Task{{
		ID: "inspect", Name: "inspect", Description: "inspect", Capability: "file.inspect",
		Arguments: map[string]any{"path": float64(7)},
	}}}
	if _, err := Validate(plan, capabilities); err == nil || !strings.Contains(err.Error(), "must be string") {
		t.Fatalf("expected structured type validation error, got %v", err)
	}
}
