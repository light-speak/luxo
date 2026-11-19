package codegen

import (
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux"
)

func TestGeneratedMigrationSQLRoundTripsPG(t *testing.T) {
	var assertions strings.Builder
	assertions.WriteString(migrationPGAssertions)
	for _, sample := range migrationPGStates() {
		initial, _, err := GenerateMigrationSQL(mustMigrationDiff(t, &SchemaState{Models: map[string]*ModelState{}}, sample.old, nil), sample.old, pgDialect())
		if err != nil {
			t.Fatal(err)
		}
		up, down, err := GenerateMigrationSQL(mustMigrationDiff(t, sample.old, sample.next, sample.fields), sample.next, pgDialect())
		if err != nil {
			t.Fatal(err)
		}
		assertions.WriteString("\nconst " + sample.name + "Initial = " + strconv.Quote(initial))
		assertions.WriteString("\nconst " + sample.name + "Up = " + strconv.Quote(up))
		assertions.WriteString("\nconst " + sample.name + "Down = " + strconv.Quote(down))
	}
	runGeneratedFixture(t, `model MarkerRow @noTime { id: Int @id }`, assertions.String(),
		StableIDs{ModelFields: map[string]map[string]int{"MarkerRow": {"id": 1}}})
}

type migrationPGState struct {
	name      string
	old, next *SchemaState
	fields    map[string]*FieldIDMap
}

func migrationPGStates() []migrationPGState {
	state := func(table, name string, column *ColumnState, indexes ...lux.IndexInfo) *SchemaState {
		return &SchemaState{Models: map[string]*ModelState{"Row": {Table: table,
			Columns: map[string]*ColumnState{"id": {Type: "BIGINT", PK: true}, name: column}, Indexes: indexes}}}
	}
	fields := map[string]*FieldIDMap{"Row": {OldByID: map[int]string{2: "name"}, NewByID: map[int]string{2: "value"}}}
	return append([]migrationPGState{
		{"typed", state("typed_rows", "name", &ColumnState{Type: "TEXT", Unique: true, Default: "'17'::text"}),
			state("typed_rows", "value", &ColumnState{Type: "BIGINT", Unique: true, Default: "42"}), fields},
		{"relaxed", state("relaxed_rows", "name", &ColumnState{Type: "TEXT", Unique: true}),
			state("relaxed_rows", "name", &ColumnState{Type: "BIGINT"}), nil},
		{"indexed", state("indexed_rows", "name", &ColumnState{Type: "TEXT"}, lux.IndexInfo{Name: "idx_indexed_name", Columns: []string{"name"}}),
			state("indexed_rows", "value", &ColumnState{Type: "TEXT"}, lux.IndexInfo{Name: "idx_indexed_value", Columns: []string{"value"}}), fields},
		{"added", &SchemaState{Models: map[string]*ModelState{"Row": {Table: "added_rows", Columns: map[string]*ColumnState{"id": {Type: "BIGINT", PK: true}}}}},
			state("added_rows", "name", &ColumnState{Type: "TEXT", Unique: true, Default: "'first'::text"}), nil},
		{"collision", state("collision_rows", "name", &ColumnState{Type: "TEXT", Unique: true}),
			state("collision_rows", "name", &ColumnState{Type: "TEXT", Unique: true}), nil},
	}, migrationGraphPGStates()...)
}

