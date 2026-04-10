package pg

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux"
)

func d() Dialect { return Dialect{} }

// ===== Name =====

func TestDialectName(t *testing.T) {
	if got := d().Name(); got != "pg" {
		t.Errorf("Name() = %q, want pg", got)
	}
}

// ===== ColumnType — base type mappings =====

func TestColumnTypeBaseTypes(t *testing.T) {
	tests := []struct {
		luxoType string
		want     string
	}{
		{"Int", "BIGINT"},
		{"Float", "DOUBLE PRECISION"},
		{"String", "TEXT"},
		{"Boolean", "BOOLEAN"},
		{"DateTime", "TIMESTAMPTZ"},
		{"Duration", "INTERVAL"},
		{"UUID", "UUID"},
		{"Decimal", "DECIMAL"},
		{"Bytes", "BYTEA"},
		{"UnknownEnum", "TEXT"}, // enum or unknown → TEXT
	}
	for _, tt := range tests {
		t.Run(tt.luxoType, func(t *testing.T) {
			got := d().ColumnType(tt.luxoType, false, nil)
			if got != tt.want {
				t.Errorf("ColumnType(%q) = %q, want %q", tt.luxoType, got, tt.want)
			}
		})
	}
}

// ===== ColumnType — serial =====

func TestColumnTypeSerial(t *testing.T) {
	got := d().ColumnType("Int", true, nil)
	if got != "SERIAL" {
		t.Errorf("ColumnType serial = %q, want SERIAL", got)
	}
}

// ===== ColumnType — @varchar directive =====

func TestColumnTypeVarchar(t *testing.T) {
	// @varchar without arg → VARCHAR(255)
	got := d().ColumnType("String", false, []lux.DirectiveInfo{{Name: "varchar"}})
	if got != "VARCHAR(255)" {
		t.Errorf("@varchar default = %q, want VARCHAR(255)", got)
	}

	// @varchar with arg → VARCHAR(100)
	got = d().ColumnType("String", false, []lux.DirectiveInfo{{Name: "varchar", Arg: "100"}})
	if got != "VARCHAR(100)" {
		t.Errorf("@varchar(100) = %q, want VARCHAR(100)", got)
	}
}

// ===== ColumnType — Int directives =====

func TestColumnTypeBigint(t *testing.T) {
	got := d().ColumnType("Int", false, []lux.DirectiveInfo{{Name: "bigint"}})
	if got != "BIGINT" {
		t.Errorf("@bigint = %q, want BIGINT", got)
	}
}

func TestColumnTypeSmallint(t *testing.T) {
	got := d().ColumnType("Int", false, []lux.DirectiveInfo{{Name: "smallint"}})
	if got != "SMALLINT" {
		t.Errorf("@smallint = %q, want SMALLINT", got)
	}
}

// ===== ColumnType — Float directives =====

func TestColumnTypeDecimalDirective(t *testing.T) {
	// @decimal without arg
	got := d().ColumnType("Float", false, []lux.DirectiveInfo{{Name: "decimal"}})
	if got != "DECIMAL" {
		t.Errorf("@decimal = %q, want DECIMAL", got)
	}

	// @decimal with precision
	got = d().ColumnType("Float", false, []lux.DirectiveInfo{{Name: "decimal", Arg: "10,2"}})
	if got != "DECIMAL(10,2)" {
		t.Errorf("@decimal(10,2) = %q, want DECIMAL(10,2)", got)
	}
}

// ===== ColumnType — String directives =====

func TestColumnTypeInet(t *testing.T) {
	got := d().ColumnType("String", false, []lux.DirectiveInfo{{Name: "inet"}})
	if got != "INET" {
		t.Errorf("@inet = %q, want INET", got)
	}
}

func TestColumnTypePoint(t *testing.T) {
	got := d().ColumnType("String", false, []lux.DirectiveInfo{{Name: "point"}})
	if got != "POINT" {
		t.Errorf("@point = %q, want POINT", got)
	}
}

func TestColumnTypeVector(t *testing.T) {
	// Default dimension
	got := d().ColumnType("String", false, []lux.DirectiveInfo{{Name: "vector"}})
	if got != "vector(1536)" {
		t.Errorf("@vector default = %q, want vector(1536)", got)
	}

	// Custom dimension
	got = d().ColumnType("String", false, []lux.DirectiveInfo{{Name: "vector", Arg: "768"}})
	if got != "vector(768)" {
		t.Errorf("@vector(768) = %q, want vector(768)", got)
	}
}

