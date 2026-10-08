package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestGenerateScanner(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "name", Type: &ast.TypeRef{Name: "String"}},
			{Name: "email", Type: &ast.TypeRef{Name: "String"}},
			{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}},
		},
	}

	var b strings.Builder
	generateScanner(&b, m)
	got := b.String()

	checks := []string{
		"func scanUser(rows pg.Rows) (*User, error)",
		"var user User",
		"fds := rows.FieldDescriptions()",
		"dests := make([]any, len(fds))",
		"switch string(fd.Name)",
		`case "id":`,
		"dests[i] = &user.Id",
		`case "name":`,
		"dests[i] = &user.Name",
		`case "email":`,
		"dests[i] = &user.Email",
		`case "created_at":`,
		"dests[i] = &user.CreatedAt",
		"default:",
		"dests[i] = nil",
		"return &user, rows.Scan(dests...)",
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("missing %q in:\n%s", check, got)
		}
	}
}

func TestGeneratedScannerDiscard(t *testing.T) {
	var generated strings.Builder
	generateScanner(&generated, &ast.ModelDecl{Name: "User", Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}})
	current := generated.String()
	previous := strings.ReplaceAll(strings.Replace(current, "func scanUser(", "func scanUserPrevious(", 1), "dests[i] = nil", "dests[i] = new(any)")
	source := "package generated\nimport pg \"github.com/light-speak/luxo/pkg/lux/pg\"\ntype User struct { Id int64 }\n" + current + previous
	runGeneratedProtocolPackage(t, source, `package generated
import ("fmt"; "testing"; pg "github.com/light-speak/luxo/pkg/lux/pg"; "github.com/jackc/pgx/v5/pgconn")
type discardRows struct { pg.Rows; fields []pgconn.FieldDescription; decoded int }
func (r *discardRows) FieldDescriptions() []pgconn.FieldDescription { return r.fields }
func (r *discardRows) Scan(dest ...any) error {
 *dest[0].(*int64) = 7
 for _, value := range dest[1:] { if value != nil { *value.(*any) = "ignored"; r.decoded++ } }
 return nil
}
func TestDiscard(t *testing.T) {
 rows := &discardRows{fields: []pgconn.FieldDescription{{Name:"id"}, {Name:"ignored"}}}
 user, err := scanUser(rows)
 if err != nil || user.Id != 7 || rows.decoded != 0 { t.Fatalf("%+v %v decoded=%d", user, err, rows.decoded) }
}
var scannerSink *User
func BenchmarkScannerDiscard(b *testing.B) {
 for _, unknown := range []int{0, 1, 16} {
  rows := &discardRows{fields: make([]pgconn.FieldDescription, unknown+1)}
  rows.fields[0].Name = "id"
  for i := 1; i < len(rows.fields); i++ { rows.fields[i].Name = "ignored" }
  for _, previous := range []bool{true, false} {
   scan := scanUser; if previous { scan = scanUserPrevious }
   b.Run(fmt.Sprintf("unknown=%d/previous=%v", unknown, previous), func(b *testing.B) {
    b.ReportAllocs()
    for b.Loop() { var err error; scannerSink, err = scan(rows); if err != nil { b.Fatal(err) } }
   })
  }
 }
}
`)
}

func TestGenerateScannerSkipsComputed(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "Post",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "title", Type: &ast.TypeRef{Name: "String"}},
			{Name: "commentCount", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{}},
		},
	}

	var b strings.Builder
	generateScanner(&b, m)
	got := b.String()

	if strings.Contains(got, "comment_count") {
		t.Errorf("computed field should be skipped:\n%s", got)
	}
	if !strings.Contains(got, `case "id":`) {
		t.Errorf("non-computed field missing:\n%s", got)
	}
}

func TestGenerateScannerFunctionName(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "OrderItem",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		},
	}

	var b strings.Builder
	generateScanner(&b, m)
	got := b.String()

	if !strings.Contains(got, "func scanOrderItem(rows pg.Rows)") {
		t.Errorf("wrong function name:\n%s", got)
	}
	if !strings.Contains(got, "var orderItem OrderItem") {
		t.Errorf("wrong variable name:\n%s", got)
	}
}

func TestExtendScannerSkipsRelationObjects(t *testing.T) {
	files := []*ast.File{{Extends: []*ast.ExtendDecl{{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "name", Type: &ast.TypeRef{Name: "String"}},
			{Name: "tags", Type: &ast.TypeRef{Name: "String", IsList: true}},
			{Name: "team", Type: &ast.TypeRef{Name: "Team", Nullable: true}},
			{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
		},
	}}}}
	var output strings.Builder
	defaultGenerator().generateExtendScanners(&output, files, nil, nil)
	code := output.String()
	for _, column := range []string{"id", "name", "tags"} {
		if strings.Count(code, `case "`+column+`":`) != 1 {
			t.Fatalf("missing or duplicated scalar %s:\n%s", column, code)
		}
	}
	for _, column := range []string{"team", "posts"} {
		if strings.Contains(code, `case "`+column+`":`) {
			t.Fatalf("relation %s included in DB scanner:\n%s", column, code)
		}
	}
}