func migrationGraphPGStates() []migrationPGState {
	chainOld, chainNext := migrationRenameStates([]string{"b", "c"})
	swapOld, swapNext := migrationRenameStates([]string{"b", "a"})
	longOld, longNext := migrationRenameStates([]string{"b", "a"})
	reuseOld, reuseNext := migrationRenameStates([]string{"b"})
	reuseOld.Models["Row"].Table, reuseNext.Models["Row"].Table = "reuse_rows", "reuse_rows"
	reuseNext.Models["Row"].Columns["a"] = &ColumnState{Type: "BIGINT", Default: "7"}
	chainOld.Models["Row"].Table, chainNext.Models["Row"].Table = "chain_rows", "chain_rows"
	swapOld.Models["Row"].Table, swapNext.Models["Row"].Table = "swap_rows", "swap_rows"
	table := strings.Repeat("t", 40)
	longOld.Models["Row"].Table, longNext.Models["Row"].Table = table, table
	oldName, nextName := strings.Repeat("a", 32), strings.Repeat("b", 32)
	longOld.Models["Row"].Columns = map[string]*ColumnState{oldName: {Type: "TEXT", Unique: true}}
	longNext.Models["Row"].Columns = map[string]*ColumnState{nextName: {Type: "TEXT", Unique: true}}
	longOld.Models["Row"].Indexes = []lux.IndexInfo{{Name: pgDialect().GeneratedIdentifier("idx_" + table + "_" + oldName), Columns: []string{oldName}}}
	longNext.Models["Row"].Indexes = []lux.IndexInfo{{Name: pgDialect().GeneratedIdentifier("idx_" + table + "_" + nextName), Columns: []string{nextName}}}
	return []migrationPGState{
		{"chain", chainOld, chainNext, migrationRenameIDs([]string{"b", "c"})},
		{"swap", swapOld, swapNext, migrationRenameIDs([]string{"b", "a"})},
		{"reuse", reuseOld, reuseNext, migrationRenameIDs([]string{"b"})},
		{"long", longOld, longNext, map[string]*FieldIDMap{"Row": {OldByID: map[int]string{1: oldName}, NewByID: map[int]string{1: nextName}}}},
	}
}

