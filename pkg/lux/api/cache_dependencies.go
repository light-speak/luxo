package api

import (
	"fmt"
	"maps"
	"slices"

	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/light-speak/luxo/pkg/lux/selection"
)

// Built only by startup validation, never mutated while serving requests.
type cacheDependencyPlan struct {
	models    []schema.ComputedSource
	selection *schema.SelectionDependencies
	policy    *compiledCachePolicy
}

func (rt *Router) prepareCacheRequirements() (map[string]*cacheDependencyPlan, error) {
	if len(rt.requiredCache) == 0 {
		return nil, nil
	}
	names := slices.Sorted(maps.Keys(rt.requiredCache))
	if rt.responseCache == nil {
		return nil, fmt.Errorf("api: cached API %q requires an explicitly configured response cache", names[0])
	}
	if rt.Schema == nil {
		return nil, fmt.Errorf("api: cached API %q requires a registered schema", names[0])
	}
	plans := make(map[string]*cacheDependencyPlan, len(names))
	selections := make(map[string]*schema.SelectionDependencies)
	for _, name := range names {
		plan, err := rt.prepareCachePlan(name, selections)
		if err != nil {
			return nil, err
		}
		plans[name] = plan
	}
	return plans, nil
}

func (rt *Router) prepareCachePlan(name string, selections map[string]*schema.SelectionDependencies) (*cacheDependencyPlan, error) {
	definition := rt.Schema.APIs[name]
	if definition == nil || definition.CacheDependencies == nil {
		return nil, fmt.Errorf("api: cached API %q requires verified execution dependency metadata", name)
	}
	if definition.Stream {
		return nil, fmt.Errorf("api: stream %q cannot use response caching", name)
	}
	policy, err := rt.compileCachePolicy(definition.CachePolicy)
	if err != nil {
		return nil, fmt.Errorf("api: cached API %q: %w", name, err)
	}
	dependencies := definition.CacheDependencies
	// TTL accepts explicitly declared read-only external dependencies without
	// requiring a write-side invalidation channel.
	models, err := rt.prepareCacheModels(dependencies.Models)
	if err != nil {
		return nil, fmt.Errorf("api: cached API %q: %w", name, err)
	}
	selected, err := rt.prepareCacheSelection(definition.ReturnType, selections)
	if err != nil {
		return nil, fmt.Errorf("api: cached API %q: %w", name, err)
	}
	return &cacheDependencyPlan{models: models, selection: selected, policy: policy}, nil
}

func (rt *Router) prepareCacheModels(models []schema.ComputedSource) ([]schema.ComputedSource, error) {
	result := make([]schema.ComputedSource, 0, len(models))
	for _, source := range models {
		model := rt.Schema.Models[source.Model]
		if model == nil {
			return nil, fmt.Errorf("dependency model %q is not registered", source.Model)
		}
		if source.Module != model.Module {
			return nil, fmt.Errorf("dependency module mismatch for %s", source.Model)
		}
		if !slices.Contains(result, source) {
			result = append(result, source)
		}
	}
	return result, nil
}

func (rt *Router) prepareCacheSelection(name string, prepared map[string]*schema.SelectionDependencies) (*schema.SelectionDependencies, error) {
	if plan := prepared[name]; plan != nil {
		return plan, nil
	}
	if rt.Schema.LookupModel(name) != nil {
		plan, err := rt.Schema.PrepareSelectionDependencies(name)
		if err == nil {
			prepared[name] = plan
		}
		return plan, err
	}
	if rt.Schema.Enums[name] != nil {
		return nil, nil
	}
	switch name {
	case "", "Int", "Float", "Boolean", "String", "DateTime", "Duration", "UUID", "Decimal", "JSON", "Bytes":
		return nil, nil
	default:
		return nil, fmt.Errorf("cache return type %q is not registered", name)
	}
}

func (p *cacheDependencyPlan) appendModels(dst []schema.ComputedSource, selected []*selection.Field) ([]schema.ComputedSource, error) {
	original := dst
	// Startup already deduplicated execution reads. The normal empty-buffer
	// path can copy the immutable set rather than repeat quadratic work.
	if len(dst) == 0 {
		dst = append(dst, p.models...)
	} else {
		for _, source := range p.models {
			if !slices.Contains(dst, source) {
				dst = append(dst, source)
			}
		}
	}
	if p.selection == nil {
		return dst, nil
	}
	result, err := p.selection.AppendSources(dst, selected)
	if err != nil {
		return original, err
	}
	return result, nil
}
