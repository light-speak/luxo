package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestSharedEnumDefinitionsAndAliases(t *testing.T) {
	declarations := map[string]*ast.EnumDecl{
		"State": {Name: "State", Values: []string{"OPEN", "CLOSED"}},
		"Role":  {Name: "Role", Values: []string{"USER", "ADMIN"}},
	}
	source, err := generateSharedEnumFile(declarations)
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, expected := range []string{"package luxotypes", "type Role string", "type State string", "func (v Role) IsValid() bool", "func (v State) AppendLuxo"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %q in shared definitions", expected)
		}
	}
	if strings.Index(text, "type Role") > strings.Index(text, "type State") {
		t.Fatal("shared declarations are not deterministically sorted")
	}
	var aliases strings.Builder
	generateEnumAlias(&aliases, declarations["Role"])
	for _, expected := range []string{"type Role = luxotypes.Role", "RoleUSER = luxotypes.RoleUSER", "RoleADMIN = luxotypes.RoleADMIN"} {
		if !strings.Contains(aliases.String(), expected) {
			t.Fatalf("missing static alias %q", expected)
		}
	}
	if strings.Contains(aliases.String(), "func ") {
		t.Fatal("aliases must not duplicate methods")
	}
	if empty, err := generateSharedEnumFile(nil); err != nil || empty != nil {
		t.Fatalf("empty shared package = %q, %v", empty, err)
	}
}

