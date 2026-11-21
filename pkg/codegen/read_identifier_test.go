package codegen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestGeneratedArrayReaderReceiverNames(t *testing.T) {
	var source, assertions strings.Builder
	source.WriteString(`package generated
import (
    "encoding/json"
    "time"
    "github.com/google/uuid"
    "github.com/shopspring/decimal"
    "github.com/light-speak/luxo/pkg/lux/codec"
)
`)
	generateEnum(&source, &ast.EnumDecl{Name: "State", Values: []string{"OPEN", "CLOSED"}})
	assertions.WriteString("package generated\nimport (\"testing\"; \"github.com/light-speak/luxo/pkg/lux/codec\")\n")
	for _, name := range []string{"Input", "Value"} {
		model := &ast.ModelDecl{Name: name}
		ids := make(map[string]int)
		for index, typ := range []string{"JSON", "Duration", "UUID", "Decimal", "State"} {
			field := strings.ToLower(typ)
			model.Fields = append(model.Fields, &ast.FieldDecl{Name: field, Type: &ast.TypeRef{Name: typ, IsList: true}})
			ids[field] = index + 1
		}
		model.Fields = append(model.Fields, &ast.FieldDecl{Name: "optional", Type: &ast.TypeRef{Name: "State", Nullable: true}})
		ids["optional"] = 6
		g := generatorWithModelFieldIDs(map[string]map[string]int{name: ids})
		enums := map[string]bool{"State": true}
		generateModel(&source, model, enums)
		g.generateReadLuxo(&source, model, enums)
		g.generateReadColumnar(&source, model, enums)
		fmt.Fprintf(&assertions, `func Test%s(t *testing.T) {
    var value %s
    dec:=codec.NewDecoder([]byte{0,1,1,1,'0',0})
    value.ReadLuxoInput(dec)
    if err:=dec.Finish(); err!=nil || len(value.Json)!=1 || string(value.Json[0])!="0" {t.Fatalf("read: %%#v %%v",value,err)}
}
`, name, name)
	}
	runGeneratedProtocolPackage(t, source.String(), assertions.String())
}
