package semantic

import (
	"path/filepath"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

// ModuleInfo describes which module a file belongs to.
type ModuleInfo struct {
	// Name is the module name, e.g. "user", "post", "common".
	Name string
	// IsCommon is true for the special common/ module whose types are globally visible.
	IsCommon bool
}

// FileModuleMap maps file names (as set in ast.File.Name) to their module info.
type FileModuleMap map[string]*ModuleInfo

// FileToModule derives the module name from a file path.
// Rules:
//   - origin/user.luxo → module "user"
//   - origin/post/model.luxo → module "post"
//   - origin/common/base.luxo → module "common" (IsCommon = true)
//   - anything not under origin/ → module derived from filename stem
func FileToModule(filename string) *ModuleInfo {
	// normalize path separators
	clean := filepath.ToSlash(filepath.Clean(filename))

	// find the origin/ segment
	idx := strings.LastIndex(clean, "origin/")
	if idx == -1 {
		// not under origin/ — derive from filename stem
		base := filepath.Base(filename)
		ext := filepath.Ext(base)
		name := strings.TrimSuffix(base, ext)
		return &ModuleInfo{Name: name}
	}

	// relative path after origin/
	rel := clean[idx+len("origin/"):]

	// check if it's a folder-based module (has subdirectory)
	dir := filepath.Dir(rel)
	if dir != "." && dir != "" {
		// folder-based: origin/post/model.luxo → "post"
		// use the first directory segment as the module name
		parts := strings.SplitN(dir, "/", 2)
		modName := parts[0]
		return &ModuleInfo{
			Name:     modName,
			IsCommon: modName == "common",
		}
	}

	// file-based: origin/user.luxo → "user"
	base := filepath.Base(rel)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	return &ModuleInfo{
		Name:     name,
		IsCommon: name == "common",
	}
}

// BuildFileModuleMap creates a FileModuleMap from a list of AST files.
func BuildFileModuleMap(files []*ast.File) FileModuleMap {
	m := make(FileModuleMap, len(files))
	for _, f := range files {
		m[f.Name] = FileToModule(f.Name)
	}
	return m
}

// typeOwnership tracks which module declared each type name.
type typeOwnership struct {
	// typeName → module name
	owners map[string]string
}

func newTypeOwnership() *typeOwnership {
	return &typeOwnership{owners: make(map[string]string)}
}

// register records that a type was declared in a given module.
func (o *typeOwnership) register(typeName, moduleName string) {
	o.owners[typeName] = moduleName
}

// ownerOf returns the module that owns the given type, or "" if unknown (builtin).
func (o *typeOwnership) ownerOf(typeName string) string {
	return o.owners[typeName]
}
