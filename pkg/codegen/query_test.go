package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestGenerateQueryBuilderUsesDeclaredPrimaryKey(t *testing.T) {
	model := &ast.ModelDecl{Name: "Product", Fields: []*ast.FieldDecl{
		{Name: "sku", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "id"}}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
	}}
	var b strings.Builder
	defaultGenerator().generateQueryBuilder(&b, model, map[string]bool{})
	code := b.String()
	for _, want := range []string{
		"Find(ctx context.Context, id string)",
		"ProductWhere.Sku.Eq(id)",
		`IDColumn: "\"sku\""`,
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("custom primary key output missing %q:\n%s", want, code)
		}
	}
}

func TestNoTimePreservesExplicitTimestampInputs(t *testing.T) {
	model := noTimeInputModel()
	var output strings.Builder
	defaultGenerator().generateQueryBuilder(&output, model, nil)
	code := output.String()
	for _, fragment := range []string{"SetCreatedAt(v time.Time)", "SetUpdatedAt(v time.Time)", "CreatedAt time.Time", "UpdatedAt time.Time", "r.CreatedAt", "r.UpdatedAt"} {
		if !strings.Contains(code, fragment) {
			t.Errorf("@noTime lost explicit timestamp input %q", fragment)
		}
	}
	if strings.Contains(code, "time.Now()") {
		t.Error("@noTime generated automatic timestamp values")
	}
	for _, update := range []bool{false, true} {
		fields := mutationInputFields(model, nil, update)
		if len(fields) != 2 || fields[0].field.Name != "createdAt" || fields[1].field.Name != "updatedAt" {
			t.Errorf("@noTime CRUD inputs missing: update=%v, fields=%v", update, fields)
		}
	}
}

func noTimeInputModel() *ast.ModelDecl {
	return &ast.ModelDecl{Name: "Audit", Directives: []*ast.Directive{{Name: "crud"}, {Name: "noTime"}}, Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}, {Name: "auto"}}},
		{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}},
		{Name: "updatedAt", Type: &ast.TypeRef{Name: "DateTime"}},
	}}
}

func TestNoTimeKeepsAutomaticIdentifiers(t *testing.T) {
	model := noTimeInputModel()
	model.Fields[0].Type.Name = "UUID"
	fills := defaultGenerator().collectAutoFills(model.Name, model.Fields, "b", model.Directives)
	columns := collectAutoCols(model.Fields, model.Directives)
	if len(fills) != 1 || !strings.Contains(fills[0], "uuid.NewV7()") {
		t.Fatalf("@noTime changed automatic UUID generation: %v", fills)
	}
	if len(columns) != 1 || columns[0].col != "id" || columns[0].expr != "uuid.NewV7()" {
		t.Fatalf("@noTime changed batch UUID generation: %v", columns)
	}
	if fields := collectUserFields(model.Fields, model.Directives); len(fields) != 2 {
		t.Fatalf("automatic UUID leaked into batch input: %v", fields)
	}
}

func TestGeneratedNoTimeTimestampContract(t *testing.T) {
	input := result(&ast.File{Models: []*ast.ModelDecl{noTimeInputModel()}})
	config := GeneratorConfig{Driver: DriverPG, IDs: completeFixtureAPIIDs(input, StableIDs{
		ModelFields: map[string]map[string]int{"Audit": {"id": 1, "createdAt": 2, "updatedAt": 3}},
	})}
	generated, err := Generate(input, "generated", config)
	if err != nil {
		t.Fatal(err)
	}
	dir := writeProtocolFixture(t, "package generated\n", noTimeTimestampAssertions)
	for name, data := range generated.Files {
		if filepath.Base(name) != name {
			t.Fatalf("unexpected generated path %q", name)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("go", "test", "-count=1", "-timeout=60s", ".")
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated @noTime contract: %v\n%s", err, output)
	}
}

const noTimeTimestampAssertions = `package generated
import ("testing";"time";"github.com/light-speak/luxo/pkg/lux/schema")
func TestNoTimeTimestampContract(t *testing.T) {
  first:=time.Unix(-1,123456789).UTC(); second:=time.Unix(1776427200,987654321).UTC()
  create:=(&AuditClient{}).Create().SetCreatedAt(first).SetUpdatedAt(second)
  if len(create.Cols)!=2 || create.Cols[0]!="\"created_at\"" || create.Cols[1]!="\"updated_at\"" || create.Vals[0]!=first || create.Vals[1]!=second {t.Fatalf("create replaced times: %+v",create)}
  update:=newAuditUpdateBuilder(nil,"audits",1).SetCreatedAt(first).SetUpdatedAt(second)
  if len(update.Sets)!=2 || update.Sets[0].Val!=first || update.Sets[1].Val!=second {t.Fatalf("update replaced times: %+v",update)}
  many:=(&AuditClient{}).CreateMany().Add(AuditCreateInput{CreatedAt:first,UpdatedAt:second})
  if len(many.rows)!=1 || many.rows[0].CreatedAt!=first || many.rows[0].UpdatedAt!=second {t.Fatal("batch changed times")}
  catalog:=schema.New(); if err:=RegisterSchema(catalog);err!=nil {t.Fatal(err)}
  for _,name:=range []string{"createAudit","updateAudit"} {
    declaration:=catalog.APIs[name];if declaration==nil {t.Fatalf("missing %s",name)}
    found:=0
    for _,param:=range declaration.Params {if param.Name=="createdAt" || param.Name=="updatedAt" {found++;if param.Type!=schema.FieldDateTime {t.Fatal("timestamp input changed type")}}}
    if found!=2 {t.Fatalf("SDK schema lost timestamp inputs for %s: %+v",name,declaration.Params)}
  }
}
`

func TestGenerateClient(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
	}
	var b strings.Builder
	defaultGenerator().generateClient(&b, "User", "users", fields, false)
	got := b.String()

	checks := []string{
		"type UserClient struct",
		"db *pg.DB",
		"func (c *UserClient) Find(ctx context.Context, id int64) (*User, error)",
		"UserWhere.Id.Eq(id)",
		"func (c *UserClient) Where(conds ...lux.Condition) *UserQuery",
		"pg.NewQuery(c.db,",
		"scanUser",
		"func (c *UserClient) Create() *UserCreateBuilder",
		"pg.CreateBase[User]",
		"func (c *UserClient) CreateMany() *UserCreateManyBuilder",
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("missing %q in:\n%s", check, got)
		}
	}
}

func TestGenerateFieldConstants(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}},
		{Name: "commentCount", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{}},
	}

	var b strings.Builder
	defaultGenerator().generateFieldConstants(&b, "User", fields)
	got := b.String()

	checks := []string{
		"var UserFields = struct {",
		"Id string",
		"Name string",
		"CreatedAt string",
		`Id: "\"id\""`,
		`Name: "\"name\""`,
		`CreatedAt: "\"created_at\""`,
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("missing %q in:\n%s", check, got)
		}
	}

	if strings.Contains(got, "CommentCount") {
		t.Errorf("computed field should be skipped:\n%s", got)
	}
}

func TestGenerateWhereFields(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		{Name: "score", Type: &ast.TypeRef{Name: "Float"}},
		{Name: "active", Type: &ast.TypeRef{Name: "Boolean"}},
		{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}},
		{Name: "role", Type: &ast.TypeRef{Name: "Role"}}, // enum → StringField
	}

	var b strings.Builder
	defaultGenerator().generateWhereFields(&b, "User", fields)
	got := b.String()

	checks := []string{
		"var UserWhere = struct {",
		"Id lux.IntField",
		"Name lux.StringField",
		"Score lux.FloatField",
		"Active lux.BoolField",
		"CreatedAt lux.TimeField",
		"Role lux.StringField",
		`Id: lux.NewIntField("\"id\"")`,
		`Name: lux.NewStringField("\"name\"")`,
		`Score: lux.NewFloatField("\"score\"")`,
		`Active: lux.NewBoolField("\"active\"")`,
		`CreatedAt: lux.NewTimeField("\"created_at\"")`,
		`Role: lux.NewStringField("\"role\"")`,
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("missing %q in:\n%s", check, got)
		}
	}
}

func TestGenerateWhereFieldsSkipsComputed(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "commentCount", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{}},
	}

	var b strings.Builder
	defaultGenerator().generateWhereFields(&b, "Post", fields)
	got := b.String()

	if strings.Contains(got, "CommentCount") {
		t.Errorf("computed field should be skipped:\n%s", got)
	}
	if !strings.Contains(got, "Id lux.IntField") {
		t.Errorf("non-computed field missing:\n%s", got)
	}
}

func TestGenerateCreateBuilder(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		{Name: "email", Type: &ast.TypeRef{Name: "String"}},
		{Name: "internal", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "internal"}}},
		{Name: "commentCount", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{}},
	}

	var b strings.Builder
	defaultGenerator().generateCreateBuilder(&b, "User", fields, nil)
	got := b.String()

	checks := []string{
		"type UserCreateBuilder struct",
		"pg.CreateBase[User]",
		"func (b *UserCreateBuilder) SetId(v int64) *UserCreateBuilder",
		"func (b *UserCreateBuilder) SetName(v string) *UserCreateBuilder",
		"func (b *UserCreateBuilder) SetEmail(v string) *UserCreateBuilder",
		`b.Set("\"name\"", v)`,
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("missing %q in:\n%s", check, got)
		}
	}

	// no Exec method — inherited from CreateBase
	if strings.Contains(got, "func (b *UserCreateBuilder) Exec") {
		t.Errorf("Exec should be inherited, not generated:\n%s", got)
	}

	if strings.Contains(got, "SetInternal") {
		t.Errorf("@internal field should be skipped:\n%s", got)
	}
	if strings.Contains(got, "SetCommentCount") {
		t.Errorf("computed field should be skipped:\n%s", got)
	}
}

