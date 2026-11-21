package codegen

import (
	"github.com/light-speak/luxo/pkg/ast"
	"slices"
)

type extensionPosition struct{ file, declaration int }
type extensionScope struct{ module, model string }
type extensionGroup struct {
	first       extensionPosition
	declaration *ast.ExtendDecl
	merged      bool
}

// Merge only within a service; preserve cross-service field ownership and source ASTs.
func mergeModuleExtensions(files []*ast.File) []*ast.File {
	var groups map[extensionScope]*extensionGroup
	var skipped map[extensionPosition]bool
	for fi, file := range files {
		for ei, extension := range file.Extends {
			if groups == nil {
				groups = make(map[extensionScope]*extensionGroup)
			}
			key := extensionScope{moduleNameFromFile(file.Name), extension.Name}
			position := extensionPosition{fi, ei}
			group := groups[key]
			if group == nil {
				groups[key] = &extensionGroup{first: position, declaration: extension}
				continue
			}
			if !group.merged {
				copy := *group.declaration
				copy.Fields = slices.Clone(copy.Fields)
				group.declaration = &copy
				group.merged = true
			}
			group.declaration.Fields = append(group.declaration.Fields, extension.Fields...)
			if skipped == nil {
				skipped = make(map[extensionPosition]bool)
			}
			skipped[position] = true
		}
	}
	if len(skipped) == 0 {
		return files
	}
	output := slices.Clone(files)
	for fi, file := range files {
		extensions, changed := mergedFileExtensions(fi, file, groups, skipped)
		if changed {
			copy := *file
			copy.Extends = extensions
			output[fi] = &copy
		}
	}
	return output
}

func mergedFileExtensions(fi int, file *ast.File, groups map[extensionScope]*extensionGroup, skipped map[extensionPosition]bool) ([]*ast.ExtendDecl, bool) {
	var output []*ast.ExtendDecl
	changed := false
	for ei, extension := range file.Extends {
		position := extensionPosition{fi, ei}
		group := groups[extensionScope{moduleNameFromFile(file.Name), extension.Name}]
		omit := skipped[position]
		replace := group.merged && group.first == position
		if !changed && (omit || replace) {
			output = append(make([]*ast.ExtendDecl, 0, len(file.Extends)), file.Extends[:ei]...)
			changed = true
		}
		if !changed {
			continue
		}
		if omit {
			continue
		}
		if replace {
			extension = group.declaration
		}
		output = append(output, extension)
	}
	return output, changed
}
