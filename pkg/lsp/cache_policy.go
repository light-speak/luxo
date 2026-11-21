package lsp

import (
	"path/filepath"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/cachepolicy"
	"github.com/light-speak/luxo/pkg/semantic"
)

// documentProjectRoot follows the same project/origin layout as the CLI. A
// standalone document uses its own directory, never the language server's cwd.
func documentProjectRoot(path string) string {
	start := filepath.Dir(path)
	for dir := start; ; dir = filepath.Dir(dir) {
		if filepath.Base(dir) == "origin" {
			return filepath.Dir(dir)
		}
		if parent := filepath.Dir(dir); parent == dir {
			return start
		}
	}
}

// analyzeProjects keeps catalogs, symbols and dependency analysis isolated
// across projects. Each pass reads the current catalog, including deletions.
func analyzeProjects(files []*ast.File) map[*ast.File]*semantic.Result {
	projects := make(map[string][]*ast.File)
	for _, file := range files {
		root := documentProjectRoot(file.Name)
		projects[root] = append(projects[root], file)
	}
	results := make(map[*ast.File]*semantic.Result, len(files))
	for root, projectFiles := range projects {
		policies, err := cachepolicy.Load(filepath.Join(root, cachepolicy.Filename))
		result := semantic.AnalyzeWithOptions(projectFiles, semantic.Options{ModuleIsolation: true, CachePolicies: policies})
		if err != nil {
			// Empty position makes the project configuration error visible in
			// each affected source document, not in unrelated projects.
			result.Errors = append(result.Errors, semantic.Error{Message: err.Error()})
		}
		for _, file := range projectFiles {
			results[file] = result
		}
	}
	return results
}
