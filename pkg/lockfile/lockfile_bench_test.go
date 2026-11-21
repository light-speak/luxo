package lockfile

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

// This measures cold-tool parsing, excluding disk I/O and ID validation. The
// legacy reader does not reject duplicate names, invalid Unicode or unknown keys.
func BenchmarkReadLockMetadata(b *testing.B) {
	for _, count := range []int{1, 128} {
		lf := New()
		for i := range count {
			name := "Entry" + strconv.Itoa(i)
			lf.Models[name] = &ModelLock{NextID: 4, Fields: map[string]int{"id": 1, "name": 2}, FieldTypes: map[string]string{"id": "Int", "name": "String"}, Reserved: []int{3}}
			lf.APIs[name] = &APILock{ID: i + 1, Params: map[string]int{"id": 1}, ParamTypes: map[string]string{"id": "Int"}, ReturnType: name, Contract: true}
		}
		raw, err := json.Marshal(lf)
		if err != nil {
			b.Fatal(err)
		}
		for _, reader := range []struct {
			name string
			read func([]byte) (*LockFile, error)
		}{
			{"legacy", func(raw []byte) (*LockFile, error) {
				lf := New()
				err := json.Unmarshal(raw, lf)
				return lf, err
			}},
			{"strict", readLock},
		} {
			b.Run(strconv.Itoa(count)+"/"+reader.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					lf, err := reader.read(raw)
					if err != nil || len(lf.Models) != count || len(lf.APIs) != count {
						b.Fatal("invalid benchmark result", err)
					}
				}
			})
		}
	}
}

func BenchmarkValidateIdentifiers(b *testing.B) {
	for _, count := range []int{1, 128} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			lf := New()
			for i := range count {
				name := "Entry" + strconv.Itoa(i)
				lf.Models[name] = &ModelLock{NextID: 4, Fields: map[string]int{"id": 1, "name": 2}, Reserved: []int{3}}
				lf.APIs[name] = &APILock{ID: i + 1, Params: map[string]int{"id": 1}, Reserved: []int{2}}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if err := lf.Validate(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

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
		mustUpdate(b, lf, ff)
	}
}

func BenchmarkUpdateExistingSnapshot(b *testing.B) {
	for _, count := range []int{1, 128} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			lf := New()
			input := []*ast.File{{}}
			for i := range count {
				name := "Entry" + strconv.Itoa(i)
				input[0].Models = append(input[0].Models, &ast.ModelDecl{Name: name, Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}})
				input[0].APIs = append(input[0].APIs, &ast.ApiDecl{Name: "get" + name, Params: []*ast.ParamDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}})
			}
			mustUpdate(b, lf, input)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				mustUpdate(b, lf, input)
			}
		})
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
