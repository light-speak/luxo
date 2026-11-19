package codegen

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
)

type columnRenamePlanner struct {
	table       string
	pending     map[string]DiffOp
	predecessor map[string]string
	occupied    map[string]bool
	reserved    map[string]bool
	keys        []string
	ready       []string
	cursor      int
	ops         []DiffOp
}

func newColumnRenamePlanner(model, table string, renames map[string]string, current, desired *ModelState) (*columnRenamePlanner, error) {
	p := &columnRenamePlanner{table: table, pending: make(map[string]DiffOp, len(renames)),
		predecessor: make(map[string]string, len(renames)), occupied: make(map[string]bool, len(current.Columns)),
		reserved: make(map[string]bool, len(current.Columns)+len(desired.Columns)), keys: sortedMigrationKeys(renames)}
	for name := range current.Columns {
		p.occupied[name], p.reserved[name] = true, true
	}
	for name := range desired.Columns {
		p.reserved[name] = true
	}
	for _, old := range p.keys {
		next := renames[old]
		if previous, exists := p.predecessor[next]; exists {
			return nil, fmt.Errorf("columns %q and %q both rename to %q", previous, old, next)
		}
		p.predecessor[next] = old
		p.pending[old] = DiffOp{Kind: RenameColumn, Model: model, Table: table, OldName: old, Column: next,
			OldColumn: current.Columns[old], NewColumn: desired.Columns[next]}
	}
	for _, old := range p.keys {
		if _, blocked := p.pending[renames[old]]; !blocked {
			p.ready = append(p.ready, old)
		}
	}
	return p, nil
}

func (p *columnRenamePlanner) plan() ([]DiffOp, error) {
	for head := 0; len(p.pending) > 0; {
		if head == len(p.ready) {
			if err := p.breakCycle(); err != nil {
				return nil, err
			}
		}
		old := p.ready[head]
		head++
		op := p.pending[old]
		if p.occupied[op.Column] {
			return nil, fmt.Errorf("rename %q to %q would overwrite a nonmoving column; use an explicit migration", old, op.Column)
		}
		p.ops = append(p.ops, op)
		delete(p.pending, old)
		delete(p.occupied, old)
		p.occupied[op.Column] = true
		p.releasePredecessor(old)
	}
	return p.ops, nil
}

func (p *columnRenamePlanner) releasePredecessor(name string) {
	previous := p.predecessor[name]
	if _, exists := p.pending[previous]; exists {
		p.ready = append(p.ready, previous)
	}
}

func (p *columnRenamePlanner) breakCycle() error {
	for p.cursor < len(p.keys) {
		old := p.keys[p.cursor]
		p.cursor++
		op, exists := p.pending[old]
		if !exists {
			continue
		}
		temporary := p.temporaryName(old, op.Column)
		first := op
		first.Column, first.NewColumn = temporary, op.OldColumn
		p.ops = append(p.ops, first)
		delete(p.pending, old)
		delete(p.occupied, old)
		p.occupied[temporary] = true
		op.OldName = temporary
		p.pending[temporary] = op
		p.predecessor[op.Column] = temporary
		p.releasePredecessor(old)
		return nil
	}
	return fmt.Errorf("invalid column rename dependency graph for table %q", p.table)
}

func (p *columnRenamePlanner) temporaryName(old, next string) string {
	for counter := 0; ; counter++ {
		hash := sha256.Sum256([]byte(p.table + "\x00" + old + "\x00" + next + "\x00" + strconv.Itoa(counter)))
		var suffix [16]byte
		hex.Encode(suffix[:], hash[:8])
		name := "__luxo_rename_" + string(suffix[:])
		if !p.reserved[name] {
			p.reserved[name] = true
			return name
		}
	}
}

func validateColumnTransition(model, name string, old, next *ColumnState) error {
	if old.PK != next.PK || old.Serial != next.Serial {
		return columnIdentityError(model, name)
	}
	return nil
}

func columnIdentityError(model, name string) error {
	return fmt.Errorf("%s.%s changes primary-key or serial identity; use an explicit migration", model, name)
}
