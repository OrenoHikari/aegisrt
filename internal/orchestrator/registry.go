package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"aegisrt/internal/planner"
	"aegisrt/internal/scheduler"
)

// JobFactory adapts one cognitive task to the existing CAPSuleRT Job/ACB
// execution model.
type JobFactory func(ctx context.Context, task planner.Task) (scheduler.Job, error)

// Registration binds one planner-visible capability to its execution-plane
// adapter.
type Registration struct {
	Capability planner.Capability
	Build      JobFactory
}

// Registry is the allowlist shared by Planner and Orchestrator.
type Registry struct {
	definitions []planner.Capability
	factories   map[string]JobFactory
}

// NewRegistry creates a closed capability catalog. Unknown Agent/action pairs
// cannot be converted into Runtime jobs.
func NewRegistry(registrations []Registration) (*Registry, error) {
	if len(registrations) == 0 {
		return nil, fmt.Errorf("at least one capability registration is required")
	}

	registry := &Registry{
		definitions: make([]planner.Capability, 0, len(registrations)),
		factories:   make(map[string]JobFactory, len(registrations)),
	}

	for _, registration := range registrations {
		capability := normalizeCapability(registration.Capability)
		name := planner.CapabilityName(capability)
		if name == "" {
			return nil, fmt.Errorf("capability name is required")
		}
		if registration.Build == nil {
			return nil, fmt.Errorf("capability %s has no job factory", name)
		}
		if err := capability.InputSchema.ValidateDefinition(); err != nil {
			return nil, fmt.Errorf("capability %s input schema: %w", name, err)
		}

		if _, exists := registry.factories[name]; exists {
			return nil, fmt.Errorf("capability %s is duplicated", name)
		}

		registry.definitions = append(registry.definitions, cloneCapability(capability))
		registry.factories[name] = registration.Build
	}

	return registry, nil
}

// Lookup returns a copy of one registered capability definition.
func (r *Registry) Lookup(name string) (planner.Capability, bool) {
	if r == nil {
		return planner.Capability{}, false
	}
	name = strings.TrimSpace(name)
	for _, capability := range r.definitions {
		if capability.Name == name {
			return cloneCapability(capability), true
		}
	}
	return planner.Capability{}, false
}

// Capabilities returns a copy suitable for Planner prompting and validation.
func (r *Registry) Capabilities() []planner.Capability {
	if r == nil {
		return nil
	}

	result := make([]planner.Capability, len(r.definitions))
	for index, capability := range r.definitions {
		result[index] = cloneCapability(capability)
	}
	return result
}

// Build converts one allowed task into a native scheduler.Job.
func (r *Registry) Build(ctx context.Context, task planner.Task) (scheduler.Job, error) {
	if r == nil {
		return scheduler.Job{}, fmt.Errorf("capability registry is required")
	}

	task = planner.Normalize(planner.Plan{Goal: "registry", Tasks: []planner.Task{task}}).Tasks[0]
	capability, exists := r.Lookup(task.Capability)
	if !exists {
		return scheduler.Job{}, fmt.Errorf("unregistered capability %s", task.Capability)
	}
	if err := capability.InputSchema.Validate(task.Arguments); err != nil {
		return scheduler.Job{}, fmt.Errorf("task %s arguments: %w", task.ID, err)
	}
	factory := r.factories[task.Capability]

	job, err := factory(ctx, task)
	if err != nil {
		return scheduler.Job{}, fmt.Errorf("build task %s with %s: %w", task.ID, task.Capability, err)
	}

	return job, nil
}

func cloneCapability(source planner.Capability) planner.Capability {
	result := source
	result.InputSchema = source.InputSchema.Clone()
	if source.OutputSchema != nil {
		result.OutputSchema = make(map[string]string, len(source.OutputSchema))
		for name, value := range source.OutputSchema {
			result.OutputSchema[name] = value
		}
	}
	result.RequiredParameters = append([]string(nil), source.RequiredParameters...)
	result.OptionalParameters = append([]string(nil), source.OptionalParameters...)
	return result
}

func normalizeCapability(source planner.Capability) planner.Capability {
	result := cloneCapability(source)
	result.Name = planner.CapabilityName(source)
	if len(result.InputSchema.Fields) == 0 &&
		(len(source.RequiredParameters) > 0 || len(source.OptionalParameters) > 0) {
		result.InputSchema.Fields = make(map[string]planner.ArgumentField)
		for _, name := range source.RequiredParameters {
			result.InputSchema.Fields[name] = planner.ArgumentField{
				Type:     planner.ArgumentString,
				Required: true,
			}
		}
		for _, name := range source.OptionalParameters {
			result.InputSchema.Fields[name] = planner.ArgumentField{Type: planner.ArgumentString}
		}
	}
	return result
}
