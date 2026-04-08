package lockfile

import (
	"path/filepath"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func BenchmarkUpdate(b *testing.B) {
	// 10 models × 20 fields each
	var models []*ast.ModelDecl
	for i := range 10 {
		var fields []*ast.FieldDecl
		for j := range 20 {
			fields = append(fields, &ast.FieldDecl{
				Name: "field_" + string(rune('a'+i)) + "_" + string(rune('0'+j)),
				Type: &ast.TypeRef{Name: "String"},
			})
		}
		models = append(models, &ast.ModelDecl{
			Name:   "Model" + string(rune('A'+i)),
			Fields: fields,
		})
	}
	ff := []*ast.File{{Name: "bench.luxo", Models: models}}

	b.ResetTimer()
	for range b.N {
		lf := New()
		lf.Update(ff)
	}
}

func BenchmarkSaveLoad(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "luxo.lock")

	lf := New()
	for i := range 10 {
		ml := &ModelLock{
			NextID: 21,
			Fields: make(map[string]int, 20),
		}
		for j := 1; j <= 20; j++ {
			ml.Fields["field_"+string(rune('0'+j))] = j
		}
		lf.Models["Model"+string(rune('A'+i))] = ml
	}
	lf.Save(path)

	b.ResetTimer()
	for range b.N {
		loaded, _ := Load(path)
		loaded.Save(path)
	}
}