func TestGenerateCreateManyBuilder(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		{Name: "email", Type: &ast.TypeRef{Name: "String"}},
		{Name: "internal", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "internal"}}},
		{Name: "commentCount", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{}},
	}

	var b strings.Builder
	defaultGenerator().generateCreateManyBuilder(&b, "User", fields)
	got := b.String()

	checks := []string{
		"type UserCreateInput struct",
		"Id int64",
		"Name string",
		"Email string",
		"type UserCreateManyBuilder struct",
		"rows  []UserCreateInput",
		"func (b *UserCreateManyBuilder) Add(input UserCreateInput) *UserCreateManyBuilder",
		"func (b *UserCreateManyBuilder) Exec(ctx context.Context) ([]*User, error)",
		"pg.InsertManyReturning(ctx, b.db, scanUser",
		`"\"id\"", "\"name\"", "\"email\""`,
		"r.Id, r.Name, r.Email,",
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("missing %q in:\n%s", check, got)
		}
	}

	if strings.Contains(got, "Internal string") {
		t.Errorf("@internal field should be skipped in input struct:\n%s", got)
	}
}

func TestGenerateUpdateBuilder(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "immutable"}}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		{Name: "email", Type: &ast.TypeRef{Name: "String"}},
		{Name: "internal", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "internal"}}},
		{Name: "commentCount", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{}},
	}

	var b strings.Builder
	defaultGenerator().generateUpdateBuilder(&b, "User", fields)
	got := b.String()

	checks := []string{
		"type UserUpdateBuilder struct",
		"pg.UpdateBase[User, int64]",
		"func (b *UserUpdateBuilder) SetName(v string) *UserUpdateBuilder",
		"func (b *UserUpdateBuilder) SetEmail(v string) *UserUpdateBuilder",
		`b.Set("\"name\"", v)`,
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("missing %q in:\n%s", check, got)
		}
	}

	if strings.Contains(got, "SetId") {
		t.Errorf("@immutable field should be skipped:\n%s", got)
	}
	if strings.Contains(got, "SetInternal") {
		t.Errorf("@internal field should be skipped:\n%s", got)
	}
	if strings.Contains(got, "SetCommentCount") {
		t.Errorf("computed field should be skipped:\n%s", got)
	}
}

func TestGenerateQueryBuilder(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "name", Type: &ast.TypeRef{Name: "String"}},
			{Name: "email", Type: &ast.TypeRef{Name: "String"}},
		},
	}

	var b strings.Builder
	defaultGenerator().generateQueryBuilder(&b, m, nil)
	got := b.String()

	sections := []string{
		"scanUser",
		"UserClient",
		"UserFields",
		"UserWhere",
		"UserCreateBuilder",
		"UserUpdateBuilder",
	}
	for _, section := range sections {
		if !strings.Contains(got, section) {
			t.Errorf("missing section %q in:\n%s", section, got)
		}
	}

	if !strings.Contains(got, `"\"users\""`) {
		t.Errorf("missing table name 'users' in:\n%s", got)
	}
}

func TestGenerateQueryBuilderTableName(t *testing.T) {
	tests := []struct {
		modelName string
		tableName string
	}{
		{"User", "users"},
		{"Order", "orders"},
		{"OrderItem", "order_items"},
	}

	for _, tt := range tests {
		t.Run(tt.modelName, func(t *testing.T) {
			m := &ast.ModelDecl{
				Name: tt.modelName,
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
				},
			}
			var b strings.Builder
			defaultGenerator().generateQueryBuilder(&b, m, nil)
			got := b.String()
			if !strings.Contains(got, `\"`+tt.tableName+`\"`) {
				t.Errorf("expected table name %q in:\n%s", tt.tableName, got)
			}
		})
	}
}

func TestFieldConditionType(t *testing.T) {
	tests := []struct {
		typeRef *ast.TypeRef
		expect  string
	}{
		{&ast.TypeRef{Name: "Int"}, "IntField"},
		{&ast.TypeRef{Name: "Float"}, "FloatField"},
		{&ast.TypeRef{Name: "String"}, "StringField"},
		{&ast.TypeRef{Name: "Boolean"}, "BoolField"},
		{&ast.TypeRef{Name: "DateTime"}, "TimeField"},
		{&ast.TypeRef{Name: "Role"}, "StringField"},    // enum → string comparison
		{&ast.TypeRef{Name: "Unknown"}, "StringField"}, // unknown → string fallback
		{nil, "StringField"},                           // nil → string fallback
	}

	for _, tt := range tests {
		name := "nil"
		if tt.typeRef != nil {
			name = tt.typeRef.Name
		}
		t.Run(name, func(t *testing.T) {
			got := fieldConditionType(tt.typeRef)
			if got != tt.expect {
				t.Errorf("fieldConditionType = %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestGenerateCreateBuilderWithTypes(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "score", Type: &ast.TypeRef{Name: "Float"}},
		{Name: "active", Type: &ast.TypeRef{Name: "Boolean"}},
		{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}},
		{Name: "bio", Type: &ast.TypeRef{Name: "String", Nullable: true}},
	}

	var b strings.Builder
	defaultGenerator().generateCreateBuilder(&b, "Post", fields, nil)
	got := b.String()

	checks := []string{
		"SetId(v int64)",
		"SetScore(v float64)",
		"SetActive(v bool)",
		"SetBio(v *string)",
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("missing %q in:\n%s", check, got)
		}
	}

	// createdAt is auto-managed, no Set method
	if strings.Contains(got, "SetCreatedAt") {
		t.Errorf("createdAt should be auto-managed, no Set method:\n%s", got)
	}
	// but Exec should auto-fill it
	if !strings.Contains(got, `"\"created_at\"", time.Now()`) {
		t.Errorf("missing auto-fill for created_at:\n%s", got)
	}
}

func TestGenerateCreateBuilderAutoUUID(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "UUID"}, Directives: []*ast.Directive{{Name: "auto"}}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}},
	}

	var b strings.Builder
	defaultGenerator().generateCreateBuilder(&b, "Token", fields, nil)
	got := b.String()

	// No SetId — @auto UUID
	if strings.Contains(got, "SetId") {
		t.Errorf("@auto UUID should not generate SetId:\n%s", got)
	}
	// Auto-fill UUID
	if !strings.Contains(got, "uuid.NewV7()") {
		t.Errorf("missing uuid.NewV7() auto-fill:\n%s", got)
	}
	// SetName should exist
	if !strings.Contains(got, "SetName") {
		t.Errorf("missing SetName:\n%s", got)
	}
}

func TestGenerateCreateBuilderSerial(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "serial"}}},
		{Name: "title", Type: &ast.TypeRef{Name: "String"}},
	}

	var b strings.Builder
	defaultGenerator().generateCreateBuilder(&b, "Post", fields, nil)
	got := b.String()

	// No SetId — @serial
	if strings.Contains(got, "SetId") {
		t.Errorf("@serial should not generate SetId:\n%s", got)
	}
	// SetTitle should exist
	if !strings.Contains(got, "SetTitle") {
		t.Errorf("missing SetTitle:\n%s", got)
	}
}

func TestGenerateUpdateBuilderAutoUpdatedAt(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "immutable"}}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		{Name: "updatedAt", Type: &ast.TypeRef{Name: "DateTime"}},
	}

	var b strings.Builder
	defaultGenerator().generateUpdateBuilder(&b, "User", fields)
	got := b.String()

	// No SetUpdatedAt
	if strings.Contains(got, "SetUpdatedAt") {
		t.Errorf("updatedAt should be auto-managed:\n%s", got)
	}
	// Exec auto-fills updated_at
	if !strings.Contains(got, `"\"updated_at\"", time.Now()`) {
		t.Errorf("missing auto-fill for updated_at:\n%s", got)
	}
}

func TestLuxImportConstant(t *testing.T) {
	if luxImport != "github.com/light-speak/luxo/pkg/lux" {
		t.Errorf("luxImport = %q, want github.com/light-speak/luxo/pkg/lux", luxImport)
	}
}

func TestGenerateClientSoftDelete(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
	}
	var b strings.Builder
	defaultGenerator().generateClient(&b, "User", "users", fields, true)
	got := b.String()

	checks := []string{
		// Where auto-filters deleted records
		`lux.NewTimeField("\"deleted_at\"").IsNull()`,
		// WithDeleted bypasses soft delete filter
		"func (c *UserClient) WithDeleted(conds ...lux.Condition) *UserQuery",
		// SoftDelete sets deleted_at
		"func (c *UserClient) SoftDelete(ctx context.Context, conds ...lux.Condition) (int64, error)",
		`lux.SetField{Col: "\"deleted_at\"", Val: time.Now()}`,
		// ForceDelete does real DELETE
		"func (c *UserClient) ForceDelete(ctx context.Context, conds ...lux.Condition) (int64, error)",
		"q.Delete(ctx)",
		// Restore un-deletes
		"func (c *UserClient) Restore(ctx context.Context, conds ...lux.Condition) (int64, error)",
		`lux.SetField{Col: "\"deleted_at\"", Val: nil}`,
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("missing %q in:\n%s", check, got)
		}
	}
}

func TestGenerateClientNoSoftDelete(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
	}
	var b strings.Builder
	defaultGenerator().generateClient(&b, "User", "users", fields, false)
	got := b.String()

	// Should NOT have soft delete methods
	absent := []string{"WithDeleted", "SoftDelete", "ForceDelete", "Restore", "deleted_at"}
	for _, a := range absent {
		if strings.Contains(got, a) {
			t.Errorf("non-soft model should not contain %q in:\n%s", a, got)
		}
	}
}

func TestGenerateQueryBuilderSoft(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "Post",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "title", Type: &ast.TypeRef{Name: "String"}},
		},
		Directives: []*ast.Directive{{Name: "soft"}},
	}

	var b strings.Builder
	defaultGenerator().generateQueryBuilder(&b, m, nil)
	got := b.String()

	// Should have deletedAt in scanner and fields
	checks := []string{
		"SoftDelete",
		"ForceDelete",
		"WithDeleted",
		"Restore",
		"DeletedAt",    // field constant
		`"deleted_at"`, // column name in scanner
		`*time.Time`,   // nullable DateTime type for deletedAt
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("missing %q in:\n%s", check, got)
		}
	}
}