const migrationPGAssertions = `package generated
import("strings";"testing";"github.com/light-speak/luxo/internal/testutil";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres")
func TestMigrationSQLPG(t *testing.T){
 container,err:=postgres.Run(t.Context(),testutil.PostgresImage(),postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies());testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 exec:=func(t *testing.T,sql string){t.Helper();if _,err:=pg.Exec(t.Context(),db,sql);err!=nil{t.Fatalf("SQL failed: %v\n%s",err,sql)}}
 t.Run("rename_type_default_unique_roundtrip",func(t *testing.T){
  exec(t,typedInitial);exec(t,"INSERT INTO typed_rows(id,name) VALUES(1,'17')");exec(t,typedUp)
  got,err:=pg.QueryScalar[int64](t.Context(),db,"SELECT value FROM typed_rows WHERE id=1");if err!=nil||got!=17{t.Fatal("type conversion lost a value",got,err)}
  exec(t,"INSERT INTO typed_rows(id) VALUES(2)");got,err=pg.QueryScalar[int64](t.Context(),db,"SELECT value FROM typed_rows WHERE id=2");if err!=nil||got!=42{t.Fatal("new default lost",got,err)}
  exec(t,typedDown)
  text,err:=pg.QueryScalar[string](t.Context(),db,"SELECT name FROM typed_rows WHERE id=1");if err!=nil||text!="17"{t.Fatal("rollback did not restore original column",text,err)}
  text,err=pg.QueryScalar[string](t.Context(),db,"SELECT column_default FROM information_schema.columns WHERE table_name='typed_rows' AND column_name='name'");if err!=nil||!strings.Contains(text,"17"){t.Fatal("original default not restored",text,err)}
  exists,err:=pg.QueryScalar[bool](t.Context(),db,"SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='typed_rows'::regclass AND conname='typed_rows_name_key' AND contype='u')");if err!=nil||!exists{t.Fatal("unique constraint identity not restored",exists,err)}
 })
 t.Run("remove_unique_before_lossy_cast",func(t *testing.T){
  exec(t,relaxedInitial);exec(t,"INSERT INTO relaxed_rows(id,name) VALUES(1,'1'),(2,'01')");exec(t,relaxedUp)
  count,err:=pg.QueryScalar[int64](t.Context(),db,"SELECT count(*) FROM relaxed_rows WHERE name=1");if err!=nil||count!=2{t.Fatal("obsolete unique constraint prevented requested conversion",count,err)}
 })
 t.Run("index_rename_roundtrip",func(t *testing.T){
  exec(t,indexedInitial);exec(t,"INSERT INTO indexed_rows VALUES(1,'original')");exec(t,indexedUp);exec(t,indexedDown)
  value,err:=pg.QueryScalar[string](t.Context(),db,"SELECT name FROM indexed_rows WHERE id=1");if err!=nil||value!="original"{t.Fatal(value,err)}
  exists,err:=pg.QueryScalar[bool](t.Context(),db,"SELECT to_regclass('idx_indexed_name') IS NOT NULL AND to_regclass('idx_indexed_value') IS NULL");if err!=nil||!exists{t.Fatal("index names not restored",exists,err)}
 })
 t.Run("add_unique_column_roundtrip",func(t *testing.T){
  exec(t,addedInitial);exec(t,"INSERT INTO added_rows(id) VALUES(1)");exec(t,addedUp)
  value,err:=pg.QueryScalar[string](t.Context(),db,"SELECT name FROM added_rows WHERE id=1");if err!=nil||value!="first"{t.Fatal("added default lost",value,err)}
  exists,err:=pg.QueryScalar[bool](t.Context(),db,"SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='added_rows'::regclass AND conname='added_rows_name_key' AND contype='u')");if err!=nil||!exists{t.Fatal("added column unique name lost",exists,err)}
  exec(t,addedDown);exists,err=pg.QueryScalar[bool](t.Context(),db,"SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='added_rows' AND column_name='name')");if err!=nil||exists{t.Fatal("added column was not rolled back",exists,err)}
 })
 t.Run("chained_identity_roundtrip",func(t *testing.T){
  exec(t,chainInitial);exec(t,"INSERT INTO chain_rows(a,b) VALUES('first','second')");exec(t,chainUp)
  value,err:=pg.QueryScalar[string](t.Context(),db,"SELECT b||','||c FROM chain_rows");if err!=nil||value!="first,second"{t.Fatal("chain changed identities",value,err)}
  exec(t,chainDown);value,err=pg.QueryScalar[string](t.Context(),db,"SELECT a||','||b FROM chain_rows");if err!=nil||value!="first,second"{t.Fatal("chain rollback changed identities",value,err)}
 })
 t.Run("cyclic_identity_roundtrip",func(t *testing.T){
  exec(t,swapInitial);exec(t,"INSERT INTO swap_rows(a,b) VALUES('first','second')");exec(t,swapUp)
  value,err:=pg.QueryScalar[string](t.Context(),db,"SELECT a||','||b FROM swap_rows");if err!=nil||value!="second,first"{t.Fatal("cycle changed identities",value,err)}
  exec(t,swapDown);value,err=pg.QueryScalar[string](t.Context(),db,"SELECT a||','||b FROM swap_rows");if err!=nil||value!="first,second"{t.Fatal("cycle rollback changed identities",value,err)}
 })
 t.Run("reused_column_name_roundtrip",func(t *testing.T){
  exec(t,reuseInitial);exec(t,"INSERT INTO reuse_rows(a) VALUES('original')");exec(t,reuseUp)
  value,err:=pg.QueryScalar[string](t.Context(),db,"SELECT b||','||a::text FROM reuse_rows");if err!=nil||value!="original,7"{t.Fatal("reused name overwrote original identity",value,err)}
  exec(t,reuseDown);value,err=pg.QueryScalar[string](t.Context(),db,"SELECT a FROM reuse_rows");if err!=nil||value!="original"{t.Fatal("reused name rollback lost original",value,err)}
 })
 t.Run("generated_long_names_roundtrip",func(t *testing.T){
  table:=strings.Repeat("t",40);oldName,nextName:=strings.Repeat("a",32),strings.Repeat("b",32)
  exec(t,longInitial);exec(t,"INSERT INTO "+table+"("+oldName+") VALUES('original')");exec(t,longUp)
  value,err:=pg.QueryScalar[string](t.Context(),db,"SELECT "+nextName+" FROM "+table);if err!=nil||value!="original"{t.Fatal("long rename",value,err)}
  exec(t,longDown);value,err=pg.QueryScalar[string](t.Context(),db,"SELECT "+oldName+" FROM "+table);if err!=nil||value!="original"{t.Fatal("long rollback",value,err)}
 })
 t.Run("unique_name_collision_fails_without_alias",func(t *testing.T){
  exec(t,"CREATE TABLE external_owner(id bigint);CREATE INDEX collision_rows_name_key ON external_owner(id)")
  if _,err:=pg.Exec(t.Context(),db,collisionInitial);err==nil{t.Fatal("CREATE silently chose another unique constraint name")}
  absent,err:=pg.QueryScalar[bool](t.Context(),db,"SELECT to_regclass('collision_rows') IS NULL");if err!=nil||!absent{t.Fatal("failed named-constraint creation left a table",absent,err)}
 })
}
`
