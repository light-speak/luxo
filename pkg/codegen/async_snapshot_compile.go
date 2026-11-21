package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

func (c *compiler) asyncSnapshotState() *asyncSnapshotGenerator {
	if c.generator == nil {
		c.generator = defaultGenerator()
	}
	generator := c.generator.asyncSnapshotOwner
	if generator == nil {
		generator = c.generator
	}
	if generator.asyncSnapshots == nil {
		generator.asyncSnapshots = generator.newAsyncSnapshots(generator.asyncSource, c.enums)
	}
	return generator.asyncSnapshots
}

func (c *compiler) asyncCaptureNeedsCopy(value valType) bool {
	if value.name == "Channel" && !value.isList {
		return false
	}
	if value.isModel || c.types[value.name] || value.nullable || value.isList || value.name == "Bytes" || value.name == "JSON" {
		return true
	}
	if c.generator.sealedDeclaration(value.name) != nil {
		return true
	}
	if c.generator != nil && c.generator.asyncSource != nil {
		for _, file := range c.generator.asyncSource.Files {
			for _, declaration := range file.Types {
				if declaration.Name == value.name {
					return true
				}
			}
		}
	}
	return false
}

// Evaluate all snapshots synchronously before making the task runnable.
func (c *compiler) prepareAsyncCaptures(body *ast.Block) (string, string, string) {
	var prefix *strings.Builder
	var params, args []string
	state := ""
	for _, name := range capturedAsyncNames(body, c.vars) {
		value := c.vars[name]
		ref := &ast.TypeRef{Name: value.name, Nullable: value.nullable, IsList: value.isList}
		if value.typeRef != nil {
			ref.TypeArgs = value.typeRef.TypeArgs
		}
		goType := goTypeForValType(value)
		if value.isChan {
			goType = compiledFunctionGoType(ref, c.models)
		}
		argument := c.bindings.name(name)
		params = append(params, argument+" "+goType)
		if c.asyncCaptureNeedsCopy(value) {
			if state == "" {
				prefix = new(strings.Builder)
				c.resultTmp++
				state = "_asyncSnapshot" + strconv.Itoa(c.resultTmp)
				fmt.Fprintf(prefix, "%svar %s _luxoAsyncSnapshot\n", c.indent, state)
			}
			c.resultTmp++
			copyName := "_asyncValue" + strconv.Itoa(c.resultTmp)
			copyExpr := strings.TrimPrefix(c.asyncSnapshotState().copy(ref, goType, argument), "s.")
			fmt.Fprintf(prefix, "%s%s := %s.%s\n", c.indent, copyName, state, copyExpr)
			argument = copyName
		}
		args = append(args, argument)
	}
	if state != "" {
		fmt.Fprintf(prefix, "%s%s.finish()\n", c.indent, state)
		return prefix.String(), strings.Join(params, ", "), strings.Join(args, ", ")
	}
	return "", strings.Join(params, ", "), strings.Join(args, ", ")
}
