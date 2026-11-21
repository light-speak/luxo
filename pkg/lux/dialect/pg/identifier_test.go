package pg

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux"
)

func TestGeneratedIdentifierKeepsStableUTF8Names(t *testing.T) {
	for _, name := range []string{"idx_users_name", strings.Repeat("a", 63)} {
		if got := d().GeneratedIdentifier(name); got != name {
			t.Fatal("short generated name changed", got, name)
		}
	}
	for _, name := range []string{strings.Repeat("a", 64), strings.Repeat("界", 30)} {
		hash := sha256.Sum256([]byte(name))
		got := d().GeneratedIdentifier(name)
		if err := d().ValidateIdentifier(got); err != nil || !strings.HasSuffix(got, "_"+hex.EncodeToString(hash[:8])) {
			t.Fatal("generated name lost its stable hash or UTF-8 boundary", got, err)
		}
		if other := d().GeneratedIdentifier(name + "different"); got == other {
			t.Fatal("different full names collapsed to one truncated name", got)
		}
	}
}

func TestQuoteIdentifierPreservesOneIdentifier(t *testing.T) {
	for _, sample := range []struct{ raw, quoted string }{
		{"order", `"order"`}, {"User", `"User"`},
		{"tenant.orders", `"tenant.orders"`}, {"有 空格", `"有 空格"`},
		{`a"b`, `"a""b"`}, {`x';DROP TABLE users;--`, `"x';DROP TABLE users;--"`},
	} {
		if got := d().QuoteIdentifier(sample.raw); got != sample.quoted {
			t.Errorf("identifier %q: got %q, want %q", sample.raw, got, sample.quoted)
		}
	}
}

func TestValidateIdentifierBoundary(t *testing.T) {
	for _, name := range []string{"id", "order", `a"b`, "a.b", strings.Repeat("a", 63), strings.Repeat("界", 21)} {
		if err := d().ValidateIdentifier(name); err != nil {
			t.Errorf("valid identifier %q rejected: %v", name, err)
		}
	}
	for _, name := range []string{"", "a\x00b", string([]byte{0xff}), strings.Repeat("a", 64), strings.Repeat("界", 22)} {
		if err := d().ValidateIdentifier(name); err == nil {
			t.Errorf("unrepresentable identifier %q accepted", name)
		}
	}
}

func TestDDLQuotesNamesWithoutQuotingSQLExpressions(t *testing.T) {
	dialect := d()
	for _, sample := range []struct{ sql, expected string }{
		{dialect.ColumnDef("order", lux.ColumnInfo{Type: "BIGINT", Default: "42"}), `"order" BIGINT NOT NULL DEFAULT 42`},
		{dialect.AddColumn("table", `"order" BIGINT`), `ALTER TABLE "table" ADD COLUMN "order" BIGINT;`},
		{dialect.DropColumn("table", "order"), `ALTER TABLE "table" DROP COLUMN "order";`},
		{dialect.AlterColumnType("table", "order", "BIGINT"), `ALTER TABLE "table" ALTER COLUMN "order" TYPE BIGINT USING "order"::BIGINT;`},
		{dialect.SetNotNull("table", "order"), `ALTER TABLE "table" ALTER COLUMN "order" SET NOT NULL;`},
		{dialect.DropNotNull("table", "order"), `ALTER TABLE "table" ALTER COLUMN "order" DROP NOT NULL;`},
		{dialect.AddUnique("table", "order"), `ALTER TABLE "table" ADD CONSTRAINT "table_order_key" UNIQUE ("order");`},
		{dialect.DropUnique("table", "order"), `ALTER TABLE "table" DROP CONSTRAINT "table_order_key";`},
		{dialect.RenameUnique("table", "old", "order"), `ALTER TABLE "table" RENAME CONSTRAINT "table_old_key" TO "table_order_key";`},
		{dialect.DropIndex(`a"b`), `DROP INDEX IF EXISTS "a""b";`},
		{dialect.DropTable("table"), `DROP TABLE IF EXISTS "table";`},
	} {
		if !strings.Contains(sample.sql, sample.expected) {
			t.Errorf("DDL did not quote names: got %q, want fragment %q", sample.sql, sample.expected)
		}
	}
	for _, kind := range []lux.IndexKind{lux.BTreeIndex, lux.BrinIndex, lux.UniqueIndex, lux.CompositeIndex, lux.SearchIndex} {
		got := dialect.CreateTable("table", []lux.ColumnEntry{{Name: "order", Info: lux.ColumnInfo{Type: "TEXT"}}}, []lux.IndexInfo{{Name: "index", Columns: []string{"order"}, Kind: kind}})
		for _, fragment := range []string{`CREATE TABLE "table"`, `"order" TEXT`, `"index" ON "table"`, `"order"`} {
			if !strings.Contains(got, fragment) {
				t.Errorf("index kind %d: missing %q in %q", kind, fragment, got)
			}
		}
	}
}