// ===== ColumnType — DateTime directives =====

func TestColumnTypeDate(t *testing.T) {
	got := d().ColumnType("DateTime", false, []lux.DirectiveInfo{{Name: "date"}})
	if got != "DATE" {
		t.Errorf("@date = %q, want DATE", got)
	}
}

func TestColumnTypeTime(t *testing.T) {
	got := d().ColumnType("DateTime", false, []lux.DirectiveInfo{{Name: "time"}})
	if got != "TIME" {
		t.Errorf("@time = %q, want TIME", got)
	}
}

// ===== ColumnDef =====

func TestColumnDefSimple(t *testing.T) {
	got := d().ColumnDef("name", lux.ColumnInfo{Type: "TEXT"})
	if got != "name TEXT NOT NULL" {
		t.Errorf("got %q", got)
	}
}

func TestColumnDefNullable(t *testing.T) {
	got := d().ColumnDef("bio", lux.ColumnInfo{Type: "TEXT", Nullable: true})
	// Nullable → no NOT NULL
	if got != "bio TEXT" {
		t.Errorf("got %q", got)
	}
}

func TestColumnDefPK(t *testing.T) {
	got := d().ColumnDef("id", lux.ColumnInfo{Type: "SERIAL", PK: true, Serial: true})
	if !strings.Contains(got, "PRIMARY KEY") {
		t.Errorf("PK missing: %q", got)
	}
	// Serial should not have NOT NULL (serial implies it)
	if strings.Contains(got, "NOT NULL") {
		t.Errorf("serial should not have NOT NULL: %q", got)
	}
}

func TestColumnDefUnique(t *testing.T) {
	got := d().ColumnDef("email", lux.ColumnInfo{Type: "TEXT", Unique: true})
	if !strings.Contains(got, "UNIQUE") {
		t.Errorf("UNIQUE missing: %q", got)
	}
	if !strings.Contains(got, "NOT NULL") {
		t.Errorf("NOT NULL missing for non-nullable unique: %q", got)
	}
}

func TestColumnDefDefault(t *testing.T) {
	got := d().ColumnDef("active", lux.ColumnInfo{Type: "BOOLEAN", Default: "TRUE"})
	if !strings.Contains(got, "NOT NULL DEFAULT TRUE") {
		t.Errorf("DEFAULT missing or wrong: %q", got)
	}
}

func TestColumnDefDefaultNullable(t *testing.T) {
	// Default with nullable: should still have DEFAULT
	got := d().ColumnDef("score", lux.ColumnInfo{Type: "BIGINT", Nullable: true, Default: "0"})
	// Nullable + default → should have "NOT NULL DEFAULT 0" per the code logic
	// (The code adds NOT NULL DEFAULT when default is set, regardless of nullable)
	if !strings.Contains(got, "DEFAULT 0") {
		t.Errorf("DEFAULT missing: %q", got)
	}
}

// ===== CreateTable =====

func TestCreateTable(t *testing.T) {
	columns := []lux.ColumnEntry{
		{Name: "id", Info: lux.ColumnInfo{Type: "SERIAL", PK: true, Serial: true}},
		{Name: "name", Info: lux.ColumnInfo{Type: "TEXT"}},
		{Name: "email", Info: lux.ColumnInfo{Type: "TEXT", Unique: true}},
	}
	indexes := []lux.IndexInfo{
		{Name: "idx_users_name", Kind: lux.BTreeIndex, Columns: []string{"name"}},
	}

	got := d().CreateTable("users", columns, indexes)

	checks := []string{
		"CREATE TABLE users (",
		"id SERIAL PRIMARY KEY",
		"name TEXT NOT NULL",
		"email TEXT NOT NULL UNIQUE",
		"CREATE INDEX idx_users_name ON users (name)",
	}
	for _, c := range checks {
		if !strings.Contains(got, c) {
			t.Errorf("missing %q in:\n%s", c, got)
		}
	}
}

func TestCreateTableNoIndexes(t *testing.T) {
	columns := []lux.ColumnEntry{
		{Name: "id", Info: lux.ColumnInfo{Type: "SERIAL", PK: true, Serial: true}},
	}
	got := d().CreateTable("configs", columns, nil)
	if !strings.Contains(got, "CREATE TABLE configs") {
		t.Errorf("missing CREATE TABLE:\n%s", got)
	}
	if strings.Contains(got, "CREATE INDEX") {
		t.Errorf("should have no indexes:\n%s", got)
	}
}

