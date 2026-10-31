package inferred

import (
	"strconv"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

var benchmarkModelsSink map[string]*ast.ModelDecl

func BenchmarkModelIndex(b *testing.B) {
	for _, count := range []int{16, 128} {
		for _, inherited := range []bool{false, true} {
			b.Run(strconv.Itoa(count)+"/inherited="+strconv.FormatBool(inherited), func(b *testing.B) {
				files := benchmarkModelFiles(count, inherited)
				b.ReportAllocs()
				for b.Loop() {
					benchmarkModelsSink = Models(files)
				}
			})
		}
	}
}

var benchmarkFilesSink []*ast.File

func BenchmarkExpandedModels(b *testing.B) {
	for _, count := range []int{16, 128} {
		for _, prepared := range []bool{false, true} {
			b.Run(strconv.Itoa(count)+"/prepared="+strconv.FormatBool(prepared), func(b *testing.B) {
				files := benchmarkModelFiles(count, true)
				if prepared {
					files = ExpandedFiles(files)
				}
				b.ReportAllocs()
				for b.Loop() {
					benchmarkFilesSink = ExpandedFiles(files)
				}
			})
		}
	}
}

func benchmarkModelFiles(count int, inherited bool) []*ast.File {
	base := &ast.ModelDecl{Name: "Base", Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}}
	file := &ast.File{Models: []*ast.ModelDecl{base}}
	for i := range count {
		model := &ast.ModelDecl{Name: "Model" + strconv.Itoa(i), Fields: []*ast.FieldDecl{{Name: "name", Type: &ast.TypeRef{Name: "String"}}}}
		if inherited {
			model.Parents = []string{"Base"}
		}
		file.Models = append(file.Models, model)
	}
	return []*ast.File{file}
}