func TestGeneratedSharedEnumMethods(t *testing.T) {
	source, err := generateSharedEnumFile(map[string]*ast.EnumDecl{
		"Role": {Name: "Role", Values: []string{"USER", "ADMIN"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	runGeneratedProtocolPackage(t, strings.Replace(string(source), "package luxotypes", "package generated", 1), `package generated
import("testing";"bytes";"errors";"github.com/light-speak/luxo/pkg/lux/codec")
func TestCanonicalEnum(t *testing.T) {
 if !RoleUSER.IsValid() || Role("OTHER").IsValid() {t.Fatal("enum validation changed")}
 want,err:=codec.AppendString(nil,"USER"); if err!=nil {t.Fatal(err)}
 got,err:=RoleUSER.AppendLuxo(nil); if err!=nil||!bytes.Equal(got,want){t.Fatalf("wire = %x, %v",got,err)}
 prefix:=[]byte{7,8};got,err=Role("OTHER").AppendLuxo(prefix)
 if !errors.Is(err,codec.ErrInvalidEnum)||!bytes.Equal(got,prefix){t.Fatal("invalid enum altered output")}
}
`)
}

func TestSharedEnumDependenciesPreserveInputAndOwnership(t *testing.T) {
	local := &ast.EnumDecl{Name: "Local", Values: []string{"HERE"}}
	file := &ast.File{Name: "origin/user/model.luxo", Enums: []*ast.EnumDecl{local}, Models: []*ast.ModelDecl{{
		Name: "User", Fields: []*ast.FieldDecl{{Name: "role", Type: &ast.TypeRef{Name: "Role", Nullable: true}}},
	}}}
	input := &semantic.Result{Files: []*ast.File{file}}
	generator := mustNewGenerator(t, GeneratorConfig{Events: &EventContext{
		SharedEnumPackage: "example.com/app/internal/luxotypes",
		EnumModule:        map[string]string{"Role": "common", "Unused": "other", "Local": "user"},
		Enums: map[string]*ast.EnumDecl{
			"Role":   {Name: "Role", Values: []string{"USER"}},
			"Unused": {Name: "Unused", Values: []string{"NO"}}, "Local": local,
		},
	}})
	output := generator.withEnumDependencies(input)
	if len(input.Files) != 1 || len(file.Enums) != 1 {
		t.Fatal("dependency registration mutated source files")
	}
	if len(output.Files) != 2 || output.Files[1].Enums[0].Name != "Role" || moduleNameFromFile(output.Files[1].Name) != "common" {
		t.Fatalf("incorrect enum dependency files: %#v", output.Files)
	}
	if repeated := generator.withEnumDependencies(output); repeated != output {
		t.Fatal("enum dependency registration is not idempotent")
	}
}

func TestEnumReferenceDeclarationPaths(t *testing.T) {
	ref := &ast.TypeRef{Name: "Role"}
	value := &ast.Ident{Name: "Role"}
	body := &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: value}}}
	params := []*ast.ParamDecl{{Type: ref, Default: value}}
	directives := []*ast.Directive{{Args: []*ast.NamedArg{{Value: value}}, Body: body}}
	fields := []*ast.FieldDecl{{Type: ref, Default: value, Directives: directives}}
	fn := &ast.FnDecl{Params: params, ReturnType: ref, Body: body, Directives: directives}
	cases := map[string]*ast.File{
		"model":      {Models: []*ast.ModelDecl{{Fields: fields, Directives: directives, Scopes: []*ast.ScopeDecl{{Params: params, Expr: value}}}}},
		"extend":     {Extends: []*ast.ExtendDecl{{Fields: fields}}},
		"type":       {Types: []*ast.TypeDecl{{Fields: fields}}},
		"api":        {APIs: []*ast.ApiDecl{{Params: params, ReturnType: ref, Body: body, Directives: directives}}},
		"function":   {Functions: []*ast.FnDecl{fn}},
		"event":      {Events: []*ast.EventDecl{{Params: params}}},
		"error":      {Errors: []*ast.ErrorDecl{{Fields: params}}},
		"interface":  {Interfaces: []*ast.InterfaceDecl{{Fields: fields, Methods: []*ast.FnDecl{fn}}}},
		"sealed":     {Sealeds: []*ast.SealedDecl{{Variants: []*ast.SealedVariant{{Fields: params}}}}},
		"listener":   {Listeners: []*ast.OnDecl{{Body: body}}},
		"middleware": {Middlewares: []*ast.MiddlewareDecl{{Body: body, Directives: directives}}},
		"global":     {Globals: []*ast.ValStmt{{Type: ref, Value: value}}},
		"generic":    {Globals: []*ast.ValStmt{{Type: &ast.TypeRef{Name: "Box", TypeArgs: []*ast.TypeRef{ref}}}}},
		"tuple":      {Globals: []*ast.ValStmt{{Type: &ast.TypeRef{Tuple: []*ast.TypeRef{ref}}}}},
	}
	for name, file := range cases {
		t.Run(name, func(t *testing.T) {
			references := enumReferences{names: make(map[string]bool)}
			references.file(file)
			if !references.names["Role"] {
				t.Fatal("enum reference was omitted")
			}
		})
	}
	references := enumReferences{names: make(map[string]bool)}
	references.typeRef(nil)
	literal := &ast.Literal{}
	literal.SetTypeTag("TaggedEnum")
	references.expr(literal)
	if !references.names["TaggedEnum"] {
		t.Fatal("resolved enum tag was omitted")
	}
}

func TestLocalEnumAnnotationDependenciesAfterAnalysis(t *testing.T) {
	for _, declaration := range []string{"val role: Role? = null", "val roles: [Role] = []"} {
		t.Run(declaration, func(t *testing.T) {
			const filename = "origin/user/api.luxo"
			tokens, lexErrors := lexer.New("api check(): Boolean {\n"+declaration+"\nreturn true\n}", filename).Tokenize()
			if len(lexErrors) != 0 {
				t.Fatal(lexErrors)
			}
			file, parseErrors := parser.New(tokens).Parse(filename)
			if len(parseErrors) != 0 {
				t.Fatal(parseErrors)
			}
			shared := &ast.File{Name: "origin/common/types.luxo", Enums: []*ast.EnumDecl{{Name: "Role", Values: []string{"USER"}}}}
			result := semantic.Analyze([]*ast.File{shared, file})
			if len(result.Errors) != 0 {
				t.Fatal(result.Errors)
			}
			context := BuildEventContext(result.Files, "example.com/app")
			context.SharedEnumPackage = "example.com/app/internal/luxotypes"
			generator := mustNewGenerator(t, GeneratorConfig{Events: context})
			local := &semantic.Result{Files: []*ast.File{file}}
			resolved := generator.withEnumDependencies(local)
			if !collectEnums(resolved)["Role"] || len(local.Files) != 1 {
				t.Fatal("local annotation lost its shared enum dependency or mutated input")
			}
		})
	}
}

func TestSharedEnumEmitterConfiguration(t *testing.T) {
	if source, err := defaultGenerator().GenerateSharedEnums(); source != nil || err != nil {
		t.Fatalf("empty config: %q %v", source, err)
	}
	generator := mustNewGenerator(t, GeneratorConfig{Events: &EventContext{Enums: map[string]*ast.EnumDecl{"Role": {Name: "Role", Values: []string{"USER"}}, "nil": nil}}})
	if _, exists := generator.events.Enums["nil"]; exists {
		t.Fatal("nil declaration was retained")
	}
	if _, err := generator.GenerateSharedEnums(); err == nil {
		t.Fatal("missing shared package accepted")
	}
	generator.events.SharedEnumPackage = "example.com/app/internal/luxotypes"
	if source, err := generator.GenerateSharedEnums(); err != nil || !strings.Contains(string(source), "type Role string") {
		t.Fatalf("shared output: %q %v", source, err)
	}
	var alias strings.Builder
	generateEnumAlias(&alias, &ast.EnumDecl{Name: "Empty"})
	if strings.Contains(alias.String(), "const") {
		t.Fatal("empty enum emitted an empty constant group")
	}
}

func TestGenerateWithSharedEnumDependencies(t *testing.T) {
	input := &semantic.Result{Files: []*ast.File{{Name: "origin/user/model.luxo", Models: []*ast.ModelDecl{{
		Name: "User", Directives: []*ast.Directive{{Name: "noTime"}},
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}}},
			{Name: "role", Type: &ast.TypeRef{Name: "Role"}},
		},
	}}}}}
	context := &EventContext{
		SharedEnumPackage: "example.com/app/internal/luxotypes",
		EnumModule:        map[string]string{"Role": "common"},
		Enums:             map[string]*ast.EnumDecl{"Role": {Name: "Role", Values: []string{"USER", "ADMIN"}}},
	}
	generator := mustNewGenerator(t, GeneratorConfig{Events: context, IDs: StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "role": 2}}}})
	output, err := generator.Generate(input, "generated", nil)
	if err != nil {
		t.Fatal(err)
	}
	model := string(output.Files["model.gen.go"])
	if !strings.Contains(model, "type Role = luxotypes.Role") || !strings.Contains(model, `"example.com/app/internal/luxotypes"`) {
		t.Fatalf("missing canonical alias/import: %s", model)
	}
	if strings.Contains(model, "pkg/lux/codec") {
		t.Fatal("alias-only enums do not need codec imports")
	}
	schema := string(output.Files["schema.gen.go"])
	if !strings.Contains(schema, `Name: "Role", Module: "common"`) || !strings.Contains(schema, `"USER", "ADMIN"`) {
		t.Fatalf("missing enum metadata: %s", schema)
	}
	if len(input.Files) != 1 {
		t.Fatal("generation changed input files")
	}
}

