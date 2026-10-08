package schema

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/light-speak/luxo/pkg/lux/selection"
)

// SelectionDependencies is an immutable snapshot of selection-triggered reads.
// It does not replace execution, authentication or native resource dependencies.
type SelectionDependencies struct {
	root    *dependencyNode
	sources []ComputedSource
	index   map[ComputedSource]int
}

type dependencyNode struct {
	name   string
	fields map[string]dependencyField
	all    []int
}

type dependencyField struct {
	sourceID int
	child    *dependencyNode
}

type dependencyBuilder struct {
	schema  *Schema
	nodes   map[string]*dependencyNode
	queue   []*dependencyNode
	parents map[*dependencyNode][]*dependencyNode
	sources []ComputedSource
	index   map[ComputedSource]int
}

// PrepareSelectionDependencies validates and snapshots reachable metadata during
// startup. Register every referenced model/type before preparing the plan.
func (s *Schema) PrepareSelectionDependencies(root string) (*SelectionDependencies, error) {
	b := dependencyBuilder{
		schema: s, nodes: make(map[string]*dependencyNode),
		parents: make(map[*dependencyNode][]*dependencyNode), index: make(map[ComputedSource]int),
	}
	node := b.node(root)
	for i := 0; i < len(b.queue); i++ {
		if err := b.prepareNode(b.queue[i]); err != nil {
			return nil, err
		}
	}
	b.closeDependencies()
	return &SelectionDependencies{root: node, sources: b.sources, index: b.index}, nil
}

func (b *dependencyBuilder) node(name string) *dependencyNode {
	if node := b.nodes[name]; node != nil {
		return node
	}
	node := &dependencyNode{name: name, fields: make(map[string]dependencyField)}
	b.nodes[name] = node
	b.queue = append(b.queue, node)
	return node
}

func (b *dependencyBuilder) prepareNode(node *dependencyNode) error {
	model := b.schema.LookupModel(node.name)
	if model == nil {
		return fmt.Errorf("cache dependency type %q is not registered", node.name)
	}
	for _, field := range model.Fields {
		if _, exists := node.fields[field.Name]; exists {
			return fmt.Errorf("duplicate cache dependency field %s.%s", node.name, field.Name)
		}
		prepared, err := b.prepareField(model, field)
		if err != nil {
			return err
		}
		node.fields[field.Name] = prepared
		if prepared.sourceID != 0 && !slices.Contains(node.all, prepared.sourceID) {
			node.all = append(node.all, prepared.sourceID)
		}
		if prepared.child != nil {
			b.parents[prepared.child] = append(b.parents[prepared.child], node)
		}
	}
	for name := range model.ComputedSources {
		field := model.FieldByName(name)
		if field == nil || !field.Computed {
			return fmt.Errorf("cache source %s.%s does not identify a computed field", node.name, name)
		}
	}
	return nil
}

func (b *dependencyBuilder) prepareField(model *Model, field Field) (dependencyField, error) {
	var prepared dependencyField
	if field.Computed {
		if field.Type == FieldModel {
			return prepared, fmt.Errorf("unsupported structured computed cache source %s.%s", model.Name, field.Name)
		}
		source, ok := model.ComputedSources[field.Name]
		if !ok {
			return prepared, fmt.Errorf("cache source missing for computed field %s.%s", model.Name, field.Name)
		}
		if err := b.validateSource(source); err != nil {
			return prepared, err
		}
		prepared.sourceID = b.sourceID(source)
	}
	if field.Type != FieldModel {
		return prepared, nil
	}
	target := b.schema.LookupModel(field.TypeName)
	if target == nil {
		return prepared, fmt.Errorf("cache dependency type %q for %s.%s is not registered", field.TypeName, model.Name, field.Name)
	}
	if field.TargetModule != "" && field.TargetModule != target.Module {
		return prepared, fmt.Errorf("cache dependency module mismatch for %s.%s", model.Name, field.Name)
	}
	// A DTO containing a model is not itself evidence of a database read.
	if field.Relation && b.schema.Models[model.Name] != nil && b.schema.Models[target.Name] != nil {
		prepared.sourceID = b.sourceID(ComputedSource{Model: target.Name, Module: target.Module})
	}
	prepared.child = b.node(target.Name)
	return prepared, nil
}