func TestGenerateQueryBuilderSoftWithExistingDeletedAt(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "Post",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "title", Type: &ast.TypeRef{Name: "String"}},
			{Name: "deletedAt", Type: &ast.TypeRef{Name: "DateTime", Nullable: true}},
		},
		Directives: []*ast.Directive{{Name: "soft"}},
	}

	var b strings.Builder
	defaultGenerator().generateQueryBuilder(&b, m, nil)
	got := b.String()

	// Should still have soft delete methods
	if !strings.Contains(got, "SoftDelete") {
		t.Errorf("missing SoftDelete:\n%s", got)
	}
	// Should NOT duplicate deletedAt field
	count := strings.Count(got, `"deleted_at"`) + strings.Count(got, `\"deleted_at\"`)
	// deletedAt appears in: scanner case, field constant value, where field value, plus soft delete methods
	// but should NOT have duplicate struct fields
	if count < 3 {
		t.Errorf("expected at least 3 occurrences of deleted_at, got %d:\n%s", count, got)
	}
}

func TestGenerateExtendQueryBuilderFiltersRelations(t *testing.T) {
	ext := &ast.ExtendDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "name", Type: &ast.TypeRef{Name: "String"}},
			// Relation field (capitalized non-primitive) — should be skipped
			{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
			// Single model relation — should be skipped
			{Name: "profile", Type: &ast.TypeRef{Name: "Profile"}},
			// Computed field — should be skipped
			{Name: "fullName", Type: &ast.TypeRef{Name: "String"}, Computed: &ast.ComputedField{}},
		},
	}
	var b strings.Builder
	generateExtendQueryBuilder(&b, ext)
	got := b.String()

	// Should have id and name
	if !strings.Contains(got, `"id"`) || !strings.Contains(got, `"name"`) {
		t.Fatalf("should include primitive fields, got:\n%s", got)
	}
	// Should NOT have posts or profile as DB columns
	if strings.Contains(got, `"posts"`) {
		t.Fatalf("should not include list relation field 'posts', got:\n%s", got)
	}
	if strings.Contains(got, `"profile"`) {
		t.Fatalf("should not include single relation field 'profile', got:\n%s", got)
	}
}

// ─── fieldConditionTypeWithDirectives ────────────────────────────────────────

func TestFieldConditionTypeWithDirectives_SearchField(t *testing.T) {
	f := &ast.FieldDecl{
		Name:       "title",
		Type:       &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{{Name: "search"}},
	}
	got := fieldConditionTypeWithDirectives(f)
	if got != "SearchField" {
		t.Errorf("expected SearchField, got %q", got)
	}
}

func TestFieldConditionTypeWithDirectives_StringWithoutSearch(t *testing.T) {
	f := &ast.FieldDecl{
		Name: "name",
		Type: &ast.TypeRef{Name: "String"},
	}
	got := fieldConditionTypeWithDirectives(f)
	if got != "StringField" {
		t.Errorf("expected StringField, got %q", got)
	}
}

func TestFieldConditionTypeWithDirectives_IntField(t *testing.T) {
	f := &ast.FieldDecl{
		Name: "age",
		Type: &ast.TypeRef{Name: "Int"},
	}
	got := fieldConditionTypeWithDirectives(f)
	if got != "IntField" {
		t.Errorf("expected IntField, got %q", got)
	}
}

func TestFieldConditionTypeWithDirectives_NilType(t *testing.T) {
	f := &ast.FieldDecl{Name: "unknown"}
	got := fieldConditionTypeWithDirectives(f)
	if got != "StringField" {
		t.Errorf("expected StringField for nil type, got %q", got)
	}
}

func TestGenerateWhereFieldsWithSearch(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "title", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "search"}}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
	}

	var b strings.Builder
	defaultGenerator().generateWhereFields(&b, "Post", fields)
	got := b.String()

	// @search String field should use SearchField
	if !strings.Contains(got, "Title lux.SearchField") {
		t.Errorf("expected SearchField for @search field:\n%s", got)
	}
	if !strings.Contains(got, `Title: lux.NewSearchField("\"title\"")`) {
		t.Errorf("expected NewSearchField constructor:\n%s", got)
	}
	// Non-search String field should remain StringField
	if !strings.Contains(got, "Name lux.StringField") {
		t.Errorf("expected StringField for non-search field:\n%s", got)
	}
}

func TestGeneratedBeforeSaveWriteBoundaries(t *testing.T) {
	runGeneratedFixture(t, `
model User @noTime @crud { id: Int @id name: Int @beforeSave { it += 2 it *= 3 } }
fn createDirect(id: Int): User { return User.create(id: id, name: 4) }
fn updateDirect(user: User): Int { return user.update(name: 4) }
`, `package generated
import("testing";"os";"fmt";"bytes";"net/http/httptest";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/pg")
func TestWriteBoundaries(t *testing.T){
 connection:=os.Getenv("LUXO_TEST_DATABASE_URL");if connection==""{t.Skip("requires isolated LUXO_TEST_DATABASE_URL")}
 ctx:=t.Context();db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE users(id bigint PRIMARY KEY,name bigint NOT NULL)");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db)
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 crud:=func(name string,id int64,binary bool)error{
  body:=[]byte(fmt.Sprintf("{\"$api\":\"%s\",\"$select\":\"id,name\",\"id\":%d,\"name\":4}",name,id))
  if binary{
   mask,err:=codec.FieldMaskSet(nil,1);if err!=nil{return err};mask,err=codec.FieldMaskSet(mask,2);if err!=nil{return err}
   mask,err=codec.AppendSelectionMask(nil,mask,nil);if err!=nil{return err}
   body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,mask,map[string]any{"id":id,"name":int64(4)},router.Registry.ParamOrder(name));if err!=nil{return err}
  }
  request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Content-Type","application/json")
  if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{return fmt.Errorf("CRUD %s: %d %s",name,response.Code,response.Body.String())};return nil
 }
 cases:=[]struct{name string; existing bool; write func(int64)error}{
  {"crudCreate",false,func(id int64)error{return crud("createUser",id,false)}},
  {"crudUpdate",true,func(id int64)error{return crud("updateUser",id,false)}},
  {"crudCreateBinary",false,func(id int64)error{return crud("createUser",id,true)}},
  {"crudUpdateBinary",true,func(id int64)error{return crud("updateUser",id,true)}},
  {"dslCreate",false,func(id int64)error{_,err:=app.createDirect(ctx,id);return err}},
  {"dslUpdate",true,func(id int64)error{_,err:=app.updateDirect(ctx,&User{Id:id});return err}},
  {"builderCreate",false,func(id int64)error{_,err:=app.User.Create().SetId(id).SetName(4).Exec(ctx);return err}},
  {"builderUpdate",true,func(id int64)error{_,err:=newUserUpdateBuilder(db,"users",id).SetName(4).Exec(ctx);return err}},
  {"batchCreate",false,func(id int64)error{_,err:=app.User.CreateMany().Add(UserCreateInput{Id:id,Name:4}).Exec(ctx);return err}},
  {"queryUpdate",true,func(id int64)error{_,err:=app.User.Where(UserWhere.Id.Eq(id)).Update().SetName(4).Exec(ctx);return err}},
 }
 for i,tc:=range cases{t.Run(tc.name,func(t *testing.T){
  id:=int64(i+1)
  if tc.existing{if _,err:=pg.Exec(ctx,db,"INSERT INTO users VALUES($1,0)",id);err!=nil{t.Fatal(err)}}
  if err:=tc.write(id);err!=nil{t.Fatal(err)}
  got,err:=db.QueryScalar[int64](ctx,"SELECT name FROM users WHERE id=$1",id)
  if err!=nil||got!=18{t.Fatalf("beforeSave must run exactly once: got=%d want=18 err=%v",got,err)}
 })}
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "name": 2}}})
}

func TestGeneratedUpdatePreservesSourceObject(t *testing.T) {
	runGeneratedFixture(t, `
model Plain @noTime { id: Int @id name: Int }
model User @noTime { id: Int @id name: Int @beforeSave { it += 2 it *= 3 } }
fn copyPlainAdd(source: Plain, target: Int): Int { Plain.where(id: target).update(name: source.name + 1) return 0 }
fn copyPlainSubtract(source: Plain, target: Int): Int { Plain.where(id: target).update(name: source.name - 1) return 0 }
fn copyUserAdd(source: User, target: Int): Int { User.where(id: target).update(name: source.name + 1) return 0 }
fn copyUserSubtract(source: User, target: Int): Int { User.where(id: target).update(name: source.name - 1) return 0 }
`, `package generated
import("testing";"os";"github.com/light-speak/luxo/pkg/lux/pg")
func TestSourceObject(t *testing.T){
 connection:=os.Getenv("LUXO_TEST_DATABASE_URL");if connection==""{t.Skip("requires isolated LUXO_TEST_DATABASE_URL")}
 ctx:=t.Context();db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE plains(id bigint PRIMARY KEY,name bigint NOT NULL);CREATE TABLE users(id bigint PRIMARY KEY,name bigint NOT NULL);INSERT INTO plains VALUES(1,7);INSERT INTO users VALUES(1,7)");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);plain:=&Plain{Id:99,Name:41};user:=&User{Id:99,Name:41}
 for _,tc:=range []struct{name,table string;want int64;run func()error}{
  {"plainAdd","plains",42,func()error{_,err:=app.copyPlainAdd(ctx,plain,1);return err}},
  {"plainSubtract","plains",40,func()error{_,err:=app.copyPlainSubtract(ctx,plain,1);return err}},
  {"preparedAdd","users",132,func()error{_,err:=app.copyUserAdd(ctx,user,1);return err}},
  {"preparedSubtract","users",126,func()error{_,err:=app.copyUserSubtract(ctx,user,1);return err}},
 }{t.Run(tc.name,func(t *testing.T){
  if _,err:=pg.Exec(ctx,db,"UPDATE "+tc.table+" SET name=7 WHERE id=1");err!=nil{t.Fatal(err)}
  if err:=tc.run();err!=nil{t.Fatal(err)}
  got,err:=db.QueryScalar[int64](ctx,"SELECT name FROM "+tc.table+" WHERE id=1")
  if err!=nil||got!=tc.want{t.Fatalf("source expression changed: got=%d want=%d err=%v",got,tc.want,err)}
  if plain.Name!=41||user.Name!=41{t.Fatal("source object changed")}
 })}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Plain": {"id": 1, "name": 2}, "User": {"id": 1, "name": 2}}})
}

func TestMutationSetterFieldsWithoutPrimaryKey(t *testing.T) {
	field := &ast.FieldDecl{Name: "name", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "immutable"}}}
	fields := []*ast.FieldDecl{field}
	if got := mutationSetterFields(fields, nil, true); len(got) != 0 {
		t.Fatal("update exposes immutable setter")
	}
	if got := mutationSetterFields(fields, nil, false); len(got) != 1 || got[0] != field {
		t.Fatal("create lost immutable input")
	}
}