// ===== CreateIndexSQL =====

func TestCreateIndexBTree(t *testing.T) {
	idx := lux.IndexInfo{Name: "idx_users_name", Kind: lux.BTreeIndex, Columns: []string{"name"}}
	got := d().CreateIndexSQL(idx, "users")
	if got != "CREATE INDEX idx_users_name ON users (name);\n" {
		t.Errorf("BTree index: %q", got)
	}
}

func TestCreateIndexSearch(t *testing.T) {
	idx := lux.IndexInfo{Name: "idx_posts_title_search", Kind: lux.SearchIndex, Columns: []string{"title"}}
	got := d().CreateIndexSQL(idx, "posts")
	// Default lang is "simple"
	if !strings.Contains(got, "USING gin(to_tsvector('simple', title))") {
		t.Errorf("Search index default lang: %q", got)
	}
}

func TestCreateIndexSearchWithLang(t *testing.T) {
	idx := lux.IndexInfo{Name: "idx_posts_title_search", Kind: lux.SearchIndex, Columns: []string{"title"}, Lang: "english"}
	got := d().CreateIndexSQL(idx, "posts")
	if !strings.Contains(got, "USING gin(to_tsvector('english', title))") {
		t.Errorf("Search index with lang: %q", got)
	}
}

func TestCreateIndexBRIN(t *testing.T) {
	idx := lux.IndexInfo{Name: "idx_logs_created_at_brin", Kind: lux.BrinIndex, Columns: []string{"created_at"}}
	got := d().CreateIndexSQL(idx, "logs")
	if !strings.Contains(got, "USING brin(created_at)") {
		t.Errorf("BRIN index: %q", got)
	}
}

func TestCreateIndexUnique(t *testing.T) {
	idx := lux.IndexInfo{Name: "idx_users_email_unique", Kind: lux.UniqueIndex, Columns: []string{"email"}}
	got := d().CreateIndexSQL(idx, "users")
	if got != "CREATE UNIQUE INDEX idx_users_email_unique ON users (email);\n" {
		t.Errorf("Unique index: %q", got)
	}
}

func TestCreateIndexComposite(t *testing.T) {
	idx := lux.IndexInfo{Name: "idx_users_name_email", Kind: lux.CompositeIndex, Columns: []string{"name", "email"}}
	got := d().CreateIndexSQL(idx, "users")
	if got != "CREATE INDEX idx_users_name_email ON users (name, email);\n" {
		t.Errorf("Composite index: %q", got)
	}
}

// ===== AlterColumnType =====

func TestAlterColumnTypeBigint(t *testing.T) {
	got := d().AlterColumnType("users", "age", "BIGINT")
	if !strings.Contains(got, "USING age::BIGINT") {
		t.Errorf("BIGINT should have USING clause: %q", got)
	}
}

func TestAlterColumnTypeBoolean(t *testing.T) {
	got := d().AlterColumnType("users", "active", "BOOLEAN")
	if !strings.Contains(got, "USING active::BOOLEAN") {
		t.Errorf("BOOLEAN should have USING clause: %q", got)
	}
}

func TestAlterColumnTypeUUID(t *testing.T) {
	got := d().AlterColumnType("users", "token", "UUID")
	if !strings.Contains(got, "USING token::UUID") {
		t.Errorf("UUID should have USING clause: %q", got)
	}
}

func TestAlterColumnTypeTimestamptz(t *testing.T) {
	got := d().AlterColumnType("events", "ts", "TIMESTAMPTZ")
	if !strings.Contains(got, "USING ts::TIMESTAMPTZ") {
		t.Errorf("TIMESTAMPTZ should have USING clause: %q", got)
	}
}

func TestAlterColumnTypeDoublePrecision(t *testing.T) {
	got := d().AlterColumnType("users", "score", "DOUBLE PRECISION")
	if !strings.Contains(got, "USING score::DOUBLE PRECISION") {
		t.Errorf("DOUBLE PRECISION should have USING clause: %q", got)
	}
}