func (b *dependencyBuilder) sourceID(source ComputedSource) int {
	if id := b.index[source]; id != 0 {
		return id
	}
	b.sources = append(b.sources, source)
	id := len(b.sources)
	b.index[source] = id
	return id
}

func (b *dependencyBuilder) validateSource(source ComputedSource) error {
	model := b.schema.Models[source.Model]
	if model == nil {
		return fmt.Errorf("cache dependency model %q is not registered", source.Model)
	}
	if model.Module != source.Module {
		return fmt.Errorf("cache dependency module mismatch for %s", source.Model)
	}
	return nil
}

// Propagate each node/source pair once, including recursive model graphs.
func (b *dependencyBuilder) closeDependencies() {
	type entry struct {
		node   *dependencyNode
		source int
	}
	var pending []entry
	seen := make(map[entry]bool)
	for _, node := range b.queue {
		for _, source := range node.all {
			item := entry{node, source}
			seen[item] = true
			pending = append(pending, item)
		}
	}
	for i := 0; i < len(pending); i++ {
		item := pending[i]
		for _, parent := range b.parents[item.node] {
			next := entry{parent, item.source}
			if !seen[next] {
				seen[next] = true
				parent.all = append(parent.all, item.source)
				pending = append(pending, next)
			}
		}
	}
	for _, node := range b.queue {
		slices.SortFunc(node.all, func(left, right int) int {
			a, b := b.sources[left-1], b.sources[right-1]
			if order := cmp.Compare(a.Module, b.Module); order != 0 {
				return order
			}
			return cmp.Compare(a.Model, b.Model)
		})
	}
}

// AppendSources appends distinct sources to caller-owned storage. It reuses the
// parsed selection; up to 256 sources use stack scratch. Larger plans allocate
// a bitset proportional to their source count. Output grows only as needed.
// On error the original length is returned; appended capacity must not be consumed. Missing
// nested selections conservatively include the prebuilt reachable closure, not
// an implicit change to the response selection. The plan is safe to share.
func (p *SelectionDependencies) AppendSources(dst []ComputedSource, fields []*selection.Field) ([]ComputedSource, error) {
	if len(fields) == 0 {
		return dst, fmt.Errorf("cache dependencies require an explicit root selection")
	}
	collector := dependencyCollector{dst: dst, sources: p.sources}
	if len(p.sources) > 256 {
		collector.overflow = make([]uint64, (len(p.sources)+63)/64)
	}
	for _, source := range dst {
		if id := p.index[source]; id != 0 {
			collector.mark(id)
		}
	}
	if err := p.root.appendSelected(&collector, fields, 0); err != nil {
		return dst, err
	}
	return collector.dst, nil
}

func (n *dependencyNode) appendSelected(collector *dependencyCollector, fields []*selection.Field, depth int) error {
	if depth >= 32 {
		return fmt.Errorf("cache dependency selection depth exceeds 32")
	}
	for _, field := range fields {
		if field == nil {
			return fmt.Errorf("nil cache dependency selection on %s", n.name)
		}
		prepared, ok := n.fields[field.Name]
		if !ok {
			return fmt.Errorf("unknown cache dependency selection %s.%s", n.name, field.Name)
		}
		if prepared.sourceID != 0 {
			collector.add(prepared.sourceID)
		}
		if err := prepared.appendChildren(collector, field, depth); err != nil {
			return err
		}
	}
	return nil
}

func (f dependencyField) appendChildren(collector *dependencyCollector, selected *selection.Field, depth int) error {
	if len(selected.Children) != 0 {
		if f.child == nil {
			return fmt.Errorf("cache dependency scalar %s has children", selected.Name)
		}
		return f.child.appendSelected(collector, selected.Children, depth+1)
	}
	if f.child != nil {
		for _, source := range f.child.all {
			collector.add(source)
		}
	}
	return nil
}

type dependencyCollector struct {
	dst      []ComputedSource
	sources  []ComputedSource
	seen     [4]uint64
	overflow []uint64
}

func (c *dependencyCollector) add(id int) {
	if c.mark(id) {
		c.dst = append(c.dst, c.sources[id-1])
	}
}

func (c *dependencyCollector) mark(id int) bool {
	seen := c.seen[:]
	if c.overflow != nil {
		seen = c.overflow
	}
	word, bit := (id-1)/64, uint64(1)<<((id-1)%64)
	if seen[word]&bit != 0 {
		return false
	}
	seen[word] |= bit
	return true
}
