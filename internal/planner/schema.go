package planner

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// ArgumentType is one lightweight JSON Schema value category.
type ArgumentType string

const (
	ArgumentString  ArgumentType = "string"
	ArgumentNumber  ArgumentType = "number"
	ArgumentBoolean ArgumentType = "boolean"
	ArgumentObject  ArgumentType = "object"
	ArgumentArray   ArgumentType = "array"
)

// ArgumentField describes one accepted structured Capability argument.
type ArgumentField struct {
	Type        ArgumentType `json:"type"`
	Description string       `json:"description"`
	Required    bool         `json:"required"`
	Enum        []string     `json:"enum,omitempty"`
}

// ArgumentSchema is a deliberately small JSON-schema subset. It validates
// planner output without introducing a heavyweight schema dependency.
type ArgumentSchema struct {
	Fields          map[string]ArgumentField `json:"fields"`
	AllowAdditional bool                     `json:"allow_additional"`
}

// ValidateDefinition checks Registry metadata before it reaches a prompt.
func (s ArgumentSchema) ValidateDefinition() error {
	for name, field := range s.Fields {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("argument name is required")
		}
		switch field.Type {
		case ArgumentString, ArgumentNumber, ArgumentBoolean, ArgumentObject, ArgumentArray:
		default:
			return fmt.Errorf("argument %q has unsupported type %q", name, field.Type)
		}
		if len(field.Enum) > 0 && field.Type != ArgumentString {
			return fmt.Errorf("argument %q enum requires string type", name)
		}
	}
	return nil
}

// Validate checks required fields, unknown fields, and JSON value types.
func (s ArgumentSchema) Validate(arguments map[string]any) error {
	if err := s.ValidateDefinition(); err != nil {
		return err
	}

	for name, field := range s.Fields {
		value, exists := arguments[name]
		if !exists {
			if field.Required {
				return fmt.Errorf("required argument %q is missing", name)
			}
			continue
		}
		if err := validateArgumentValue(name, value, field); err != nil {
			return err
		}
	}

	if !s.AllowAdditional {
		for name := range arguments {
			if _, exists := s.Fields[name]; !exists {
				return fmt.Errorf("argument %q is not registered", name)
			}
		}
	}

	return nil
}

// Clone returns an independent schema copy.
func (s ArgumentSchema) Clone() ArgumentSchema {
	result := ArgumentSchema{
		Fields:          make(map[string]ArgumentField, len(s.Fields)),
		AllowAdditional: s.AllowAdditional,
	}
	for name, field := range s.Fields {
		field.Enum = append([]string(nil), field.Enum...)
		result.Fields[name] = field
	}
	return result
}

func validateArgumentValue(name string, value any, field ArgumentField) error {
	valid := false
	switch field.Type {
	case ArgumentString:
		text, ok := value.(string)
		valid = ok && (!field.Required || strings.TrimSpace(text) != "")
		if valid && len(field.Enum) > 0 {
			valid = false
			for _, allowed := range field.Enum {
				if text == allowed {
					valid = true
					break
				}
			}
		}
	case ArgumentNumber:
		switch number := value.(type) {
		case float64:
			valid = !math.IsNaN(number) && !math.IsInf(number, 0)
		case float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
			valid = true
		}
	case ArgumentBoolean:
		_, valid = value.(bool)
	case ArgumentObject:
		_, valid = value.(map[string]any)
	case ArgumentArray:
		_, valid = value.([]any)
	}

	if !valid {
		return fmt.Errorf("argument %q must be %s", name, field.Type)
	}
	return nil
}