func TestGeneratedRepeatedTypedSetter(t *testing.T) {
	runGeneratedFixture(t, `model User @noTime { id: Int @id name: Int @beforeSave { it += 2 it *= 3 } optional: Int? @beforeSave { it += 1 } }`, `package generated
import "testing"
func TestRepeatedTypedSetter(t *testing.T) {
 t.Run("create",func(t *testing.T){
  input:=int64(9)
  builder:=(&UserCreateBuilder{}).SetName(4).SetOptional(&input).SetName(5).SetOptional(nil)
  if input!=9||builder.Cols[1]!="\"optional\""||builder.Vals[1].(*int64)!=nil{t.Fatal("nullable ownership or column order changed")}
  if len(builder.Cols)!=2||len(builder.Vals)!=2||builder.Cols[0]!="\"name\""||builder.Vals[0]!=int64(21){t.Fatalf("duplicate or incorrectly prepared assignment: %v %v",builder.Cols,builder.Vals)}
 })
 t.Run("update",func(t *testing.T){
  input:=int64(9)
  builder:=(&UserUpdateBuilder{}).SetName(4).SetOptional(&input).SetName(5).SetOptional(nil)
  if input!=9||builder.Sets[1].Col!="\"optional\""||builder.Sets[1].Val.(*int64)!=nil{t.Fatal("nullable ownership or column order changed")}
  if len(builder.Sets)!=2||builder.Sets[0].Col!="\"name\""||builder.Sets[0].Val!=int64(21){t.Fatalf("duplicate or incorrectly prepared assignment: %v",builder.Sets)}
 })
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "name": 2, "optional": 3}}})
}

func TestGeneratedBuilderRetryPersistence(t *testing.T) {
	runGeneratedFixture(t, `model User {
 id: Int @id @serial
 name: Int @beforeSave { it += 2 it *= 3 }
 optional: Int? @beforeSave { it += 1 }
 createdAt: DateTime
 updatedAt: DateTime
 }`, `package generated
import("testing";"os";"context";"github.com/light-speak/luxo/pkg/lux/pg")
func TestBuilderRetryPersistence(t *testing.T) {
 connection:=os.Getenv("LUXO_TEST_DATABASE_URL");if connection==""{t.Skip("requires isolated LUXO_TEST_DATABASE_URL")}
 ctx:=t.Context();db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE users(id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,name bigint NOT NULL,optional bigint,created_at timestamptz NOT NULL,updated_at timestamptz NOT NULL)");err!=nil{t.Fatal(err)}
 input:=int64(8);app:=NewFromDB(db)
 builder:=app.User.Create().SetName(4).SetOptional(&input).SetName(5).SetOptional(nil)
 canceled,cancel:=context.WithCancel(ctx);cancel()
 if _,err:=builder.Exec(canceled);err==nil{t.Fatal("accepted canceled create")}
 first,err:=builder.Exec(ctx);if err!=nil{t.Fatalf("retry after canceled create: %v",err)}
 if first.Name!=21||first.Optional!=nil||input!=8||first.CreatedAt.IsZero()||first.UpdatedAt.IsZero(){t.Fatalf("invalid first row: %+v",first)}
 second,err:=builder.Exec(ctx);if err!=nil{t.Fatalf("repeat successful create: %v",err)}
 if second.Id==first.Id||second.Name!=21||len(builder.Cols)!=2||len(builder.Vals)!=2{t.Fatal("create changed input state")}
 update:=newUserUpdateBuilder(db,"users",first.Id).SetName(1).SetOptional(&input).SetName(2).SetOptional(nil)
 for i:=0;i<2;i++{row,err:=update.Exec(ctx);if err!=nil{t.Fatal(err)};if row.Name!=12||row.Optional!=nil||input!=8||len(update.Sets)!=2{t.Fatal("update changed preparation",row)}}
 stored,err:=app.User.Find(ctx,first.Id);if err!=nil||stored.Name!=12||stored.Optional!=nil{t.Fatal("stored value",stored,err)}
 count,err:=db.QueryScalar[int64](ctx,"SELECT count(*) FROM users");if err!=nil||count!=2{t.Fatal("unexpected insert count",count,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "name": 2, "optional": 3, "createdAt": 4, "updatedAt": 5}}})
}

func TestGeneratedUUIDCreateRetry(t *testing.T) {
	runGeneratedFixture(t, `model Entry @noTime { id: UUID @id @auto value: Int }`, `package generated
import("testing";"os";"context";"github.com/light-speak/luxo/pkg/lux/pg")
func TestUUIDCreateRetry(t *testing.T) {
 connection:=os.Getenv("LUXO_TEST_DATABASE_URL");if connection==""{t.Skip("requires isolated LUXO_TEST_DATABASE_URL")}
 ctx:=t.Context();db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE entrys(id uuid PRIMARY KEY,value bigint NOT NULL)");err!=nil{t.Fatal(err)}
 builder:=NewFromDB(db).Entry.Create().SetValue(7).SetValue(9)
 canceled,cancel:=context.WithCancel(ctx);cancel()
 if _,err:=builder.Exec(canceled);err==nil{t.Fatal("accepted canceled create")}
 first,err:=builder.Exec(ctx);if err!=nil{t.Fatal(err)}
 second,err:=builder.Exec(ctx);if err!=nil{t.Fatal(err)}
 if first.Id==second.Id||first.Id.Version()!=7||second.Id.Version()!=7||first.Value!=9||second.Value!=9{t.Fatal("invalid automatic UUID or input",first,second)}
 if len(builder.Cols)!=1||len(builder.Vals)!=1||builder.Cols[0]!="\"value\""{t.Fatal("automatic UUID changed builder state",builder.Cols,builder.Vals)}
 count,err:=db.QueryScalar[int64](ctx,"SELECT count(*) FROM entrys");if err!=nil||count!=2{t.Fatal("unexpected insert count",count,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Entry": {"id": 1, "value": 2}}})
}

func TestGeneratedRepeatedUpdateExecution(t *testing.T) {
	runGeneratedFixture(t, `model User { id: Int @id name: Int @beforeSave { it += 2 it *= 3 } createdAt: DateTime updatedAt: DateTime }`, `package generated
import("testing";"os";"time";"context";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/light-speak/luxo/pkg/lux")
func TestRepeatedUpdateExecution(t *testing.T) {
 connection:=os.Getenv("LUXO_TEST_DATABASE_URL");if connection==""{t.Skip("requires isolated LUXO_TEST_DATABASE_URL")}
 ctx:=t.Context();db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE users(id bigint PRIMARY KEY,name bigint NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now()); INSERT INTO users(id,name) VALUES(1,0)");err!=nil{t.Fatal(err)}
 builder:=newUserUpdateBuilder(db,"users",1).SetName(4)
 canceled,cancel:=context.WithCancel(ctx);cancel()
 if _,err:=builder.Exec(canceled);err==nil{t.Fatal("accepted canceled execution")}
 if _,err:=builder.ExecWhere(ctx,lux.NewIntField("missing_column").Eq(1));err==nil{t.Fatal("lost database error")}
 if row,err:=builder.ExecWhere(ctx,lux.NewIntField("id").Eq(2));err!=nil||row!=nil{t.Fatal("no-match result",row,err)}
 stored,readErr:=db.QueryScalar[int64](ctx,"SELECT name FROM users WHERE id=1")
 if err:=readErr;err!=nil||stored!=0{t.Fatal("failed or unmatched execution changed record",stored,err)}
 previous:=time.Time{}
 for i:=0;i<4;i++ {
  var row *User
  if i%2==0{row,err=builder.Exec(ctx)}else{row,err=builder.ExecWhere(ctx,lux.NewIntField("id").Eq(1))}
  if err!=nil{t.Fatalf("execution %d: %v",i,err)}
  if row==nil||row.Name!=18||row.UpdatedAt.Before(previous){t.Fatalf("execution %d changed preparation/time: %+v",i,row)}
  previous=row.UpdatedAt
 }
 if len(builder.Sets)!=1||builder.Sets[0].Col!="\"name\""{t.Fatalf("execution mutated assignments: %+v",builder.Sets)}
 client:=&UserClient{db:db}
 bulk:=client.Where(UserWhere.Id.Eq(1)).Update().SetName(5)
 if _,err:=bulk.Exec(canceled);err==nil{t.Fatal("bulk accepted canceled execution")}
 for i:=0;i<3;i++ {
  count,err:=bulk.Exec(ctx);if err!=nil||count!=1{t.Fatalf("bulk execution %d: %d %v",i,count,err)}
  got,err:=db.QueryScalar[int64](ctx,"SELECT name FROM users WHERE id=1");if err!=nil||got!=21{t.Fatal(got,err)}
 }
 if len(bulk.state.Sets)!=1||bulk.state.Sets[0].Col!="\"name\""{t.Fatal("bulk execution mutated assignments",bulk.state.Sets)}
 if count,err:=client.Where(UserWhere.Id.Eq(99)).Update().SetName(6).Exec(ctx);err!=nil||count!=0{t.Fatal("unmatched bulk",count,err)}

}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "name": 2, "createdAt": 3, "updatedAt": 4}}}, generatedUpdateTimestampBenchmarks)
}