func TestExtendScannerPreservesEnumColumns(t *testing.T) {
	file := &ast.File{
		Enums:  []*ast.EnumDecl{{Name: "Role", Values: []string{"USER", "ADMIN"}}},
		Models: []*ast.ModelDecl{{Name: "Post", Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}}},
		Extends: []*ast.ExtendDecl{{Name: "User", Fields: []*ast.FieldDecl{
			{Name: "role", Type: &ast.TypeRef{Name: "Role"}},
		}}},
	}
	result := &semantic.Result{Files: []*ast.File{file}}
	code := string(defaultGenerator().generateDBFile(result, "generated", collectEnums(result)))
	if !strings.Contains(code, `case "role":`) || !strings.Contains(code, "dests[i] = &user.Role") {
		t.Fatalf("enum column silently discarded by external scanner:\n%s", code)
	}
}

func TestGeneratedExtendEnumScanner(t *testing.T) {
	extend := &ast.ExtendDecl{Name: "User", Fields: []*ast.FieldDecl{
		{Name: "role", Type: &ast.TypeRef{Name: "Role"}},
		{Name: "optionalRole", Type: &ast.TypeRef{Name: "Role", Nullable: true}},
		{Name: "tags", Type: &ast.TypeRef{Name: "String", IsList: true, Nullable: true}},
	}}
	enums := map[string]bool{"Role": true}
	var source strings.Builder
	source.WriteString("package generated\nimport pg \"github.com/light-speak/luxo/pkg/lux/pg\"\ntype Role string\n")
	g := defaultGenerator()
	g.generateExtendStub(&source, extend, enums)
	g.generateExtendScanners(&source, []*ast.File{{Extends: []*ast.ExtendDecl{extend}}}, nil, enums)
	runGeneratedProtocolPackage(t, source.String(), `package generated
import (
 "errors"
 "testing"
 pg "github.com/light-speak/luxo/pkg/lux/pg"
 "github.com/jackc/pgx/v5/pgconn"
 "github.com/testcontainers/testcontainers-go"
 "github.com/testcontainers/testcontainers-go/modules/postgres"
)
type enumRows struct { pg.Rows; nullable *Role; failure error }
func (r *enumRows) FieldDescriptions() []pgconn.FieldDescription {
 return []pgconn.FieldDescription{{Name:"id"},{Name:"role"},{Name:"optional_role"},{Name:"ignored"}}
}
func (r *enumRows) Scan(values ...any) error {
 if r.failure != nil { return r.failure }
 id, ok := values[0].(*int64); if !ok { return errors.New("missing id destination") }; *id = 7
 role, ok := values[1].(*Role); if !ok { return errors.New("missing enum destination") }; *role = Role("ADMIN")
 optional, ok := values[2].(**Role); if !ok { return errors.New("missing nullable enum destination") }; *optional = r.nullable
 if values[3] != nil { return errors.New("unknown column must be skipped without decoding") }
 return nil
}
func TestEnumScanner(t *testing.T) {
 value := Role("USER")
 for _, nullable := range []*Role{nil, &value} {
  got, err := scanUser(&enumRows{nullable:nullable})
  if err != nil { t.Fatal(err) }
  if got.Id != 7 || got.Role != Role("ADMIN") || got.OptionalRole != nullable { t.Fatalf("lost enum: %+v",got) }
 }
 failure := errors.New("scan failed")
 if _, err := scanUser(&enumRows{failure:failure}); !errors.Is(err,failure) { t.Fatalf("lost scan error: %v",err) }
}
func TestEnumScannerPostgres(t *testing.T) {
 ctx := t.Context()
 container, err := postgres.Run(ctx,"postgres:17-alpine",postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
 testcontainers.CleanupContainer(t, container)
 if err != nil { t.Fatal(err) }
 connection, err := container.ConnectionString(ctx,"sslmode=disable"); if err != nil { t.Fatal(err) }
 db, err := pg.NewDB(ctx,connection); if err != nil { t.Fatal(err) }; defer db.Close()
 value := "USER"
 for _, optional := range []*string{nil, &value} {
  rows, err := pg.QueryRows(ctx,db,scanUser,"SELECT 7::bigint AS id, 'ADMIN'::text AS role, $1::text AS optional_role, 'ignored'::text AS ignored, CASE WHEN $1::text IS NULL THEN NULL::text[] ELSE ARRAY['a','b']::text[] END AS tags",optional)
  if err != nil { t.Fatal(err) }
  if len(rows)!=1 || rows[0].Id!=7 || rows[0].Role!=Role("ADMIN") { t.Fatalf("lost database row: %+v",rows) }
  if optional==nil { if rows[0].OptionalRole!=nil { t.Fatal("NULL enum changed") } } else if rows[0].OptionalRole==nil || string(*rows[0].OptionalRole)!=*optional { t.Fatal("nullable enum lost") }
  if optional==nil { if rows[0].Tags!=nil { t.Fatal("NULL array changed") } } else if len(rows[0].Tags)!=2 || rows[0].Tags[0]!="a" || rows[0].Tags[1]!="b" { t.Fatalf("array lost: %+v",rows[0].Tags) }
 }
}
`)
}