func TestGenerateEnum(t *testing.T) {
	tests := []struct {
		name   string
		enum   *ast.EnumDecl
		expect string
	}{
		{
			name: "basic enum",
			enum: &ast.EnumDecl{
				Name:   "Role",
				Values: []string{"USER", "ADMIN", "MODERATOR"},
			},
			expect: `type Role string

const (
	RoleUSER      Role = "USER"
	RoleADMIN     Role = "ADMIN"
	RoleMODERATOR Role = "MODERATOR"
)

// IsValid reports whether the value is declared by this service's schema.
func (v Role) IsValid() bool {
	switch v {
	case RoleUSER, RoleADMIN, RoleMODERATOR:
		return true
	}
	return false
}

// AppendLuxo encodes a declared enum member, leaving dst unchanged on failure.
func (v Role) AppendLuxo(dst []byte) ([]byte, error) {
	if !v.IsValid() { return dst, codec.ErrInvalidEnum }
	return codec.AppendString(dst, string(v))
}
`,
		},
		{
			name: "single value enum",
			enum: &ast.EnumDecl{
				Name:   "Status",
				Values: []string{"ACTIVE"},
			},
			expect: `type Status string

const (
	StatusACTIVE Status = "ACTIVE"
)

// IsValid reports whether the value is declared by this service's schema.
func (v Status) IsValid() bool {
	switch v {
	case StatusACTIVE:
		return true
	}
	return false
}

// AppendLuxo encodes a declared enum member, leaving dst unchanged on failure.
func (v Status) AppendLuxo(dst []byte) ([]byte, error) {
	if !v.IsValid() { return dst, codec.ErrInvalidEnum }
	return codec.AppendString(dst, string(v))
}
`,
		},
		{
			name: "order status enum",
			enum: &ast.EnumDecl{
				Name:   "OrderStatus",
				Values: []string{"PENDING", "PAID", "SHIPPED", "COMPLETED", "CANCELLED"},
			},
			expect: `type OrderStatus string

const (
	OrderStatusPENDING   OrderStatus = "PENDING"
	OrderStatusPAID      OrderStatus = "PAID"
	OrderStatusSHIPPED   OrderStatus = "SHIPPED"
	OrderStatusCOMPLETED OrderStatus = "COMPLETED"
	OrderStatusCANCELLED OrderStatus = "CANCELLED"
)

// IsValid reports whether the value is declared by this service's schema.
func (v OrderStatus) IsValid() bool {
	switch v {
	case OrderStatusPENDING, OrderStatusPAID, OrderStatusSHIPPED, OrderStatusCOMPLETED, OrderStatusCANCELLED:
		return true
	}
	return false
}

// AppendLuxo encodes a declared enum member, leaving dst unchanged on failure.
func (v OrderStatus) AppendLuxo(dst []byte) ([]byte, error) {
	if !v.IsValid() { return dst, codec.ErrInvalidEnum }
	return codec.AppendString(dst, string(v))
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			generateEnum(&b, tt.enum)
			got := b.String()
			if got != tt.expect {
				t.Errorf("generateEnum mismatch:\ngot:\n%s\nexpect:\n%s", got, tt.expect)
			}
		})
	}
}