func TestGeneratedRepeatedHashedUpdates(t *testing.T) {
	runGeneratedFixture(t, `
model User @noTime { id: Int @id password: String @hash }
fn instanceTwice(user: User): Int {
 user.update(password: "first")
 user.update(password: "second")
 return 0
}
fn queryTwice(id: Int): Int {
 User.where(id: id).update(password: "first")
 User.where(id: id).update(password: "second")
 return 0
}
fn instanceFailure(user: User, password: String): Int { user.update(password: password) return 0 }
fn queryFailure(id: Int, password: String): Int { User.where(id: id).update(password: password) return 0 }
`, `package generated
import("testing";"strings";"errors";"os";"github.com/light-speak/luxo/pkg/lux/pg";luxocrypto "github.com/light-speak/luxo/pkg/lux/crypto")
func TestGeneratedHashedUpdateFailures(t *testing.T) {
 password:=strings.Repeat("x",73);_,expected:=luxocrypto.HashPassword(password);if expected==nil{t.Fatal("missing reference hash failure")}
 expected=errors.Unwrap(expected);if expected==nil{t.Fatal("missing reference bcrypt cause")}
 app:=&App{}
 if _,err:=app.instanceFailure(t.Context(),&User{Id:1},password);!errors.Is(err,expected){t.Fatal(err)}
 if _,err:=app.queryFailure(t.Context(),1,password);!errors.Is(err,expected){t.Fatal(err)}
}

func TestRepeatedHashWrites(t *testing.T){
 connection:=os.Getenv("LUXO_TEST_DATABASE_URL");if connection==""{t.Skip("requires isolated LUXO_TEST_DATABASE_URL")}
 ctx:=t.Context();db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 ddl:="CREATE TABLE users(id bigint PRIMARY KEY,password text NOT NULL); INSERT INTO users VALUES(1,'seed'); CREATE TABLE password_writes(seq bigserial,password text NOT NULL); CREATE FUNCTION audit_password() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO password_writes(password) VALUES(NEW.password); RETURN NEW; END $$; CREATE TRIGGER audit_password AFTER UPDATE ON users FOR EACH ROW EXECUTE FUNCTION audit_password()"
 if _,err:=pg.Exec(ctx,db,ddl);err!=nil{t.Fatal(err)}
 app:=NewFromDB(db)
 for _,tc:=range []struct{name string;write func()error;reject func()error}{
  {"instance",func()error{_,err:=app.instanceTwice(ctx,&User{Id:1});return err},func()error{_,err:=app.instanceFailure(ctx,&User{Id:1},strings.Repeat("x",73));return err}},
  {"query",func()error{_,err:=app.queryTwice(ctx,1);return err},func()error{_,err:=app.queryFailure(ctx,1,strings.Repeat("x",73));return err}},
 }{t.Run(tc.name,func(t *testing.T){
  if _,err:=pg.Exec(ctx,db,"TRUNCATE password_writes");err!=nil{t.Fatal(err)}
  if err:=tc.write();err!=nil{t.Fatal(err)}
  count,err:=db.QueryScalar[int64](ctx,"SELECT count(*) FROM password_writes");if err!=nil||count!=2{t.Fatal(count,err)}
  first,err:=db.QueryScalar[string](ctx,"SELECT password FROM password_writes ORDER BY seq LIMIT 1");if err!=nil||!luxocrypto.VerifyPassword("first",first){t.Fatal("first write was not hashed",err)}
  last,err:=db.QueryScalar[string](ctx,"SELECT password FROM users WHERE id=1");if err!=nil||!luxocrypto.VerifyPassword("second",last)||luxocrypto.VerifyPassword("first",last){t.Fatal("final password mismatch",err)}
  if err:=tc.reject();err==nil{t.Fatal("invalid password accepted")}
  unchanged,err:=db.QueryScalar[string](ctx,"SELECT password FROM users WHERE id=1");if err!=nil||unchanged!=last{t.Fatal("failure changed stored hash",err)}
  count,err=db.QueryScalar[int64](ctx,"SELECT count(*) FROM password_writes");if err!=nil||count!=2{t.Fatal("failure executed update",count,err)}
 })}
}

`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "password": 2}}})
}