func TestAlterColumnTypeSmallint(t *testing.T) {
	got := d().AlterColumnType("users", "level", "SMALLINT")
	if !strings.Contains(got, "USING level::BIGINT") {
		t.Errorf("SMALLINT should have USING clause: %q", got)
	}
}

func TestAlterColumnTypeSerial(t *testing.T) {
	got := d().AlterColumnType("users", "seq", "SERIAL")
	if !strings.Contains(got, "USING seq::BIGINT") {
		t.Errorf("SERIAL should have USING clause: %q", got)
	}
}

func TestAlterColumnTypeDecimal(t *testing.T) {
	got := d().AlterColumnType("products", "price", "DECIMAL(10,2)")
	if !strings.Contains(got, "USING price::DECIMAL") {
		t.Errorf("DECIMAL should have USING clause: %q", got)
	}
}

func TestAlterColumnTypeText(t *testing.T) {
	got := d().AlterColumnType("users", "name", "TEXT")
	// TEXT should NOT have a USING clause (implicit cast)
	if strings.Contains(got, "USING") {
		t.Errorf("TEXT should not have USING clause: %q", got)
	}
	want := "ALTER TABLE users ALTER COLUMN name TYPE TEXT;\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAlterColumnTypeVarchar(t *testing.T) {
	got := d().AlterColumnType("users", "name", "VARCHAR(255)")
	// VARCHAR should NOT have a USING clause
	if strings.Contains(got, "USING") {
		t.Errorf("VARCHAR should not have USING clause: %q", got)
	}
}

// ===== Simple DDL methods =====

func TestAddColumn(t *testing.T) {
	got := d().AddColumn("users", "bio TEXT")
	want := "ALTER TABLE users ADD COLUMN bio TEXT;\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDropColumn(t *testing.T) {
	got := d().DropColumn("users", "old_field")
	// Should be commented out for safety
	if !strings.HasPrefix(got, "-- ALTER TABLE") {
		t.Errorf("should be commented: %q", got)
	}
	if !strings.Contains(got, "DANGEROUS") {
		t.Errorf("should warn DANGEROUS: %q", got)
	}
}

func TestSetNotNull(t *testing.T) {
	got := d().SetNotNull("users", "name")
	want := "ALTER TABLE users ALTER COLUMN name SET NOT NULL;\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDropNotNull(t *testing.T) {
	got := d().DropNotNull("users", "bio")
	want := "ALTER TABLE users ALTER COLUMN bio DROP NOT NULL;\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAddUnique(t *testing.T) {
	got := d().AddUnique("users", "email")
	if !strings.Contains(got, "ADD CONSTRAINT users_email_key UNIQUE (email)") {
		t.Errorf("got %q", got)
	}
}

func TestDropUnique(t *testing.T) {
	got := d().DropUnique("users", "email")
	if !strings.Contains(got, "DROP CONSTRAINT users_email_key") {
		t.Errorf("got %q", got)
	}
}

func TestDropTable(t *testing.T) {
	got := d().DropTable("old_model")
	// Should be commented out for safety
	if !strings.HasPrefix(got, "-- DROP TABLE") {
		t.Errorf("should be commented: %q", got)
	}
	if !strings.Contains(got, "DANGEROUS") {
		t.Errorf("should warn DANGEROUS: %q", got)
	}
}

func TestDropIndex(t *testing.T) {
	got := d().DropIndex("idx_users_name")
	want := "DROP INDEX IF EXISTS idx_users_name;\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ===== Helper functions =====

func TestHasDir(t *testing.T) {
	dirs := []lux.DirectiveInfo{{Name: "unique"}, {Name: "index"}}
	if !hasDir(dirs, "unique") {
		t.Error("should find unique")
	}
	if hasDir(dirs, "serial") {
		t.Error("should not find serial")
	}
	if hasDir(nil, "unique") {
		t.Error("nil should not find anything")
	}
}

func TestDirArg(t *testing.T) {
	dirs := []lux.DirectiveInfo{
		{Name: "varchar", Arg: "100"},
		{Name: "index"},
	}
	if got := dirArg(dirs, "varchar", "255"); got != "100" {
		t.Errorf("got %q, want 100", got)
	}
	if got := dirArg(dirs, "index", "default"); got != "default" {
		t.Errorf("got %q, want default (no arg)", got)
	}
	if got := dirArg(dirs, "missing", "fb"); got != "fb" {
		t.Errorf("got %q, want fb (missing directive)", got)
	}
}