func TestGeneratedMutationArgumentSnapshots(t *testing.T) {
	runGeneratedFixture(t, `
model Record @noTime { id: Int @id value: Int marker: Int }
fn changeSource(source: Record): Int { source.value = 99 return 2 }
fn createSnapshot(source: Record, id: Int): Record { return Record.create(id: id, value: source.value, marker: changeSource(source)) }
fn updateSnapshot(source: Record, target: Record): Int { return target.update(value: source.value, marker: changeSource(source)) }
fn querySnapshot(source: Record, id: Int): Int { Record.where(id: id).update(value: source.value, marker: changeSource(source)) return 0 }
fn createConstantSnapshot(source: Record, id: Int): Record { return Record.create(id: id, value: 1 + 2, marker: changeSource(source)) }
`, `package generated
import("testing";"os";"github.com/light-speak/luxo/pkg/lux/pg")
func TestMutationSnapshots(t *testing.T){
 connection:=os.Getenv("LUXO_TEST_DATABASE_URL");if connection==""{t.Skip("requires isolated LUXO_TEST_DATABASE_URL")}
 ctx:=t.Context();db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE records(id bigint PRIMARY KEY,value bigint NOT NULL,marker bigint NOT NULL)");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db)
 for i,tc:=range []struct{name string;existing bool;want int64;write func(*Record,int64)error}{
  {"create",false,41,func(source *Record,id int64)error{_,err:=app.createSnapshot(ctx,source,id);return err}},
  {"instance",true,41,func(source *Record,id int64)error{_,err:=app.updateSnapshot(ctx,source,&Record{Id:id});return err}},
  {"query",true,41,func(source *Record,id int64)error{_,err:=app.querySnapshot(ctx,source,id);return err}},
  {"constant",false,3,func(source *Record,id int64)error{_,err:=app.createConstantSnapshot(ctx,source,id);return err}},
 }{t.Run(tc.name,func(t *testing.T){
  id:=int64(i+1);source:=&Record{Id:99,Value:41}
  if tc.existing{if _,err:=pg.Exec(ctx,db,"INSERT INTO records VALUES($1,0,0)",id);err!=nil{t.Fatal(err)}}
  if err:=tc.write(source,id);err!=nil{t.Fatal(err)}
  got,err:=app.Record.Find(ctx,id)
  if err!=nil||got==nil||got.Value!=tc.want||got.Marker!=2||source.Value!=99{t.Fatalf("argument order changed: row=%+v source=%+v err=%v",got,source,err)}
 })}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Record": {"id": 1, "value": 2, "marker": 3}}})
}

func TestGeneratedQueryArgumentFailureStopsExecution(t *testing.T) {
	runGeneratedFixture(t, `
model Record @noTime { id: Int @id value: Int }
fn step(index: Int): Result<Int> @native
fn stopFind(): [Record] {
 return Record.findMany(where: it.id == step(1)?, limit: step(2)?, offset: step(3)?)
}
fn stopChain(): Int {
 Record.where(id: step(1)?, value: step(2)?).update(value: step(3)?)
 return 0
}
fn stopBulkConditionFirst(): Int {
 return Record.updateMany(where: it.id == step(1)? && it.value == step(2)?, value: step(3)?)
}
fn stopBulkValueFirst(): Int {
 return Record.updateMany(value: step(1)?, where: it.id == step(2)? || it.value == step(3)?)
}
fn stopDeleteNested(): Int {
 return Record.deleteMany(where: (it.id == step(1)? || it.value == step(2)?) && it.id != step(3)?)
}
fn stopRange(): [Record] {
 return Record.where { it.id in ((step(1)?)..(step(2)?)) }.limit(step(3)?).all()
}
fn stopFunctionFind(): [Record] {
 return findMany(Record, where: it.id == step(1)?, limit: step(2)?, offset: step(3)?)
}
`, `package generated
import("context";"errors";"slices";"testing")
var argumentFailure=errors.New("argument failed")
type queryFailureResolver struct{failAt int64;calls []int64}
func(r *queryFailureResolver)Step(_ context.Context,index int64)(int64,error){
 r.calls=append(r.calls,index)
 if index==r.failAt{return 0,argumentFailure}
 return index,nil
}
func TestQueryFailureStopsExecution(t *testing.T){
 for _,tc:=range []struct{name string;call func(*App)error}{
  {"find",func(app *App)error{_,err:=app.stopFind(t.Context());return err}},
  {"chain",func(app *App)error{_,err:=app.stopChain(t.Context());return err}},
  {"bulkConditionFirst",func(app *App)error{_,err:=app.stopBulkConditionFirst(t.Context());return err}},
  {"bulkValueFirst",func(app *App)error{_,err:=app.stopBulkValueFirst(t.Context());return err}},
  {"deleteNested",func(app *App)error{_,err:=app.stopDeleteNested(t.Context());return err}},
  {"range",func(app *App)error{_,err:=app.stopRange(t.Context());return err}},
  {"functionFind",func(app *App)error{_,err:=app.stopFunctionFind(t.Context());return err}},
 }{t.Run(tc.name,func(t *testing.T){
  for failAt:=int64(1);failAt<=3;failAt++{
   resolver:=&queryFailureResolver{failAt:failAt}
   app:=NewFromDB(nil);app.Resolver=resolver
   if err:=tc.call(app);!errors.Is(err,argumentFailure){t.Fatalf("failure %d: %v",failAt,err)}
   want:=[]int64{1,2,3}[:failAt]
   if !slices.Equal(resolver.calls,want){t.Fatalf("failure %d: calls=%v want=%v",failAt,resolver.calls,want)}
  }
 })}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Record": {"id": 1, "value": 2}}})
}

func TestGeneratedBulkUpdateAndNamedAggregate(t *testing.T) {
	runGeneratedFixture(t, `
model BatchRow @noTime { id: Int @id value: Int }
fn updateAll(value: Int): Int { return BatchRow.updateMany(value: value) }
fn totals(): Int {
 val (left, right) = await {
  BatchRow.sum { row -> row.value }
  BatchRow.where(it.value > 0).sum { row -> row.value }
 }
 return left + right
}
`, `package generated
import("testing";"os";"github.com/light-speak/luxo/pkg/lux/pg")
func TestBulkUpdateAndNamedAggregate(t *testing.T){
 connection:=os.Getenv("LUXO_TEST_DATABASE_URL")
 if connection==""{t.Skip("requires isolated LUXO_TEST_DATABASE_URL")}
 ctx:=t.Context();db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE batch_rows(id bigint PRIMARY KEY,value bigint NOT NULL); INSERT INTO batch_rows VALUES(1,1),(2,2),(3,3)");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db)
 if total,err:=app.totals(ctx);err!=nil||total!=12{t.Fatal("initial aggregate",total,err)}
 if changed,err:=app.updateAll(ctx,7);err!=nil||changed!=3{t.Fatal("unconditional update",changed,err)}
 rows,err:=app.BatchRow.Where().All(ctx);if err!=nil||len(rows)!=3{t.Fatal(rows,err)}
 for _,row:=range rows{if row.Value!=7{t.Fatal("row not updated",row)}}
 if total,err:=app.totals(ctx);err!=nil||total!=42{t.Fatal("updated aggregate",total,err)}
 if _,err:=pg.Exec(ctx,db,"DELETE FROM batch_rows");err!=nil{t.Fatal(err)}
 if changed,err:=app.updateAll(ctx,9);err!=nil||changed!=0{t.Fatal("empty update",changed,err)}
 if total,err:=app.totals(ctx);err!=nil||total!=0{t.Fatal("empty aggregate",total,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"BatchRow": {"id": 1, "value": 2}}})
}

func TestGeneratedQueryReceiverSnapshot(t *testing.T) {
	runGeneratedFixture(t, `
model Record @noTime { id: Int @id value: Int }
fn changeRecordID(source: Record): Int { source.id = 2 return 42 }
fn updateSelected(source: Record): Int {
 Record.where(id: source.id).update(value: changeRecordID(source))
 return 0
}
fn updateSelectedArguments(source: Record): Int {
 Record.where(id: source.id, value: changeRecordID(source)).update(value: 7)
 return 0
}
fn updateSelectedAnd(source: Record): Int {
 Record.where(it.id == source.id && it.value == changeRecordID(source)).update(value: 7)
 return 0
}
fn updateSelectedOr(source: Record): Int {
 Record.where(it.id == source.id || it.value == changeRecordID(source)).update(value: 7)
 return 0
}
fn findSelected(source: Record): [Record] {
 return Record.findMany(where: it.id == source.id, limit: changeRecordID(source))
}
fn bulkSelected(source: Record): Int {
 return Record.updateMany(where: it.id == source.id, value: changeRecordID(source))
}
fn bulkValueFirst(source: Record): Int {
 return Record.updateMany(value: source.id, where: it.value == changeRecordID(source))
}
fn rangeEnd(source: Record): Int { source.id = 2 return 1 }
fn rangeSelected(source: Record): [Record] {
 return Record.where { it.id in source.id..rangeEnd(source) }.all()
}
fn functionSelected(source: Record): [Record] {
 return findMany(Record, where: it.id == source.id, limit: changeRecordID(source))
}
fn deleteSelected(source: Record): Int {
 return Record.deleteMany(where: (it.id == source.id || it.value == changeRecordID(source)) && it.id > 0)
}
`, `package generated
import("testing";"os";"github.com/light-speak/luxo/pkg/lux/pg")
func TestSelectedRow(t *testing.T){
 connection:=os.Getenv("LUXO_TEST_DATABASE_URL");if connection==""{t.Skip("requires isolated LUXO_TEST_DATABASE_URL")}
 ctx:=t.Context();db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE records(id bigint PRIMARY KEY,value bigint NOT NULL); INSERT INTO records VALUES(1,0),(2,0)");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);source:=&Record{Id:1}
 if _,err:=app.updateSelected(ctx,source);err!=nil{t.Fatal(err)}
 first,err:=app.Record.Find(ctx,1);if err!=nil{t.Fatal(err)}
 second,err:=app.Record.Find(ctx,2);if err!=nil{t.Fatal(err)}
 if first.Value!=42||second.Value!=0||source.Id!=2{t.Fatalf("wrong target: first=%+v second=%+v source=%+v",first,second,source)}
 if _,err:=pg.Exec(ctx,db,"UPDATE records SET value=42");err!=nil{t.Fatal(err)}
 source.Id=1
 if _,err:=app.updateSelectedArguments(ctx,source);err!=nil{t.Fatal(err)}
 first,err=app.Record.Find(ctx,1);if err!=nil{t.Fatal(err)}
 second,err=app.Record.Find(ctx,2);if err!=nil{t.Fatal(err)}
 if first.Value!=7||second.Value!=42||source.Id!=2{t.Fatalf("condition argument changed target: first=%+v second=%+v source=%+v",first,second,source)}
 for _,tc:=range []struct{name string;initial int64;write func(*Record)(int64,error)}{
  {"and",42,func(source *Record)(int64,error){return app.updateSelectedAnd(ctx,source)}},
  {"or",0,func(source *Record)(int64,error){return app.updateSelectedOr(ctx,source)}},
 }{t.Run(tc.name,func(t *testing.T){
  if _,err:=pg.Exec(ctx,db,"UPDATE records SET value=$1",tc.initial);err!=nil{t.Fatal(err)}
  source.Id=1
  if _,err:=tc.write(source);err!=nil{t.Fatal(err)}
  first,err:=app.Record.Find(ctx,1);if err!=nil{t.Fatal(err)}
  second,err:=app.Record.Find(ctx,2);if err!=nil{t.Fatal(err)}
  if first.Value!=7||second.Value!=tc.initial||source.Id!=2{t.Fatalf("compound predicate changed target: first=%+v second=%+v source=%+v",first,second,source)}
 })}
 t.Run("find",func(t *testing.T){
  source.Id=1
  rows,err:=app.findSelected(ctx,source)
  if err!=nil||len(rows)!=1||rows[0].Id!=1||source.Id!=2{t.Fatalf("find changed target: rows=%+v source=%+v err=%v",rows,source,err)}
 })
 t.Run("bulkWhereFirst",func(t *testing.T){
  if _,err:=pg.Exec(ctx,db,"UPDATE records SET value=0");err!=nil{t.Fatal(err)}
  source.Id=1
  count,err:=app.bulkSelected(ctx,source);if err!=nil||count!=1{t.Fatal(count,err)}
  first,err:=app.Record.Find(ctx,1);if err!=nil{t.Fatal(err)}
  second,err:=app.Record.Find(ctx,2);if err!=nil{t.Fatal(err)}
  if first.Value!=42||second.Value!=0{t.Fatalf("bulk changed target: first=%+v second=%+v",first,second)}
 })
 t.Run("bulkValueFirst",func(t *testing.T){
  if _,err:=pg.Exec(ctx,db,"UPDATE records SET value=42");err!=nil{t.Fatal(err)}
  source.Id=1
  count,err:=app.bulkValueFirst(ctx,source);if err!=nil||count!=2{t.Fatal(count,err)}
  first,err:=app.Record.Find(ctx,1);if err!=nil{t.Fatal(err)}
  second,err:=app.Record.Find(ctx,2);if err!=nil{t.Fatal(err)}
  if first.Value!=1||second.Value!=1||source.Id!=2{t.Fatalf("bulk changed value: first=%+v second=%+v source=%+v",first,second,source)}
 })
 for _,tc:=range []struct{name string;call func(*Record)([]*Record,error)}{
  {"range",func(source *Record)([]*Record,error){return app.rangeSelected(ctx,source)}},
  {"functionFind",func(source *Record)([]*Record,error){return app.functionSelected(ctx,source)}},
 }{t.Run(tc.name,func(t *testing.T){
  source.Id=1
  rows,err:=tc.call(source)
  if err!=nil||len(rows)!=1||rows[0].Id!=1||source.Id!=2{t.Fatalf("selection changed: rows=%+v source=%+v err=%v",rows,source,err)}
 })}
 t.Run("nestedDelete",func(t *testing.T){
  if _,err:=pg.Exec(ctx,db,"UPDATE records SET value=0");err!=nil{t.Fatal(err)}
  source.Id=1
  count,err:=app.deleteSelected(ctx,source)
  if err!=nil||count!=1||source.Id!=2{t.Fatal(count,source,err)}
  rows,err:=app.Record.Where().All(ctx)
  if err!=nil||len(rows)!=1||rows[0].Id!=2{t.Fatalf("deleted wrong row: rows=%+v err=%v",rows,err)}
 })
}
`, StableIDs{ModelFields: map[string]map[string]int{"Record": {"id": 1, "value": 2}}})
}

// This measures only timestamp assignment preparation, not SQL or network I/O.
const generatedUpdateTimestampBenchmarks = `package generated
import("testing";"time";"strconv";"github.com/light-speak/luxo/pkg/lux")
var timestampAssignments []lux.SetField
//go:noinline
func legacyTimestampAssignments(builder *UserUpdateBuilder, stamp time.Time) []lux.SetField {
 builder.Set("updated_at",stamp)
 return builder.Sets
}
//go:noinline
func isolatedTimestampAssignments(builder *UserUpdateBuilder, stamp time.Time) []lux.SetField {
 update:=builder.UpdateBase
 update.Set("updated_at",stamp)
 return update.Sets
}
func BenchmarkUpdateTimestampPreparation(b *testing.B) {
 stamp:=time.Unix(1,123)
 for _,size:=range []int{1,16,128} {
  for _,spare:=range []int{0,1} {
   for _,sample:=range []struct{name string;prepare func(*UserUpdateBuilder,time.Time)[]lux.SetField}{
    {"legacy",legacyTimestampAssignments},{"isolated",isolatedTimestampAssignments},
   } {
    b.Run(strconv.Itoa(size)+"/spare="+strconv.Itoa(spare)+"/"+sample.name,func(b *testing.B){
     builder:=newUserUpdateBuilder(nil,"users",1)
     backing:=make([]lux.SetField,size,size+spare)
     for i:=range backing{backing[i]=lux.SetField{Col:"field"+strconv.Itoa(i),Val:int64(i)}}
     b.ReportAllocs()
     for b.Loop(){
      // Both candidates start from the same length and capacity every time.
      builder.Sets=backing[:size:size+spare]
      timestampAssignments=sample.prepare(builder,stamp)
     }
     if len(timestampAssignments)!=size+1||timestampAssignments[size].Col!="updated_at"||timestampAssignments[size].Val!=stamp{b.Fatal("invalid prepared assignments")}
    })
   }
  }
 }
}
var creationColumns []string
var creationValues []any
//go:noinline
func legacyCreationAssignments(builder *UserCreateBuilder, stamp time.Time) ([]string,[]any) {
 builder.Set("created_at",stamp)
 builder.Set("updated_at",stamp)
 return builder.Cols,builder.Vals
}
//go:noinline
func isolatedCreationAssignments(builder *UserCreateBuilder, stamp time.Time) ([]string,[]any) {
 create:=builder.CreateBase
 create.Set("created_at",stamp)
 create.Set("updated_at",stamp)
 return create.Cols,create.Vals
}
func BenchmarkCreateTimestampPreparation(b *testing.B) {
 stamp:=time.Unix(1,123)
 for _,size:=range []int{1,16,128}{
  for _,spare:=range []int{0,2}{
   for _,sample:=range []struct{name string;prepare func(*UserCreateBuilder,time.Time)([]string,[]any)}{
    {"legacy",legacyCreationAssignments},{"isolated",isolatedCreationAssignments},
   }{
    b.Run(strconv.Itoa(size)+"/spare="+strconv.Itoa(spare)+"/"+sample.name,func(b *testing.B){
     builder:=&UserCreateBuilder{}
     cols:=make([]string,size,size+spare);vals:=make([]any,size,size+spare)
     for i:=range cols{cols[i]="field"+strconv.Itoa(i);vals[i]=int64(i)}
     b.ReportAllocs()
     for b.Loop(){
      builder.Cols=cols[:size:size+spare];builder.Vals=vals[:size:size+spare]
      creationColumns,creationValues=sample.prepare(builder,stamp)
     }
     if len(creationColumns)!=size+2||len(creationValues)!=size+2||creationColumns[size]!="created_at"||creationColumns[size+1]!="updated_at"||creationValues[size]!=stamp||creationValues[size+1]!=stamp{b.Fatal("invalid auto fields")}
    })
   }
  }
 }
}

`

func TestGeneratedTypedQueryUpdate(t *testing.T) {
	runGeneratedTransactionFixture(t, `
fn normalize(value: String): String { if value == "bad" { throw error.BadRequest } return value.trim() }
model User @noTime { id: Int @id name: String @beforeSave { normalize(it) } }
`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux")
func TestTypedQueryUpdate(t *testing.T){
 client:=&UserClient{}
 query:=client.Where(UserWhere.Id.Eq(1)).Select("id","name").OrderBy("id").Limit(1).Offset(0)
 update:=query.Update().SetName("bad").SetName("later")
 if _,err:=update.Exec(t.Context());err==nil{t.Fatal("must fail before using database")}
 good:=query.Update().SetName(" normalized ")
 if len(good.state.Sets)!=1||good.state.Sets[0]!=(lux.SetField{Col:"\"name\"",Val:"normalized"}){t.Fatal(good.state.Sets)}
}
`)
}

func TestGeneratedReadOnlyQueryHasNoMutationMethods(t *testing.T) {
	var source strings.Builder
	defaultGenerator().generateQueryFacade(&source, "External", nil, false)
	for _, forbidden := range []string{" Update(", " updatePrepared(", " Delete(", " SoftDelete("} {
		if strings.Contains(source.String(), forbidden) {
			t.Fatalf("read-only facade exposes %s", forbidden)
		}
	}
	for _, expected := range []string{"type ExternalQuery pg.Query[External]", " All(", " GroupBy[R any]", " AggregateBatchTo("} {
		if !strings.Contains(source.String(), expected) {
			t.Fatalf("missing %s", expected)
		}
	}
}

func TestGeneratedHashedBuilderPreparation(t *testing.T) {
	runGeneratedFixture(t, `
model Credential @noTime { id: Int @id password: String @hash @beforeSave { it.trim() } optional: String? @hash }
`, `package generated
import("testing";"strings";luxocrypto "github.com/light-speak/luxo/pkg/lux/crypto")
func TestHashBuilders(t *testing.T){
 plain:=" secret "
 create:=(&CredentialCreateBuilder{}).SetPassword(plain)
 update:=(&CredentialUpdateBuilder{}).SetPassword(plain)
 bulk:=(&CredentialClient{}).Where().Update().SetPassword(plain)
 batch:=(&CredentialCreateManyBuilder{}).Add(CredentialCreateInput{Password:plain})
 for _,value:=range []string{create.Vals[0].(string),update.Sets[0].Val.(string),bulk.state.Sets[0].Val.(string),batch.rows[0].Password}{
  if !luxocrypto.VerifyPassword("secret",value)||luxocrypto.VerifyPassword(plain,value){t.Fatal("missing hash or wrong preparation order")}
 }
 optional:="optional";nullable:=(&CredentialCreateBuilder{}).SetOptional(&optional)
 if !luxocrypto.VerifyPassword(optional,*nullable.Vals[0].(*string))||optional!="optional"{t.Fatal("nullable hash changed source")}
 if value:=(&CredentialCreateBuilder{}).SetOptional(nil).Vals[0].(*string);value!=nil{t.Fatal("null changed")}
 if (&Credential{}).VerifyOptional("optional"){t.Fatal("null verified")}
 if !(&Credential{Optional:nullable.Vals[0].(*string)}).VerifyOptional("optional"){t.Fatal("valid nullable hash rejected")}
 invalid:=strings.Repeat("x",73)
 if _,err:=(&CredentialCreateBuilder{}).SetPassword(invalid).SetPassword("later").Exec(t.Context());err==nil{t.Fatal("lost hash error")}
 if _,err:=(&CredentialUpdateBuilder{}).SetPassword(invalid).Exec(t.Context());err==nil{t.Fatal("lost update hash error")}
 if _,err:=(&CredentialClient{}).Where().Update().SetPassword(invalid).Exec(t.Context());err==nil{t.Fatal("lost bulk hash error")}
 if _,err:=(&CredentialCreateManyBuilder{}).Add(CredentialCreateInput{Password:invalid}).Exec(t.Context());err==nil{t.Fatal("lost batch hash error")}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Credential": {"id": 1, "password": 2, "optional": 3}}})
}

func TestGeneratedHashWriteBoundaries(t *testing.T) {
	runGeneratedFixture(t, `
model User @noTime @crud { id: Int @id password: String @hash @beforeSave { it.trim() } }
fn createDirect(id: Int, value: String): User { return User.create(id:id,password:value) }
fn updateDirect(user: User, value: String): Int { return user.update(password:value) }
`, `package generated
import("testing";"os";"fmt";"strings";"net/http/httptest";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/pg";luxocrypto "github.com/light-speak/luxo/pkg/lux/crypto")
func TestHashWriteBoundaries(t *testing.T){
 connection:=os.Getenv("LUXO_TEST_DATABASE_URL");if connection==""{t.Skip("requires isolated LUXO_TEST_DATABASE_URL")}
 ctx:=t.Context();db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE users(id bigint PRIMARY KEY,password text NOT NULL)");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);router:=api.NewRouter()
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 crud:=func(apiName string,id int64,value string)error{
  body:=fmt.Sprintf("{\"$api\":%q,\"$select\":\"id\",\"id\":%d,\"password\":%q}",apiName,id,value)
  request:=httptest.NewRequest("POST","/luvia",strings.NewReader(body));request.Header.Set("Content-Type","application/json")
  response:=httptest.NewRecorder();router.ServeHTTP(response,request)
  if response.Code!=200{return fmt.Errorf("CRUD: %d %s",response.Code,response.Body.String())};return nil
 }
 cases:=[]struct{name string;existing bool;write func(int64,string)error}{
 {"crudCreate",false,func(id int64,v string)error{return crud("createUser",id,v)}},
 {"crudUpdate",true,func(id int64,v string)error{return crud("updateUser",id,v)}},
 {"dslCreate",false,func(id int64,v string)error{_,err:=app.createDirect(ctx,id,v);return err}},
 {"dslUpdate",true,func(id int64,v string)error{_,err:=app.updateDirect(ctx,&User{Id:id},v);return err}},
 {"builderCreate",false,func(id int64,v string)error{_,err:=app.User.Create().SetId(id).SetPassword(v).Exec(ctx);return err}},
 {"builderUpdate",true,func(id int64,v string)error{_,err:=newUserUpdateBuilder(db,"users",id).SetPassword(v).Exec(ctx);return err}},
 {"batch",false,func(id int64,v string)error{_,err:=app.User.CreateMany().Add(UserCreateInput{Id:id,Password:v}).Exec(ctx);return err}},
 {"query",true,func(id int64,v string)error{_,err:=app.User.Where(UserWhere.Id.Eq(id)).Update().SetPassword(v).Exec(ctx);return err}},
 }
 for index,tc:=range cases{t.Run(tc.name,func(t *testing.T){
  id:=int64(index+1)
  if tc.existing{if _,err:=pg.Exec(ctx,db,"INSERT INTO users VALUES($1,'seed')",id);err!=nil{t.Fatal(err)}}
  if err:=tc.write(id,strings.Repeat("x",73));err==nil{t.Fatal("invalid hash succeeded")}
  count,err:=db.QueryScalar[int64](ctx,"SELECT count(*) FROM users WHERE id=$1",id);if err!=nil{t.Fatal(err)}
  if tc.existing{got,err:=db.QueryScalar[string](ctx,"SELECT password FROM users WHERE id=$1",id);if err!=nil||got!="seed"{t.Fatal(got,err)}}else if count!=0{t.Fatal("failed hash stored row")}
  if err:=tc.write(id," secret ");err!=nil{t.Fatal(err)}
  got,err:=db.QueryScalar[string](ctx,"SELECT password FROM users WHERE id=$1",id)
  if err!=nil||!luxocrypto.VerifyPassword("secret",got)||luxocrypto.VerifyPassword(" secret ",got){t.Fatal("missing, repeated or unordered hash",err)}
 })}
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "password": 2}}})
}

func TestGeneratedNullableHashMutations(t *testing.T) {
	runGeneratedFixture(t, `
model Credential @noTime { id: Int @id password: String? @hash optional: String? @hash @beforeSave { it.trim() } }
fn createNullable(value: String?): Credential { return Credential.create(id:1,password:value,optional:value) }
fn createLiteral(): Credential { return Credential.create(id:1,password:"secret",optional:" secret ") }
fn createNull(): Credential { return Credential.create(id:1,password:null,optional:null) }
fn updateNullable(row: Credential,value: String?): Int { return row.update(password:value,optional:value) }
fn queryNullable(value: String?): Int { Credential.where(id:1).update(password:value,optional:value) return 0 }
`, `package generated
import("strings";"testing";"os";"github.com/light-speak/luxo/pkg/lux/pg";luxocrypto "github.com/light-speak/luxo/pkg/lux/crypto")
func TestNullableHashFailureBeforeDatabase(t *testing.T){
 app:=NewFromDB(nil);value:=strings.Repeat("x",73)
 if _,err:=app.createNullable(t.Context(),&value);err==nil{t.Fatal("create accepted invalid password")}
 if _,err:=app.updateNullable(t.Context(),&Credential{Id:1},&value);err==nil{t.Fatal("update accepted invalid password")}
 if _,err:=app.queryNullable(t.Context(),&value);err==nil{t.Fatal("query accepted invalid password")}
 if value!=strings.Repeat("x",73){t.Fatal("source changed")}
}
func TestNullableHashDatabase(t *testing.T){
 connection:=os.Getenv("LUXO_TEST_DATABASE_URL");if connection==""{t.Skip("requires isolated LUXO_TEST_DATABASE_URL")}
 ctx:=t.Context();db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE credentials(id bigint PRIMARY KEY,password text,optional text)");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db)
 check:=func(null bool){t.Helper();row,err:=app.Credential.Where(CredentialWhere.Id.Eq(1)).FirstRequired(ctx);if err!=nil{t.Fatal(err)}
  if null {if row.Password!=nil||row.Optional!=nil{t.Fatal("null became hash")};return}
  if row.Password==nil||row.Optional==nil||!luxocrypto.VerifyPassword("secret",*row.Password)||!luxocrypto.VerifyPassword("secret",*row.Optional){t.Fatal("missing or repeated hash")}
 }
 cases:=[]struct{name string;write func()error;null bool}{
  {"createNull",func()error{_,err:=app.createNull(ctx);return err},true},
  {"createLiteral",func()error{_,err:=app.createLiteral(ctx);return err},false},
  {"createPointer",func()error{v:="secret";_,err:=app.createNullable(ctx,&v);if v!="secret"{t.Fatal("source changed")};return err},false},
 }
 for _,tc:=range cases{t.Run(tc.name,func(t *testing.T){
  if _,err:=pg.Exec(ctx,db,"DELETE FROM credentials");err!=nil{t.Fatal(err)}
  if err:=tc.write();err!=nil{t.Fatal(err)};check(tc.null)
  if _,err:=app.updateNullable(ctx,&Credential{Id:1},nil);err!=nil{t.Fatal(err)};check(true)
  v:="secret";if _,err:=app.updateNullable(ctx,&Credential{Id:1},&v);err!=nil{t.Fatal(err)};check(false)
  if _,err:=app.queryNullable(ctx,nil);err!=nil{t.Fatal(err)};check(true)
  if _,err:=app.queryNullable(ctx,&v);err!=nil{t.Fatal(err)};check(false)
 })}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Credential": {"id": 1, "password": 2, "optional": 3}}})
}

func TestGeneratedAutomaticOnlyBatch(t *testing.T) {
	runGeneratedFixture(t, `
model Record @noTime { id: UUID @id @auto }
model Timed { id: UUID @id @auto createdAt: DateTime updatedAt: DateTime }
`, `package generated
import("testing";"os";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/google/uuid")
func TestAutomaticOnlyInput(t *testing.T){
 batch:=(&RecordCreateManyBuilder{}).Add(RecordCreateInput{}).Add(RecordCreateInput{})
 if len(batch.rows)!=2{t.Fatal("lost empty inputs")}
}
func TestAutomaticOnlyDatabase(t *testing.T){
 connection:=os.Getenv("LUXO_TEST_DATABASE_URL");if connection==""{t.Skip("requires isolated LUXO_TEST_DATABASE_URL")}
 ctx:=t.Context();db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 for _,sql:=range []string{"CREATE TABLE records(id uuid PRIMARY KEY)","CREATE TABLE timeds(id uuid PRIMARY KEY,created_at timestamptz NOT NULL,updated_at timestamptz NOT NULL)"}{if _,err:=pg.Exec(ctx,db,sql);err!=nil{t.Fatal(err)}}
 app:=NewFromDB(db)
 empty,err:=app.Record.CreateMany().Exec(ctx);if err!=nil||len(empty)!=0{t.Fatal(empty,err)}
 rows,err:=app.Record.CreateMany().Add(RecordCreateInput{}).Add(RecordCreateInput{}).Exec(ctx)
 if err!=nil||len(rows)!=2{t.Fatal(rows,err)}
 if rows[0].Id==uuid.Nil||rows[1].Id==uuid.Nil||rows[0].Id==rows[1].Id{t.Fatal("invalid generated IDs")}
 timed,err:=app.Timed.CreateMany().Add(TimedCreateInput{}).Add(TimedCreateInput{}).Exec(ctx)
 if err!=nil||len(timed)!=2{t.Fatal(timed,err)}
 for _,row:=range timed{if row.Id==uuid.Nil||row.CreatedAt.IsZero()||!row.CreatedAt.Equal(row.UpdatedAt){t.Fatal("invalid automatic fields")}}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Record": {"id": 1}, "Timed": {"id": 1, "createdAt": 2, "updatedAt": 3}}})
}

func TestGeneratedDatabaseDefaultWrites(t *testing.T) {
	runGeneratedFixture(t, `model Sequence @noTime { id: Int @id @serial }`, `package generated
import("testing";"os";"context";"errors";"github.com/light-speak/luxo/pkg/lux/pg")
func TestDatabaseDefaultWrites(t *testing.T){
 connection:=os.Getenv("LUXO_TEST_DATABASE_URL");if connection==""{t.Skip("requires isolated LUXO_TEST_DATABASE_URL")}
 ctx:=t.Context();db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE sequences(id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY)");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db)
 t.Run("single",func(t *testing.T){row,err:=app.Sequence.Create().Exec(ctx);if err!=nil{t.Fatal(err)};if row.Id<=0{t.Fatal(row)}})
 t.Run("batch",func(t *testing.T){rows,err:=app.Sequence.CreateMany().Add(SequenceCreateInput{}).Add(SequenceCreateInput{}).Exec(ctx);if err!=nil{t.Fatal(err)};if len(rows)!=2||rows[0].Id<=0||rows[0].Id==rows[1].Id{t.Fatal(rows)}})
 t.Run("atomicFailure",func(t *testing.T){
  for _,sql:=range []string{"TRUNCATE sequences RESTART IDENTITY","ALTER TABLE sequences ADD CONSTRAINT only_first CHECK(id < 2)"}{if _,err:=pg.Exec(ctx,db,sql);err!=nil{t.Fatal(err)}}
  rows,err:=app.Sequence.CreateMany().Add(SequenceCreateInput{}).Add(SequenceCreateInput{}).Exec(ctx)
  if err==nil||len(rows)!=0{t.Fatal("partial success",rows,err)}
  count,err:=app.Sequence.Count(ctx);if err!=nil||count!=0{t.Fatal("partial commit",count,err)}
  if _,err:=pg.Exec(ctx,db,"ALTER TABLE sequences DROP CONSTRAINT only_first");err!=nil{t.Fatal(err)}
  row,err:=app.Sequence.Create().Exec(ctx);if err!=nil||row==nil{t.Fatal("connection did not recover",err)}
 })
 t.Run("cancelled",func(t *testing.T){
  before,err:=app.Sequence.Count(ctx);if err!=nil{t.Fatal(err)}
  cancelled,cancel:=context.WithCancel(ctx);cancel()
  rows,err:=app.Sequence.CreateMany().Add(SequenceCreateInput{}).Exec(cancelled)
  if !errors.Is(err,context.Canceled)||len(rows)!=0{t.Fatal(rows,err)}
  after,err:=app.Sequence.Count(ctx);if err!=nil||after!=before{t.Fatal("cancelled write committed",after,err)}
 })
}
`, StableIDs{ModelFields: map[string]map[string]int{"Sequence": {"id": 1}}})
}
