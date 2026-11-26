package codegen

import (
	"os"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestGeneratedEnumScannerRejectsUnknownValues(t *testing.T) {
	runGeneratedFixture(t, `
enum Role { USER ADMIN }
model Account @noTime { id: Int @id role: Role optionalRole: Role? roles: [Role] }
`, `package generated
import("errors";"testing";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/jackc/pgx/v5/pgconn";"github.com/jackc/pgx/v5/pgtype")
type enumScanRows struct{pg.Rows; field pgconn.FieldDescription; raw []byte; mapping *pgtype.Map}
func(r *enumScanRows)FieldDescriptions()[]pgconn.FieldDescription{return []pgconn.FieldDescription{r.field}}
func(r *enumScanRows)Scan(dest ...any)error{return r.mapping.Scan(r.field.DataTypeOID,r.field.Format,r.raw,dest[0])}
func TestEnumStorageBoundary(t *testing.T){
 cases:=[]struct{name,column string;oid uint32;value any;invalid bool}{
  {"known","role",pgtype.TextOID,"ADMIN",false},
  {"unknown","role",pgtype.TextOID,"UNKNOWN",true},
  {"empty","role",pgtype.TextOID,"",true},
  {"nullableNull","optional_role",pgtype.TextOID,nil,false},
  {"nullableKnown","optional_role",pgtype.TextOID,"USER",false},
  {"nullableUnknown","optional_role",pgtype.TextOID,"UNKNOWN",true},
  {"emptyList","roles",pgtype.TextArrayOID,[]string{},false},
  {"knownList","roles",pgtype.TextArrayOID,[]string{"USER","ADMIN"},false},
  {"unknownList","roles",pgtype.TextArrayOID,[]string{"USER","UNKNOWN"},true},
  {"unselected","id",pgtype.Int8OID,int64(42),false},
 }
 for _,format:=range []int16{pgtype.TextFormatCode,pgtype.BinaryFormatCode}{
  for _,tc:=range cases{t.Run(tc.name,func(t *testing.T){
   mapping:=pgtype.NewMap();raw,err:=mapping.Encode(tc.oid,format,tc.value,make([]byte,0));if err!=nil{t.Fatal(err)}
   got,err:=scanAccount(&enumScanRows{field:pgconn.FieldDescription{Name:tc.column,DataTypeOID:tc.oid,Format:format},raw:raw,mapping:mapping})
   if tc.invalid{if !errors.Is(err,codec.ErrInvalidEnum)||got!=nil{t.Fatalf("invalid stored enum accepted: value=%+v err=%v",got,err)};return}
   if err!=nil||got==nil{t.Fatalf("legal projection rejected: value=%+v err=%v",got,err)}
   switch tc.name{case "known":if got.Role!=RoleADMIN{t.Fatal(got)};case "nullableNull":if got.OptionalRole!=nil{t.Fatal(got)};case "nullableKnown":if got.OptionalRole==nil||*got.OptionalRole!=RoleUSER{t.Fatal(got)};case "knownList":if len(got.Roles)!=2||got.Roles[0]!=RoleUSER||got.Roles[1]!=RoleADMIN{t.Fatal(got)};case "unselected":if got.Id!=42{t.Fatal(got)}}
  })}
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"Account": {"id": 1, "role": 2, "optionalRole": 3, "roles": 4}}})
}

func TestGeneratedEnumScannerBenchmark(t *testing.T) {
	model := &ast.ModelDecl{Name: "Account", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "role", Type: &ast.TypeRef{Name: "Role"}},
		{Name: "optional", Type: &ast.TypeRef{Name: "Role", Nullable: true}},
		{Name: "roles", Type: &ast.TypeRef{Name: "Role", IsList: true}},
	}}
	var source, before strings.Builder
	source.WriteString("package generated\nimport(pg \"github.com/light-speak/luxo/pkg/lux/pg\";\"github.com/light-speak/luxo/pkg/lux/codec\")\ntype Account struct{Id int64;Role Role;Optional *Role;Roles []Role}\n")
	generateEnum(&source, &ast.EnumDecl{Name: "Role", Values: []string{"USER", "ADMIN"}})
	generateScanner(&source, model, map[string]bool{"Role": true})
	generateScanner(&before, model, nil)
	source.WriteString(strings.Replace(before.String(), "func scanAccount(", "func scanAccountPrevious(", 1))
	runGeneratedProtocolPackage(t, source.String(), `package generated
import("fmt";"testing";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/jackc/pgx/v5/pgconn";"github.com/jackc/pgx/v5/pgtype")
type enumRows struct{pg.Rows;field pgconn.FieldDescription;raw []byte;mapping *pgtype.Map}
func(r *enumRows)FieldDescriptions()[]pgconn.FieldDescription{return []pgconn.FieldDescription{r.field}}
func(r *enumRows)Scan(dest ...any)error{return r.mapping.Scan(r.field.DataTypeOID,r.field.Format,r.raw,dest[0])}
func enumRow(b testing.TB,column string,oid uint32,value any)*enumRows{
 b.Helper();mapping:=pgtype.NewMap();raw,err:=mapping.Encode(oid,pgtype.BinaryFormatCode,value,make([]byte,0));if err!=nil{b.Fatal(err)}
 return &enumRows{field:pgconn.FieldDescription{Name:column,DataTypeOID:oid,Format:pgtype.BinaryFormatCode},raw:raw,mapping:mapping}
}
func TestBaseline(t *testing.T){
 for _,value:=range []string{"","UNKNOWN"}{
  row:=enumRow(t,"role",pgtype.TextOID,value)
  old,err:=scanAccountPrevious(row);if err!=nil||old==nil||string(old.Role)!=value{t.Fatal("baseline failed to reproduce",old,err)}
  got,err:=scanAccount(row);if err==nil||got!=nil{t.Fatal("invalid enum escaped",got,err)}
 }
}
var enumScannerSink *Account
func BenchmarkEnumScanner(b *testing.B){
 values:=make([]string,128);for i:=range values{values[i]="USER"}
 for _,sample:=range []struct{name,column string;oid uint32;value any}{
  {"Scalar","role",pgtype.TextOID,"ADMIN"},{"NullableNull","optional",pgtype.TextOID,nil},
  {"List128","roles",pgtype.TextArrayOID,values},{"Unselected","id",pgtype.Int8OID,int64(42)},
 }{
  for _,previous:=range []bool{true,false}{
   scan:=scanAccount;if previous{scan=scanAccountPrevious};row:=enumRow(b,sample.column,sample.oid,sample.value)
   for range 100{if _,err:=scan(row);err!=nil{b.Fatal(err)}}
   b.Run(fmt.Sprintf("%s/previous=%v",sample.name,previous),func(b *testing.B){b.ReportAllocs();for b.Loop(){var err error;enumScannerSink,err=scan(row);if err!=nil{b.Fatal(err)}}})
  }
 }
}
`)
}

func TestGeneratedJSONScannerStorage(t *testing.T) {
	runGeneratedFixture(t, `
model Document @noTime {
 id: Int @id
 data: JSON
 optional: JSON?
 entries: [JSON]
}
`, `package generated
import("bytes";"encoding/json";"os";"strings";"testing";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/schema")
func TestJSONStorage(t *testing.T) {
 connection:=os.Getenv("LUXO_TEST_DATABASE_URL");if connection==""{t.Skip("requires isolated LUXO_TEST_DATABASE_URL")}
 ctx:=t.Context();db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 d:=pg.Dialect{}
 ddl:="CREATE TABLE generated_json(id BIGINT PRIMARY KEY,data "+d.ColumnType("JSON",false,nil)+",optional "+d.ColumnType("JSON",false,nil)+",entries "+d.ArrayColumnType("JSON")+")"
 if _,err:=pg.Exec(ctx,db,ddl);err!=nil{t.Fatal(err)}
 value:=json.RawMessage("123456789012345678901234567890")
 list:=[]json.RawMessage{value,json.RawMessage("null")}
 metadata:=schema.New();if err:=RegisterSchema(metadata);err!=nil{t.Fatal(err)}
 mask,err:=codec.AppendSelectionMask(nil,[]byte{4},nil);if err!=nil{t.Fatal(err)}
 var absentBinary []byte
 for _,literalNull:=range []bool{false,true}{
  var optional *json.RawMessage
  if literalNull{raw:=json.RawMessage("null");optional=&raw}
  id:=int64(1);if literalNull{id=2}
  if _,err:=pg.Exec(ctx,db,"INSERT INTO generated_json VALUES($1,$2,$3,$4)",id,value,optional,list);err!=nil{t.Fatal(err)}
  sqlNull,err:=db.QueryScalar[bool](ctx,"SELECT optional IS NULL FROM generated_json WHERE id=$1",id)
  if err!=nil||sqlNull==literalNull{t.Fatalf("stored null differs before scanning: literal=%v SQLNULL=%v error=%v",literalNull,sqlNull,err)}
  got,err:=pg.QueryRow(ctx,db,scanDocument,"SELECT id,data,optional,entries FROM generated_json WHERE id=$1",id)
  if err!=nil||got==nil{t.Fatalf("scan: %+v %v",got,err)}
  if got.Id!=id||!strings.Contains(string(got.Data),string(value))||len(got.Entries)!=2||string(got.Entries[1])!="null"{t.Fatalf("wrong fields: %+v",got)}
  if literalNull {
   if got.Optional==nil||string(*got.Optional)!="null"{t.Fatalf("JSON null confused with SQL NULL: %+v",got.Optional)}
  }else if got.Optional!=nil{t.Fatalf("SQL NULL became present: %s",*got.Optional)}
  out:=&api.ResponseBuf{};if err:=got.WriteLuxo(out,mask);err!=nil{t.Fatal(err)}
  var decoded Document;dec:=codec.NewDecoder(out.B);decoded.ReadLuxo(dec);if err:=dec.Finish();err!=nil{t.Fatal(err)}
  if decoded.Optional!=nil{t.Fatal("public nullable JSON was not normalized")}
  if literalNull {
   if !bytes.Equal(absentBinary,out.B){t.Fatal("public Binary differs for the two stored null states")}
   if got.Optional==nil||string(*got.Optional)!="null"{t.Fatal("public output mutated stored JSON null")}
  }else{absentBinary=bytes.Clone(out.B)}
  text,err:=schema.BinaryToJSON(nil,out.B,metadata.Models["Document"],metadata)
  if err!=nil||string(text)!="{\"optional\":null}"{t.Fatalf("JSON projection=%s error=%v",text,err)}
 }
 t.Run("non-null JSON rejects SQL NULL",func(t *testing.T){
  got,err:=pg.QueryRow(ctx,db,scanDocument,"SELECT NULL::jsonb AS data")
  if err==nil||got!=nil{t.Fatalf("SQL NULL entered required JSON: %#v %v",got,err)}
 })
 t.Run("non-null JSON preserves literal null",func(t *testing.T){
  got,err:=pg.QueryRow(ctx,db,scanDocument,"SELECT 'null'::jsonb AS data")
  if err!=nil||got==nil||string(got.Data)!="null"{t.Fatal(got,err)}
 })
}
`, StableIDs{ModelFields: map[string]map[string]int{"Document": {"id": 1, "data": 2, "optional": 3, "entries": 4}}})
}

func TestGeneratedNullableJSONArrayStorage(t *testing.T) {
	runGeneratedFixture(t, `
model Document @noTime { id: Int @id entries: [JSON]? }
`, `package generated
import("encoding/json";"os";"testing";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/schema")
func TestNullableJSONArrayStorage(t *testing.T){
 connection:=os.Getenv("LUXO_TEST_DATABASE_URL");if connection==""{t.Skip("requires isolated LUXO_TEST_DATABASE_URL")}
 ctx:=t.Context();db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE nullable_json_arrays(id bigint PRIMARY KEY,entries jsonb[])");err!=nil{t.Fatal(err)}
 metadata:=schema.New();if err:=RegisterSchema(metadata);err!=nil{t.Fatal(err)}
 mask,err:=codec.AppendSelectionMask(nil,[]byte{2},nil);if err!=nil{t.Fatal(err)}
 for i,tc:=range []struct{name string;value []json.RawMessage;want string}{
  {"SQL NULL",nil,"{\"entries\":null}"},
  {"empty",[]json.RawMessage{},"{\"entries\":[]}"},
  {"JSON null element",[]json.RawMessage{json.RawMessage("null")},"{\"entries\":[null]}"},
 }{t.Run(tc.name,func(t *testing.T){
  id:=int64(i+1)
  if _,err:=pg.Exec(ctx,db,"INSERT INTO nullable_json_arrays VALUES($1,$2)",id,tc.value);err!=nil{t.Fatal(err)}
  isNull,err:=db.QueryScalar[bool](ctx,"SELECT entries IS NULL FROM nullable_json_arrays WHERE id=$1",id)
  if err!=nil||isNull!=(tc.value==nil){t.Fatal(isNull,err)}
  value,err:=pg.QueryRow(ctx,db,scanDocument,"SELECT id,entries FROM nullable_json_arrays WHERE id=$1",id)
  if err!=nil||value==nil{t.Fatal(value,err)}
  check:=func(entries []json.RawMessage){t.Helper();if (entries==nil)!=(tc.value==nil)||len(entries)!=len(tc.value){t.Fatalf("lost array presence: %#v",entries)};if len(entries)>0&&string(entries[0])!="null"{t.Fatalf("lost JSON null element: %q",entries[0])}}
  check(value.Entries)
  output:=&api.ResponseBuf{};if err:=value.WriteLuxo(output,mask);err!=nil{t.Fatal(err)}
  var decoded Document;reader:=codec.NewDecoder(output.B);decoded.ReadLuxo(reader);if err:=reader.Finish();err!=nil{t.Fatal(err)}
  check(decoded.Entries)
  raw,err:=schema.BinaryToJSON(nil,output.B,metadata.Models["Document"],metadata)
  if err!=nil||string(raw)!=tc.want{t.Fatalf("JSON=%s want=%s err=%v",raw,tc.want,err)}
 })}
 t.Run("SQL NULL element rejected",func(t *testing.T){
  if _,err:=pg.Exec(ctx,db,"INSERT INTO nullable_json_arrays VALUES(4,ARRAY[NULL::jsonb])");err!=nil{t.Fatal(err)}
  value,err:=pg.QueryRow(ctx,db,scanDocument,"SELECT id,entries FROM nullable_json_arrays WHERE id=4")
  if err==nil||value!=nil{t.Fatalf("SQL NULL element entered non-null JSON element: value=%+v err=%v",value,err)}
 })
}
`, StableIDs{ModelFields: map[string]map[string]int{"Document": {"id": 1, "entries": 2}}})
}

func TestGeneratedScannerOwnsMutableDatabaseValues(t *testing.T) {
	runGeneratedFixture(t, `
model Document @noTime {
 id: Int @id
 data: JSON
 optional: JSON?
 body: Bytes
 optionalBody: Bytes?
 entries: [JSON]
}
`, `package generated
import("bytes";"encoding/json";"fmt";"testing";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/jackc/pgx/v5/pgconn";"github.com/jackc/pgx/v5/pgtype")
type ownedRows struct { pg.Rows; fields []pgconn.FieldDescription; data [][]byte; mapping *pgtype.Map }
func(r *ownedRows)FieldDescriptions()[]pgconn.FieldDescription{return r.fields}
func(r *ownedRows)Scan(dest ...any)error{
 for i,f:=range r.fields{if err:=r.mapping.Scan(f.DataTypeOID,f.Format,r.data[i],dest[i]);err!=nil{return err}};return nil
}
func TestOwnership(t *testing.T){
 for _,format:=range []int16{pgtype.TextFormatCode,pgtype.BinaryFormatCode}{t.Run(fmt.Sprint(format),func(t *testing.T){
  mapping:=pgtype.NewMap()
  raw:=json.RawMessage("{\"value\":123}")
  body:=[]byte{0,1,2,255}
  rows:=&ownedRows{mapping:mapping}
  columns:=[]struct{name string;oid uint32;value any}{
   {"data",pgtype.JSONBOID,raw},{"optional",pgtype.JSONBOID,raw},
   {"body",pgtype.ByteaOID,body},{"optional_body",pgtype.ByteaOID,body},
   {"entries",pgtype.JSONBArrayOID,[]json.RawMessage{raw,json.RawMessage("null")}},
  }
  for _,column:=range columns{
   encoded,err:=mapping.Encode(column.oid,format,column.value,nil);if err!=nil{t.Fatal(err)}
   rows.fields=append(rows.fields,pgconn.FieldDescription{Name:column.name,DataTypeOID:column.oid,Format:format})
   rows.data=append(rows.data,encoded)
  }
  first,err:=scanDocument(rows);if err!=nil{t.Fatal(err)}
  second,err:=scanDocument(rows);if err!=nil{t.Fatal(err)}
  for _,data:=range rows.data{clear(data)}
  check:=func(value *Document){
   t.Helper()
   if !bytes.Equal(value.Data,raw)||value.Optional==nil||!bytes.Equal(*value.Optional,raw){t.Fatal("JSON aliases driver or another model")}
   if !bytes.Equal(value.Body,body)||value.OptionalBody==nil||!bytes.Equal(*value.OptionalBody,body){t.Fatal("Bytes aliases driver or another model")}
   if len(value.Entries)!=2||!bytes.Equal(value.Entries[0],raw)||string(value.Entries[1])!="null"{t.Fatal("JSON array aliases driver or another model")}
  }
  check(first);check(second)
  clear(first.Data);clear(*first.Optional);clear(first.Body);clear(*first.OptionalBody);clear(first.Entries[0]);clear(first.Entries[1])
  check(second)
 })}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Document": {"id": 1, "data": 2, "optional": 3, "body": 4, "optionalBody": 5, "entries": 6}}})
}

func TestGeneratedScannerRejectsInvalidBusinessScalars(t *testing.T) {
	runGeneratedFixture(t, `
model Reading @noTime {
 id: Int @id
 amount: Float
 optionalAmount: Float?
 amounts: [Float]
 at: DateTime
 optionalAt: DateTime?
 optionalAmounts: [Float]?
 times: [DateTime]
 optionalTimes: [DateTime]?
}
`, `package generated
import("fmt";"math";"testing";"time";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/jackc/pgx/v5/pgconn";"github.com/jackc/pgx/v5/pgtype")
type scalarRows struct {pg.Rows; field pgconn.FieldDescription; raw []byte; mapping *pgtype.Map}
func(r *scalarRows)FieldDescriptions()[]pgconn.FieldDescription{return []pgconn.FieldDescription{r.field}}
func(r *scalarRows)Scan(dest ...any)error{return r.mapping.Scan(r.field.DataTypeOID,r.field.Format,r.raw,dest[0])}
func TestScalarBoundaries(t *testing.T){
 for _,format:=range []int16{pgtype.TextFormatCode,pgtype.BinaryFormatCode}{
  for _,sample:=range []struct{name string;oid uint32;value any;valid bool}{
   {"amount",pgtype.Float8OID,math.NaN(),false},
   {"amount",pgtype.Float8OID,math.Inf(1),false},
   {"optional_amount",pgtype.Float8OID,math.Inf(-1),false},
   {"amounts",pgtype.Float8ArrayOID,[]float64{1,math.NaN()},false},
   {"amount",pgtype.Float8OID,math.MaxFloat64,true},
   {"amount",pgtype.Float8OID,math.Copysign(0,-1),true},
   {"optional_amount",pgtype.Float8OID,42.5,true},
   {"optional_amounts",pgtype.Float8ArrayOID,[]float64{math.Inf(1)},false},
   {"optional_amounts",pgtype.Float8ArrayOID,nil,true},
   {"optional_amounts",pgtype.Float8ArrayOID,[]float64{},true},
   {"discarded",pgtype.Float8OID,math.NaN(),true},
   {"optional_amount",pgtype.Float8OID,nil,true},
   {"amounts",pgtype.Float8ArrayOID,[]float64{0,math.SmallestNonzeroFloat64},true},
   {"at",pgtype.TimestamptzOID,time.Date(10000,1,1,0,0,0,0,time.UTC),false},
   {"optional_at",pgtype.TimestamptzOID,time.Date(-1,1,1,0,0,0,0,time.UTC),false},
   {"at",pgtype.TimestamptzOID,time.Date(0,1,1,0,0,0,1,time.UTC),true},
   {"optional_at",pgtype.TimestamptzOID,nil,true},
   {"optional_at",pgtype.TimestamptzOID,time.Date(9999,12,31,23,59,59,0,time.UTC),true},
   {"times",pgtype.TimestamptzArrayOID,[]time.Time{time.Date(-1,1,1,0,0,0,0,time.UTC)},false},
   {"optional_times",pgtype.TimestamptzArrayOID,[]time.Time{time.Date(10000,1,1,0,0,0,0,time.UTC)},false},
   {"optional_times",pgtype.TimestamptzArrayOID,nil,true},
   {"times",pgtype.TimestamptzArrayOID,[]time.Time{time.Date(0,1,1,0,0,0,0,time.UTC)},true},
  }{t.Run(fmt.Sprintf("%d/%s/%v",format,sample.name,sample.value),func(t *testing.T){
   mapping:=pgtype.NewMap();raw,err:=mapping.Encode(sample.oid,format,sample.value,nil);if err!=nil{t.Fatal(err)}
   rows:=&scalarRows{field:pgconn.FieldDescription{Name:sample.name,DataTypeOID:sample.oid,Format:format},raw:raw,mapping:mapping}
   value,err:=scanReading(rows)
   if sample.valid&&err!=nil{t.Fatalf("valid value rejected: %v",err)}
   if !sample.valid&&err==nil{t.Fatalf("invalid database value reached business model: %+v",value)}
   if !sample.valid&&value!=nil{t.Fatalf("partial invalid model returned: %+v",value)}
   if sample.name=="amount"&&sample.valid&&math.Float64bits(value.Amount)!=math.Float64bits(sample.value.(float64)){t.Fatal("float bits changed")}
   if sample.name=="optional_amount"&&sample.value!=nil&&sample.valid&&(value.OptionalAmount==nil||*value.OptionalAmount!=42.5){t.Fatal("present optional lost")}
  })}
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"Reading": {"id": 1, "amount": 2, "optionalAmount": 3, "amounts": 4, "at": 5, "optionalAt": 6, "optionalAmounts": 7, "times": 8, "optionalTimes": 9}}})
}

func TestGeneratedScannerDurationStorage(t *testing.T) {
	for _, kind := range []string{"model", "extend"} {
		t.Run(kind, func(t *testing.T) {
			source := durationStorageModel
			if kind == "extend" {
				source = strings.Replace(source, "model Reading @noTime", "extend Reading", 1)
				source = strings.Replace(source, " id: Int @id\n", "", 1)
			}
			runGeneratedFixture(t, source, durationStorageAssertions, durationStorageIDs())
		})
	}
}

const durationStorageModel = `model Reading @noTime {
 id: Int @id
 elapsed: Duration
 optionalElapsed: Duration?
 laps: [Duration]
 optionalLaps: [Duration]?
 at: DateTime
 amount: Decimal
}`

const durationStorageAssertions = `package generated
import("fmt";"math";"math/big";"slices";"testing";"time";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/jackc/pgx/v5/pgconn";"github.com/jackc/pgx/v5/pgtype";"github.com/shopspring/decimal")
type durationRows struct{pg.Rows;field pgconn.FieldDescription;raw []byte;mapping *pgtype.Map}
func(r *durationRows)FieldDescriptions()[]pgconn.FieldDescription{return []pgconn.FieldDescription{r.field}}
func(r *durationRows)Scan(dest ...any)error{return r.mapping.Scan(r.field.DataTypeOID,r.field.Format,r.raw,dest[0])}
func TestDurationStorage(t *testing.T){
 value:=pg.DurationValue{Microseconds:1234,Valid:true};zero:=pg.DurationValue{Valid:true}
 at:=time.Date(2026,1,1,0,0,0,123456000,time.UTC)
 for _,format:=range []int16{pgtype.TextFormatCode,pgtype.BinaryFormatCode}{for _,sample:=range []struct{
  name,column string;oid uint32;value any;valid bool;want time.Duration;list []time.Duration
 }{
  {"scalar","elapsed",pgtype.IntervalOID,value,true,1234*time.Microsecond,nil},
  {"days","elapsed",pgtype.IntervalOID,pg.DurationValue{Days:2,Microseconds:-1,Valid:true},true,48*time.Hour-time.Microsecond,nil},
  {"negative","optional_elapsed",pgtype.IntervalOID,pg.DurationValue{Microseconds:-1000,Valid:true},true,-time.Millisecond,nil},
  {"nullable_null","optional_elapsed",pgtype.IntervalOID,nil,true,0,nil},
  {"list","laps",pgtype.IntervalArrayOID,[]pg.DurationValue{value,zero},true,0,[]time.Duration{1234*time.Microsecond,0}},
  {"empty_list","laps",pgtype.IntervalArrayOID,[]pg.DurationValue{},true,0,[]time.Duration{}},
  {"nullable_list","optional_laps",pgtype.IntervalArrayOID,[]pg.DurationValue{value},true,0,[]time.Duration{1234*time.Microsecond}},
  {"nullable_list_null","optional_laps",pgtype.IntervalArrayOID,nil,true,0,nil},
  {"calendar_month","elapsed",pgtype.IntervalOID,pg.DurationValue{Months:1,Valid:true},false,0,nil},
  {"overflow","elapsed",pgtype.IntervalOID,pg.DurationValue{Microseconds:math.MaxInt64,Valid:true},false,0,nil},
  {"nullable_month","optional_elapsed",pgtype.IntervalOID,pg.DurationValue{Months:1,Valid:true},false,0,nil},
  {"nullable_overflow","optional_elapsed",pgtype.IntervalOID,pg.DurationValue{Microseconds:math.MinInt64,Valid:true},false,0,nil},
  {"list_month","laps",pgtype.IntervalArrayOID,[]pg.DurationValue{value,{Months:1,Valid:true}},false,0,nil},
  {"list_overflow","laps",pgtype.IntervalArrayOID,[]pg.DurationValue{value,{Microseconds:math.MaxInt64,Valid:true}},false,0,nil},
  {"nullable_list_month","optional_laps",pgtype.IntervalArrayOID,[]pg.DurationValue{{Months:1,Valid:true}},false,0,nil},
  {"nullable_list_overflow","optional_laps",pgtype.IntervalArrayOID,[]pg.DurationValue{{Microseconds:math.MinInt64,Valid:true}},false,0,nil},
  {"upper_boundary","elapsed",pgtype.IntervalOID,pg.DurationValue{Microseconds:math.MaxInt64/1000,Valid:true},true,time.Duration(math.MaxInt64/1000*1000),nil},
  {"lower_boundary","elapsed",pgtype.IntervalOID,pg.DurationValue{Microseconds:math.MinInt64/1000,Valid:true},true,time.Duration(math.MinInt64/1000*1000),nil},
  {"cancellation","elapsed",pgtype.IntervalOID,pg.DurationValue{Days:106751992,Microseconds:math.MinInt64,Valid:true},true,time.Duration((106751992*86400000000-9223372036854775808)*1000),nil},
  {"required_null","elapsed",pgtype.IntervalOID,nil,false,0,nil},
  {"list_null_element","laps",pgtype.IntervalArrayOID,[]pg.DurationValue{value,{}},false,0,nil},
  {"id_control","id",pgtype.Int8OID,int64(42),true,0,nil},
  {"datetime_control","at",pgtype.TimestamptzOID,at,true,0,nil},
  {"decimal_control","amount",pgtype.NumericOID,pg.DecimalValue{Int:big.NewInt(9007199254740993),Exp:-2,Valid:true},true,0,nil},
 }{t.Run(fmt.Sprintf("%d/%s",format,sample.name),func(t *testing.T){
  mapping:=pgtype.NewMap();raw,err:=mapping.Encode(sample.oid,format,sample.value,nil);if err!=nil{t.Fatal("driver fixture encoding",err)}
  result,err:=scanReading(&durationRows{field:pgconn.FieldDescription{Name:sample.column,DataTypeOID:sample.oid,Format:format},raw:raw,mapping:mapping})
  if !sample.valid{if err==nil||result!=nil{t.Fatalf("invalid interval reached model: value=%+v err=%v",result,err)};return}
  if err!=nil||result==nil{t.Fatalf("valid storage value rejected: %v",err)}
  switch sample.column{
  case "elapsed":if result.Elapsed!=sample.want{t.Fatal("duration units changed",result.Elapsed,sample.want)}
  case "optional_elapsed":if sample.value==nil{if result.OptionalElapsed!=nil{t.Fatal("SQL NULL became a duration")}}else if result.OptionalElapsed==nil||*result.OptionalElapsed!=sample.want{t.Fatal("optional duration changed",result.OptionalElapsed)}
  case "laps":if !slices.Equal(result.Laps,sample.list){t.Fatal("duration list changed",result.Laps)}
  case "optional_laps":if sample.value==nil{if result.OptionalLaps!=nil{t.Fatal("SQL NULL became a list")}}else if result.OptionalLaps==nil||!slices.Equal(result.OptionalLaps,sample.list){t.Fatal("optional list changed",result.OptionalLaps)}
  case "id":if result.Id!=42{t.Fatal("ordinary scanner control changed")}
  case "at":if !result.At.Equal(at){t.Fatal("date control changed",result.At)}
  case "amount":if !result.Amount.Equal(decimal.New(9007199254740993,-2)){t.Fatal("decimal precision changed",result.Amount)}
  }
 })}}
}
`

func durationStorageIDs() StableIDs {
	return StableIDs{ModelFields: map[string]map[string]int{"Reading": {"id": 1, "elapsed": 2, "optionalElapsed": 3, "laps": 4, "optionalLaps": 5, "at": 6, "amount": 7}}}
}

func TestGeneratedDurationDatabaseBoundary(t *testing.T) {
	imports := `"bytes";"encoding/json";"errors";"net";"net/http/httptest";"strconv";"github.com/light-speak/luxo/internal/testutil";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";luxerrors "github.com/light-speak/luxo/pkg/lux/errors";"github.com/light-speak/luxo/pkg/lux/rpc";"github.com/light-speak/luxo/pkg/lux/schema";"github.com/light-speak/luxo/pkg/lux/selection";"github.com/jackc/pgx/v5";"github.com/jackc/pgx/v5/pgxpool";"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres";`
	assertions := strings.Replace(durationStorageAssertions, "import(", "import("+imports, 1)
	runGeneratedFixture(t, durationStorageModel+` api reading(key: Int): Reading? { return Reading.where(id == key).select().first() }`, assertions+durationDatabaseAssertions, durationStorageIDs())
}

const durationDatabaseAssertions = `
func TestDurationDatabaseBoundary(t *testing.T){
 container,err:=postgres.Run(t.Context(),testutil.PostgresImage(),postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies());testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)};db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE readings(id bigint PRIMARY KEY,elapsed interval NOT NULL,optional_elapsed interval,laps interval[] NOT NULL,optional_laps interval[],at timestamptz NOT NULL,amount numeric NOT NULL)");err!=nil{t.Fatal(err)}
 good:=pg.DurationValue{Microseconds:1234,Valid:true};month:=pg.DurationValue{Months:1,Valid:true};overflow:=pg.DurationValue{Microseconds:math.MaxInt64,Valid:true};zero:=pg.DurationValue{Valid:true}
 for id:=int64(1);id<=9;id++{
  value:=good;var optional any;list:=[]pg.DurationValue{good,zero};var optionalList []pg.DurationValue
  switch id{case 2:value=month;case 3:value=overflow;case 4:optional=month;case 5:list=[]pg.DurationValue{good,month};case 6:optionalList=[]pg.DurationValue{overflow};case 8:value=pg.DurationValue{Days:106751992,Microseconds:math.MinInt64,Valid:true};case 9:value=pg.DurationValue{Microseconds:math.MaxInt64/1000,Valid:true}}
  if _,err:=pg.Exec(t.Context(),db,"INSERT INTO readings VALUES($1,$2,$3,$4,$5,$6,$7)",id,value,optional,list,optionalList,time.Date(2026,1,1,0,0,0,123456789,time.UTC),decimal.New(9007199254740993,-2));err!=nil{t.Fatal(err)}
 }
 for _,mode:=range []pgx.QueryExecMode{pgx.QueryExecModeCacheStatement,pgx.QueryExecModeCacheDescribe,pgx.QueryExecModeDescribeExec,pgx.QueryExecModeExec,pgx.QueryExecModeSimpleProtocol}{t.Run(mode.String(),func(t *testing.T){testDurationDatabaseMode(t,connection,mode)})}
}
func checkGeneratedDateTimeReturning(t *testing.T,db *pg.DB,app *App){
 t.Helper();if _,err:=pg.Exec(t.Context(),db,"DELETE FROM readings WHERE id>=10000");err!=nil{t.Fatal(err)}
 original:=time.Date(2026,1,1,0,0,0,123456789,time.UTC);want:=original.Truncate(time.Microsecond)
 row,err:=app.Reading.Create().SetId(10000).SetElapsed(time.Microsecond).SetLaps([]time.Duration{}).SetAt(original).SetAmount(decimal.New(123,-2)).Exec(t.Context())
 if err!=nil||row==nil||!row.At.Equal(want){t.Fatal("generated create returned an unpersisted timestamp",row,err)}
 batch:=app.Reading.CreateMany()
 for _,id:=range []int64{10001,10002}{batch.Add(ReadingCreateInput{Id:id,Elapsed:time.Microsecond,Laps:[]time.Duration{},At:original,Amount:decimal.New(123,-2)})}
 rows,err:=batch.Exec(t.Context());if err!=nil||len(rows)!=2{t.Fatal("generated batch write failed",rows,err)}
 for _,row:=range rows{if !row.At.Equal(want){t.Fatal("generated batch returned an unpersisted timestamp",row.At,want)}}
 changed:=original.Add(2*time.Microsecond)
 count,err:=app.Reading.Where(ReadingWhere.Id.Eq(10000)).Update().SetAt(changed).Exec(t.Context());if err!=nil||count!=1{t.Fatal("generated timestamp update failed",count,err)}
 stored,err:=pg.QueryScalar[time.Time](t.Context(),db,"SELECT at FROM readings WHERE id=10000");if err!=nil||!stored.Equal(changed.Truncate(time.Microsecond)){t.Fatal("generated update storage changed",stored,err)}
 if original.Nanosecond()!=123456789{t.Fatal("write modified caller timestamp")}
}
func testDurationDatabaseMode(t *testing.T,connection string,mode pgx.QueryExecMode){
 cfg,err:=pgxpool.ParseConfig(connection);if err!=nil{t.Fatal(err)};cfg.ConnConfig.DefaultQueryExecMode=mode;pool,err:=pgxpool.NewWithConfig(t.Context(),cfg);if err!=nil{t.Fatal(err)};db:=pg.NewDBFromPool(pool);t.Cleanup(db.Close)
 app:=NewFromDB(db);checkGeneratedDateTimeReturning(t,db,app)
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)};server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}();defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}();client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 for _,sample:=range []struct{id int64;field,want string;invalid bool}{
  {1,"elapsed","1234000",false},{1,"laps","[1234000,0]",false},
  {2,"elapsed","",true},{3,"elapsed","",true},{4,"optionalElapsed","",true},{5,"laps","",true},{6,"optionalLaps","",true},
  {7,"optionalElapsed","null",false},{7,"optionalLaps","null",false},
  {8,"elapsed",strconv.FormatInt((106751992*86400000000-9223372036854775808)*1000,10),false},
  {9,"elapsed",strconv.FormatInt(math.MaxInt64/1000*1000,10),false},
  {1,"at","",false},{1,"amount","",false},
  {10000,"at","2026-01-01T00:00:00.123458Z",false},{10001,"at","",false},{10002,"at","",false},
 }{for protocol:=0;protocol<3;protocol++{t.Run(fmt.Sprintf("%d/%s/protocol=%d",sample.id,sample.field,protocol),func(t *testing.T){
  data,status,err:=readDurationField(t,router,client,sample.id,sample.field,protocol)
  if sample.invalid{
   if protocol==2{var failure *luxerrors.AppError;if !errors.As(err,&failure)||failure.Code!=500||len(data)!=0{t.Fatalf("invalid DB duration escaped LRPC: %x err=%v",data,err)}}else if err!=nil||status!=500{t.Fatalf("invalid DB duration escaped HTTP: status=%d data=%s err=%v",status,data,err)}
   data,status,err=readDurationField(t,router,client,sample.id,"id",protocol);if err!=nil||status!=200{t.Fatal("unselected invalid field or next request failed",status,err)};assertDurationField(t,data,"id",strconv.FormatInt(sample.id,10));return
  }
  if err!=nil||status!=200{t.Fatal("valid stored duration rejected",status,err)};assertDurationField(t,data,sample.field,sample.want)
 })}}
}
func readDurationField(t *testing.T,router *api.Router,client *rpc.Client,id int64,field string,mode int)([]byte,int,error){
 t.Helper();definition:=router.Schema.APIs["reading"];mask,err:=schema.SelectToFieldMask([]*selection.Field{{Name:field}},router.Schema.Models["Reading"],router.Schema);if err!=nil{t.Fatal(err)}
 params:=map[string]any{"key":id};body,err:=api.EncodeBinaryRequest(definition.ID,mask,params,router.Registry.ParamOrder("reading"));if err!=nil{t.Fatal(err)}
 if mode==2{_,off:=codec.ReadVarint(body,0);size,n:=codec.ReadVarint(body,off);off+=n+int(size);body,err=client.CallWithMaskContext(t.Context(),"",definition.ID,mask,body[off:]);if err!=nil{return body,0,err}}else{
  if mode==0{params["$api"]="reading";params["$select"]=field;body,err=json.Marshal(params);if err!=nil{t.Fatal(err)}}
  request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));if mode==1{request.Header.Set("X-Luxo-Mode","binary")};response:=httptest.NewRecorder();router.ServeHTTP(response,request);body=response.Body.Bytes();if response.Code!=200{return body,response.Code,nil}
 }
 if mode==0{var envelope struct{Data json.RawMessage};if err:=json.Unmarshal(body,&envelope);err!=nil{return nil,0,err};return envelope.Data,200,nil}
 data,err:=router.StreamPayloadJSON("reading",body);return data,200,err
}
func assertDurationField(t *testing.T,data []byte,field,want string){
 t.Helper();var values map[string]json.RawMessage;if err:=json.Unmarshal(data,&values);err!=nil{t.Fatal(err)};raw:=values[field]
 if field=="amount"{var text string;if err:=json.Unmarshal(raw,&text);err!=nil{t.Fatal(err)};value,err:=decimal.NewFromString(text);if err!=nil||!value.Equal(decimal.New(9007199254740993,-2)){t.Fatal("decimal control lost precision",text,err)};return}
 if field=="at"{if want==""{want="2026-01-01T00:00:00.123456Z"};expected,err:=time.Parse(time.RFC3339Nano,want);if err!=nil{t.Fatal(err)};var text string;if err:=json.Unmarshal(raw,&text);err!=nil{t.Fatal(err)};value,err:=time.Parse(time.RFC3339Nano,text);if err!=nil||!value.Equal(expected){t.Fatal("date control lost stored microseconds",text,want,err)};return}
 if string(raw)!=want{t.Fatalf("stored value changed: field=%s got=%s want=%s",field,raw,want)}
}
`

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
	generateScanner(&b, m, nil)
	got := b.String()

	checks := []string{
		"func scanUser(rows pg.Rows) (*User, error)",
		"var value User",
		"fds := rows.FieldDescriptions()",
		"dests := make([]any, len(fds))",
		"switch string(fd.Name)",
		`case "id":`,
		"dests[i] = &value.Id",
		`case "name":`,
		"dests[i] = &value.Name",
		`case "email":`,
		"dests[i] = &value.Email",
		`case "created_at":`,
		"dests[i] = &value.CreatedAt",
		"default:",
		"dests[i] = nil",
		"if err := rows.Scan(dests...); err != nil",
		"pg.ValidateDateTime(value.CreatedAt)",
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("missing %q in:\n%s", check, got)
		}
	}
}

func TestGeneratedScannerModelNameIsolation(t *testing.T) {
	for _, name := range []string{"Rows", "Dests", "Fds", "I", "Fd", "Value"} {
		t.Run(name, func(t *testing.T) {
			runGeneratedFixture(t, "model "+name+" @noTime { id: Int @id optional: JSON? }",
				"package generated\nimport \"testing\"\nfunc TestCompiled(t *testing.T) {}\n",
				StableIDs{ModelFields: map[string]map[string]int{name: {"id": 1, "optional": 2}}})
		})
	}
}

func TestGeneratedNullableJSONScannerBenchmark(t *testing.T) {
	model := &ast.ModelDecl{Name: "Document", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "optional", Type: &ast.TypeRef{Name: "JSON", Nullable: true}},
	}}
	var current, before strings.Builder
	generateScanner(&current, model, nil)
	// The previous scanner passed the model field directly to pgx for every type.
	model.Fields[1].Type.Name = "Bytes"
	generateScanner(&before, model, nil)
	old := strings.Replace(before.String(), "func scanDocument(", "func scanDocumentPrevious(", 1)
	source := "package generated\nimport(\"encoding/json\";pg \"github.com/light-speak/luxo/pkg/lux/pg\")\ntype Document struct { Id int64; Optional *json.RawMessage }\n" + current.String() + old
	runGeneratedProtocolPackage(t, source, `package generated
import("fmt";"strings";"testing";pg "github.com/light-speak/luxo/pkg/lux/pg";"github.com/jackc/pgx/v5/pgconn";"github.com/jackc/pgx/v5/pgtype")
type jsonRows struct { pg.Rows; fields []pgconn.FieldDescription; raw []byte; mapping *pgtype.Map }
func(r *jsonRows)FieldDescriptions()[]pgconn.FieldDescription{return r.fields}
func(r *jsonRows)Scan(dest ...any)error{
 for i,field:=range r.fields{if err:=r.mapping.Scan(field.DataTypeOID,0,r.raw,dest[i]);err!=nil{return err}};return nil
}
func newJSONRows(raw []byte, selected bool)*jsonRows{
 field:=pgconn.FieldDescription{Name:"optional",DataTypeOID:pgtype.JSONBOID}
 if !selected{field=pgconn.FieldDescription{Name:"id",DataTypeOID:pgtype.Int8OID};raw=[]byte("1")}
 return &jsonRows{fields:[]pgconn.FieldDescription{field},raw:raw,mapping:pgtype.NewMap()}
}
func TestJSONNullAndProjection(t *testing.T){
 rows:=newJSONRows([]byte("null"),true)
 got,err:=scanDocument(rows);if err!=nil||got.Optional==nil||string(*got.Optional)!="null"{t.Fatal(got,err)}
 old,err:=scanDocumentPrevious(rows);if err!=nil||old.Optional!=nil{t.Fatal("baseline did not reproduce",old,err)}
 got,err=scanDocument(newJSONRows(nil,false));if err!=nil||got.Id!=1||got.Optional!=nil{t.Fatal(got,err)}
}
var jsonScannerSink *Document
func BenchmarkScannerDiscardNullableJSON(b *testing.B){
 for _,sample:=range []struct{name string;raw []byte;selected bool}{
  {"Unselected",nil,false},{"SQLNull",nil,true},{"Short",[]byte("{\"value\":123}"),true},{"Long",[]byte("\""+strings.Repeat("a",73728)+"\""),true},
 }{
  for _,previous:=range []bool{true,false}{
   scan:=scanDocument;if previous{scan=scanDocumentPrevious}
   rows:=newJSONRows(sample.raw,sample.selected)
   for range 100{if _,err:=scan(rows);err!=nil{b.Fatal(err)}}
   b.Run(fmt.Sprintf("%s/previous=%v",sample.name,previous),func(b *testing.B){
    b.ReportAllocs();for b.Loop(){var err error;jsonScannerSink,err=scan(rows);if err!=nil{b.Fatal(err)}}
   })
  }
 }
}
`)
}

func TestGeneratedRequiredJSONScannerBenchmark(t *testing.T) {
	model := &ast.ModelDecl{Name: "Document", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "data", Type: &ast.TypeRef{Name: "JSON"}},
	}}
	var generated strings.Builder
	generateScanner(&generated, model, nil)
	current := generated.String()
	before := strings.Replace(current, "(*pg.JSONValue)(&value.Data)", "&value.Data", 1)
	if before == current {
		t.Fatal("required JSON baseline replacement missed")
	}
	before = strings.Replace(before, "func scanDocument(", "func scanDocumentBefore(", 1)
	source := "package generated\nimport(\"encoding/json\";pg \"github.com/light-speak/luxo/pkg/lux/pg\")\ntype Document struct { Id int64; Data json.RawMessage }\n" + current + before
	runGeneratedProtocolPackage(t, source, `package generated
import("fmt";"strings";"testing";pg "github.com/light-speak/luxo/pkg/lux/pg";"github.com/jackc/pgx/v5/pgconn";"github.com/jackc/pgx/v5/pgtype")
type scalarJSONRows struct{pg.Rows;mapping *pgtype.Map;fields []pgconn.FieldDescription;raw []byte}
func(r *scalarJSONRows)FieldDescriptions()[]pgconn.FieldDescription{return r.fields}
func(r *scalarJSONRows)Scan(dest ...any)error{f:=r.fields[0];return r.mapping.Scan(f.DataTypeOID,f.Format,r.raw,dest[0])}
func newScalarRows(raw []byte,selected bool)*scalarJSONRows{
 field:=pgconn.FieldDescription{Name:"data",DataTypeOID:pgtype.JSONBOID,Format:pgtype.TextFormatCode}
 if !selected{field.Name="id";field.DataTypeOID=pgtype.Int8OID;raw=[]byte("42")}
 return &scalarJSONRows{mapping:pgtype.NewMap(),fields:[]pgconn.FieldDescription{field},raw:raw}
}
func TestScalarBaseline(t *testing.T){
 if value,err:=scanDocument(newScalarRows(nil,true));err==nil||value!=nil{t.Fatal("candidate accepted SQL NULL",value,err)}
 if value,err:=scanDocumentBefore(newScalarRows(nil,true));err!=nil||value==nil||value.Data!=nil{t.Fatal("baseline missed original defect",value,err)}
 for _,scan:=range []func(pg.Rows)(*Document,error){scanDocument,scanDocumentBefore}{
  for _,raw:=range []string{"null","{\"id\":9223372036854775807}","\""+strings.Repeat("a",73728)+"\""}{
   value,err:=scan(newScalarRows([]byte(raw),true));if err!=nil||value==nil||string(value.Data)!=raw{t.Fatal("legal JSON changed",value,err)}
  }
  value,err:=scan(newScalarRows(nil,false));if err!=nil||value.Id!=42||value.Data!=nil{t.Fatal("projection changed",value,err)}
 }
}
var scalarJSONSink *Document
func BenchmarkScannerDiscardRequiredJSON(b *testing.B){
 for _,sample:=range []struct{name string;raw []byte;selected bool}{
  {"unselected",nil,false},{"literalNull",[]byte("null"),true},{"short",[]byte("{\"id\":9223372036854775807}"),true},{"long",[]byte("\""+strings.Repeat("a",73728)+"\""),true},
 }{
  for _,before:=range []bool{true,false}{
   scan:=scanDocument;if before{scan=scanDocumentBefore};rows:=newScalarRows(sample.raw,sample.selected)
   for range 100{if _,err:=scan(rows);err!=nil{b.Fatal(err)}}
   b.Run(fmt.Sprintf("%s/before=%v",sample.name,before),func(b *testing.B){b.ReportAllocs();for b.Loop(){var err error;scalarJSONSink,err=scan(rows);if err!=nil{b.Fatal(err)}}})
  }
 }
}
`)
}

func TestGeneratedJSONListScannerBenchmark(t *testing.T) {
	model := &ast.ModelDecl{Name: "Document", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "entries", Type: &ast.TypeRef{Name: "JSON", IsList: true, Nullable: true}},
	}}
	var generated strings.Builder
	generateScanner(&generated, model, nil)
	current := generated.String()
	arrayBefore := strings.Replace(current, "(*pg.JSONListValue)(&value.Entries)", "&value.Entries", 1)
	errorBefore := strings.Replace(current, "if err := rows.Scan(dests...); err != nil { return nil, err }", "return &value, rows.Scan(dests...)", 1)
	errorBefore = strings.Replace(errorBefore, "\treturn &value, nil\n", "", 1)
	if current == arrayBefore || current == errorBefore {
		t.Fatal("scanner baseline replacement missed")
	}
	arrayBefore = strings.Replace(arrayBefore, "func scanDocument(", "func scanDocumentArrayBefore(", 1)
	errorBefore = strings.Replace(errorBefore, "func scanDocument(", "func scanDocumentErrorBefore(", 1)
	source := "package generated\nimport(\"encoding/json\";pg \"github.com/light-speak/luxo/pkg/lux/pg\")\ntype Document struct { Id int64; Entries []json.RawMessage }\n" + current + arrayBefore + errorBefore
	runGeneratedProtocolPackage(t, source, `package generated
import("encoding/json";"fmt";"testing";pg "github.com/light-speak/luxo/pkg/lux/pg";"github.com/jackc/pgx/v5/pgconn";"github.com/jackc/pgx/v5/pgtype")
type listRows struct{pg.Rows;mapping *pgtype.Map;fields []pgconn.FieldDescription;raw []byte}
func(r *listRows)FieldDescriptions()[]pgconn.FieldDescription{return r.fields}
func(r *listRows)Scan(dest ...any)error{f:=r.fields[0];return r.mapping.Scan(f.DataTypeOID,f.Format,r.raw,dest[0])}
func newListRows(t testing.TB,values []json.RawMessage,selected bool)*listRows{
 t.Helper();mapping:=pgtype.NewMap();oid:=uint32(pgtype.JSONBArrayOID);field:="entries";var input any=values
 if !selected{oid=pgtype.Int8OID;field="id";input=int64(42)}
 raw,err:=mapping.Encode(oid,pgtype.BinaryFormatCode,input,nil);if err!=nil{t.Fatal(err)}
 return &listRows{mapping:mapping,fields:[]pgconn.FieldDescription{{Name:field,DataTypeOID:oid,Format:pgtype.BinaryFormatCode}},raw:raw}
}
func TestBaselineAndFailure(t *testing.T){
 for _,values:=range [][]json.RawMessage{nil,{}, {json.RawMessage("null"),json.RawMessage("{\"id\":9223372036854775807}")}}{
  for _,scan:=range []func(pg.Rows)(*Document,error){scanDocument,scanDocumentArrayBefore,scanDocumentErrorBefore}{
   value,err:=scan(newListRows(t,values,true));if err!=nil||value==nil{t.Fatal(value,err)}
   if (value.Entries==nil)!=(values==nil)||len(value.Entries)!=len(values){t.Fatal("baseline changed presence",value)}
   for i:=range values{if string(value.Entries[i])!=string(values[i]){t.Fatal("baseline changed JSON bytes",value)}}
   value,err=scan(newListRows(t,values,false));if err!=nil||value.Id!=42||value.Entries!=nil{t.Fatal("baseline changed projection",value,err)}
  }
 }
 rows:=newListRows(t,[]json.RawMessage{nil},true)
 if value,err:=scanDocument(rows);err==nil||value!=nil{t.Fatal("candidate accepted NULL element or returned partial model",value,err)}
 if value,err:=scanDocumentArrayBefore(rows);err!=nil||value==nil||len(value.Entries)!=1||value.Entries[0]!=nil{t.Fatal("baseline missed original defect",value,err)}
 if value,err:=scanDocumentErrorBefore(rows);err==nil||value==nil{t.Fatal("baseline missed partial model",value,err)}
}
var listScannerSink *Document
func BenchmarkScannerDiscardJSONList(b *testing.B){
 many:=make([]json.RawMessage,128);for i:=range many{many[i]=json.RawMessage("{\"id\":9223372036854775807}")}
 for _,sample:=range []struct{name string;values []json.RawMessage;selected bool}{
  {"unselected",nil,false},{"null",nil,true},{"empty",[]json.RawMessage{},true},{"one",[]json.RawMessage{json.RawMessage("null")},true},{"128",many,true},
 }{
  for _,before:=range []bool{true,false}{
   scan:=scanDocument;if before{scan=scanDocumentArrayBefore};rows:=newListRows(b,sample.values,sample.selected)
   for range 100{if _,err:=scan(rows);err!=nil{b.Fatal(err)}}
   b.Run(fmt.Sprintf("array/%s/before=%v",sample.name,before),func(b *testing.B){b.ReportAllocs();for b.Loop(){var err error;listScannerSink,err=scan(rows);if err!=nil{b.Fatal(err)}}})
  }
 }
 for _,before:=range []bool{true,false}{
  scan:=scanDocument;if before{scan=scanDocumentErrorBefore};rows:=newListRows(b,nil,false)
  for range 100{if _,err:=scan(rows);err!=nil{b.Fatal(err)}}
  b.Run(fmt.Sprintf("errorGuard/before=%v",before),func(b *testing.B){b.ReportAllocs();for b.Loop(){var err error;listScannerSink,err=scan(rows);if err!=nil{b.Fatal(err)}}})
 }
}
`)
}

func TestGeneratedScannerDiscard(t *testing.T) {
	var generated strings.Builder
	generateScanner(&generated, &ast.ModelDecl{Name: "User", Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}}, nil)
	current := generated.String()
	previous := strings.ReplaceAll(strings.Replace(current, "func scanUser(", "func scanUserPrevious(", 1), "dests[i] = nil", "dests[i] = new(any)")
	source := "package generated\nimport pg \"github.com/light-speak/luxo/pkg/lux/pg\"\ntype User struct { Id int64 }\n" + current + previous
	runGeneratedProtocolPackage(t, source, `package generated
import ("fmt"; "testing"; pg "github.com/light-speak/luxo/pkg/lux/pg"; "github.com/jackc/pgx/v5/pgconn")
type discardRows struct { pg.Rows; fields []pgconn.FieldDescription; decoded int; failure error }
func (r *discardRows) FieldDescriptions() []pgconn.FieldDescription { return r.fields }
func (r *discardRows) Scan(dest ...any) error {
 *dest[0].(*int64) = 7
 for _, value := range dest[1:] { if value != nil { *value.(*any) = "ignored"; r.decoded++ } }
 return r.failure
}
func TestDiscard(t *testing.T) {
 rows := &discardRows{fields: []pgconn.FieldDescription{{Name:"id"}, {Name:"ignored"}}}
 user, err := scanUser(rows)
 if err != nil || user.Id != 7 || rows.decoded != 0 { t.Fatalf("%+v %v decoded=%d", user, err, rows.decoded) }
 rows.failure=fmt.Errorf("scan failed after writing id")
 user,err=scanUser(rows)
 if err!=rows.failure||user!=nil{t.Fatalf("partial model escaped: %+v %v",user,err)}
}
var scannerSink *User
func BenchmarkScannerDiscard(b *testing.B) {
 for _, unknown := range []int{0, 1, 16} {
  rows := &discardRows{fields: make([]pgconn.FieldDescription, unknown+1)}
  rows.fields[0].Name = "id"
  for i := 1; i < len(rows.fields); i++ { rows.fields[i].Name = "ignored" }
  for _, count := range []int{1, 128} {
  for _, previous := range []bool{true, false} {
   scan := scanUser; if previous { scan = scanUserPrevious }
   b.Run(fmt.Sprintf("unknown=%d/rows=%d/previous=%v", unknown, count, previous), func(b *testing.B) {
    b.ReportAllocs()
    for b.Loop() { for range count { var err error; scannerSink, err = scan(rows); if err != nil { b.Fatal(err) } } }
   })
  }
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
	generateScanner(&b, m, nil)
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
	generateScanner(&b, m, nil)
	got := b.String()

	if !strings.Contains(got, "func scanOrderItem(rows pg.Rows)") {
		t.Errorf("wrong function name:\n%s", got)
	}
	if !strings.Contains(got, "var value OrderItem") {
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
	if !strings.Contains(code, `case "role":`) || !strings.Contains(code, "dests[i] = &value.Role") {
		t.Fatalf("enum column silently discarded by external scanner:\n%s", code)
	}
}

func TestGeneratedExtendNullableJSONScanner(t *testing.T) {
	extend := &ast.ExtendDecl{Name: "Document", Fields: []*ast.FieldDecl{
		{Name: "optional", Type: &ast.TypeRef{Name: "JSON", Nullable: true}},
		{Name: "data", Type: &ast.TypeRef{Name: "JSON"}},
		{Name: "entries", Type: &ast.TypeRef{Name: "JSON", IsList: true, Nullable: true}},
	}}
	var source strings.Builder
	source.WriteString("package generated\nimport (\"encoding/json\"; pg \"github.com/light-speak/luxo/pkg/lux/pg\")\n")
	g := defaultGenerator()
	g.generateExtendStub(&source, extend, nil)
	g.generateExtendScanners(&source, []*ast.File{{Extends: []*ast.ExtendDecl{extend}}}, nil, nil)
	runGeneratedProtocolPackage(t, source.String(), `package generated
import("bytes";"encoding/json";"errors";"fmt";"testing";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/jackc/pgx/v5/pgconn";"github.com/jackc/pgx/v5/pgtype")
type jsonRows struct { pg.Rows; format int16; data []byte; omitted bool; failure error; name string; oid uint32 }
func(r *jsonRows)FieldDescriptions()[]pgconn.FieldDescription{
 if r.omitted{return nil};name,oid:=r.name,r.oid;if name==""{name="optional"};if oid==0{oid=pgtype.JSONBOID}
 return []pgconn.FieldDescription{{Name:name,DataTypeOID:oid,Format:r.format}}
}
func(r *jsonRows)Scan(dest ...any)error{
 if r.failure!=nil{return r.failure};if r.omitted{if len(dest)!=0{return errors.New("unexpected destination")};return nil}
 field:=r.FieldDescriptions()[0];return pgtype.NewMap().Scan(field.DataTypeOID,r.format,r.data,dest[0])
}
func TestRequiredJSONAndArray(t *testing.T){
 for _,format:=range []int16{pgtype.TextFormatCode,pgtype.BinaryFormatCode}{
  mapping:=pgtype.NewMap()
  for _,raw:=range []json.RawMessage{nil,json.RawMessage("null"),json.RawMessage("{\"id\":9223372036854775807}")}{
   data,err:=mapping.Encode(pgtype.JSONBOID,format,raw,nil);if err!=nil{t.Fatal(err)}
   value,err:=scanDocument(&jsonRows{name:"data",format:format,data:data})
   if raw==nil{if err==nil||value!=nil{t.Fatal("SQL NULL accepted",value,err)};continue}
   clear(data);if err!=nil||value==nil||string(value.Data)!=string(raw){t.Fatal("required JSON changed or borrowed bytes",value,err)}
  }
  for _,raw:=range [][]json.RawMessage{nil,{}, {json.RawMessage("null"),json.RawMessage("9223372036854775807")},{nil}}{
   data,err:=mapping.Encode(pgtype.JSONBArrayOID,format,raw,nil);if err!=nil{t.Fatal(err)}
   value,err:=scanDocument(&jsonRows{name:"entries",oid:pgtype.JSONBArrayOID,format:format,data:data})
   if len(raw)==1&&raw[0]==nil{if err==nil||value!=nil{t.Fatal("SQL NULL element accepted",value,err)};continue}
   clear(data);if err!=nil||value==nil{t.Fatal(value,err)}
   if (value.Entries==nil)!=(raw==nil)||len(value.Entries)!=len(raw){t.Fatal("array presence changed",value)}
   for i:=range raw{if string(value.Entries[i])!=string(raw[i]){t.Fatal("array changed or borrowed bytes",value)}}
  }
 }
}
func TestNullableJSON(t *testing.T){
 for _,format:=range []int16{pgtype.TextFormatCode,pgtype.BinaryFormatCode}{
  for _,raw:=range []string{"","null","{\"n\":123456789012345678901234567890}"}{t.Run(fmt.Sprintf("%d/%s",format,raw),func(t *testing.T){
   var data []byte
   if raw!=""{data=[]byte(raw);if format==pgtype.BinaryFormatCode{data=append([]byte{1},data...)}}
   rows:=&jsonRows{format:format,data:data}
   first,err:=scanDocument(rows);if err!=nil{t.Fatal(err)}
   second,err:=scanDocument(rows);if err!=nil{t.Fatal(err)}
   clear(data)
   if raw==""{if first.Optional!=nil||second.Optional!=nil{t.Fatal("SQL NULL became present")};return}
   if first.Optional==nil||second.Optional==nil||!bytes.Equal(*first.Optional,[]byte(raw))||string(*second.Optional)!=raw{t.Fatal("lost JSON value or aliased driver buffer")}
   clear(*first.Optional);if string(*second.Optional)!=raw{t.Fatal("models share JSON storage")}
  })}
 }
 got,err:=scanDocument(&jsonRows{omitted:true});if err!=nil||got==nil||got.Optional!=nil{t.Fatalf("unselected field: %+v %v",got,err)}
 failure:=errors.New("scan failed")
 got,err=scanDocument(&jsonRows{failure:failure});if got!=nil||!errors.Is(err,failure){t.Fatalf("partial model escaped: %+v %v",got,err)}
}
`)
}

func TestGeneratedExtendEnumScanner(t *testing.T) {
	if os.Getenv("LUXO_TEST_DATABASE_URL") != "" {
		// Verify the configured database path with Docker unavailable to the generated test.
		t.Setenv("DOCKER_HOST", "unix:///dev/null")
	}
	extend := &ast.ExtendDecl{Name: "User", Fields: []*ast.FieldDecl{
		{Name: "role", Type: &ast.TypeRef{Name: "Role"}},
		{Name: "optionalRole", Type: &ast.TypeRef{Name: "Role", Nullable: true}},
		{Name: "roles", Type: &ast.TypeRef{Name: "Role", IsList: true}},
		{Name: "tags", Type: &ast.TypeRef{Name: "String", IsList: true, Nullable: true}},
	}}
	enums := map[string]bool{"Role": true}
	var source strings.Builder
	source.WriteString("package generated\nimport (pg \"github.com/light-speak/luxo/pkg/lux/pg\"; \"github.com/light-speak/luxo/pkg/lux/codec\")\n")
	generateEnum(&source, &ast.EnumDecl{Name: "Role", Values: []string{"USER", "ADMIN"}})
	g := defaultGenerator()
	g.generateExtendStub(&source, extend, enums)
	g.generateExtendScanners(&source, []*ast.File{{Extends: []*ast.ExtendDecl{extend}}}, nil, enums)
	runGeneratedProtocolPackage(t, source.String(), `package generated
import (
 "errors"
 "os"
 "testing"
 pg "github.com/light-speak/luxo/pkg/lux/pg"
 "github.com/light-speak/luxo/pkg/lux/codec"
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
 connection := os.Getenv("LUXO_TEST_DATABASE_URL")
 if connection == "" {
  image:=os.Getenv("LUXO_TEST_POSTGRES_IMAGE");if image==""{image="postgres:17-alpine"}
  container, err := postgres.Run(ctx,image,postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
  testcontainers.CleanupContainer(t, container)
  if err != nil { t.Fatal(err) }
  connection, err = container.ConnectionString(ctx,"sslmode=disable"); if err != nil { t.Fatal(err) }
 }
 db, err := pg.NewDB(ctx,connection); if err != nil { t.Fatal(err) }; defer db.Close()
 value := "USER"
 for _, optional := range []*string{nil, &value} {
  rows, err := pg.QueryRows(ctx,db,scanUser,"SELECT 7::bigint AS id, 'ADMIN'::text AS role, $1::text AS optional_role, 'ignored'::text AS ignored, CASE WHEN $1::text IS NULL THEN NULL::text[] ELSE ARRAY['a','b']::text[] END AS tags",optional)
  if err != nil { t.Fatal(err) }
  if len(rows)!=1 || rows[0].Id!=7 || rows[0].Role!=Role("ADMIN") { t.Fatalf("lost database row: %+v",rows) }
  if optional==nil { if rows[0].OptionalRole!=nil { t.Fatal("NULL enum changed") } } else if rows[0].OptionalRole==nil || string(*rows[0].OptionalRole)!=*optional { t.Fatal("nullable enum lost") }
  if optional==nil { if rows[0].Tags!=nil { t.Fatal("NULL array changed") } } else if len(rows[0].Tags)!=2 || rows[0].Tags[0]!="a" || rows[0].Tags[1]!="b" { t.Fatalf("array lost: %+v",rows[0].Tags) }
 }
 for _,sql:=range []string{
  "SELECT 'UNKNOWN'::text AS role",
  "SELECT ''::text AS role",
  "SELECT 'UNKNOWN'::text AS optional_role",
  "SELECT ARRAY['USER','UNKNOWN']::text[] AS roles",
 }{t.Run(sql,func(t *testing.T){
  got,err:=pg.QueryRows(ctx,db,scanUser,sql)
  if !errors.Is(err,codec.ErrInvalidEnum)||len(got)!=0{t.Fatalf("invalid database enum escaped: rows=%+v error=%v",got,err)}
  valid,err:=pg.QueryRows(ctx,db,scanUser,"SELECT 'USER'::text AS role, ARRAY['USER','ADMIN']::text[] AS roles")
  if err!=nil||len(valid)!=1||valid[0].Role!=RoleUSER||len(valid[0].Roles)!=2||valid[0].Roles[1]!=RoleADMIN{t.Fatalf("valid query after rejection failed: %+v %v",valid,err)}
 })}

}
`)
}

func TestGeneratedScalarReceiverNameIsolation(t *testing.T) {
	for _, name := range []string{"I", "V", "E", "R", "B"} {
		t.Run(name, func(t *testing.T) {
			source := "model " + name + " @noTime { id: Int @id optional: JSON? amount: Decimal? amounts: [Decimal] names: [String] numbers: [Int] }"
			assertions := `package generated
import("encoding/json";"testing";"github.com/shopspring/decimal";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestRoundTrip(t *testing.T){
 amount:=decimal.RequireFromString("12345678901234567890.123456789")
 for _,payload:=range []string{"null","123","{\"value\":1}"}{
 optional:=json.RawMessage(payload)
 original:=` + name + `{Id:42,Optional:&optional,Amount:&amount,Amounts:[]decimal.Decimal{amount},Names:[]string{"one","中文"},Numbers:[]int64{-1,9223372036854775807}}
 for _, selected:=range []bool{false,true}{
  var mask []byte
  if selected {var err error;mask,err=codec.AppendSelectionMask(nil,[]byte{63},nil);if err!=nil{t.Fatal(err)}}
  buf:=&api.ResponseBuf{}
  if err:=original.WriteLuxo(buf,mask);err!=nil{t.Fatal(err)}
  var got ` + name + `
  dec:=codec.NewDecoder(buf.B);got.ReadLuxo(dec);if err:=dec.Finish();err!=nil{t.Fatal(err)}
  validJSON:=got.Optional==nil
  if payload!="null"{validJSON=got.Optional!=nil&&string(*got.Optional)==payload}
  if got.Id!=42||!validJSON||got.Amount==nil||!got.Amount.Equal(amount)||len(got.Amounts)!=1||!got.Amounts[0].Equal(amount)||len(got.Names)!=2||got.Names[1]!="中文"||len(got.Numbers)!=2||got.Numbers[1]!=9223372036854775807{t.Fatalf("roundtrip payload=%s selected=%v: %+v",payload,selected,got)}
 }
 }
}
`
			var additional []string
			if name == "R" {
				additional = append(additional, groupResultNameAssertions)
			}
			runGeneratedFixture(t, source, assertions, StableIDs{ModelFields: map[string]map[string]int{name: {"id": 1, "optional": 2, "amount": 3, "amounts": 4, "names": 5, "numbers": 6}}}, additional...)
		})
	}
}

const groupResultNameAssertions = `package generated
import("context";"github.com/light-speak/luxo/pkg/lux/pg")
func assertIndependentGroupResult(query *RQuery,ctx context.Context)([]int64,error){
 return query.GroupBy(ctx,[]string{"id"},nil,func(rows pg.Rows)(int64,error){var value int64;err:=rows.Scan(&value);return value,err})
}
`

func TestGeneratedExtensionScannerScalarBoundaries(t *testing.T) {
	runGeneratedFixture(t, `extend Reading { amount: Float? }
extend Reading { at: DateTime? }`, `package generated
import("math";"testing";"time";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/jackc/pgx/v5/pgtype";"github.com/jackc/pgx/v5/pgconn")
type extensionRows struct{pg.Rows;mapping *pgtype.Map;fields []pgconn.FieldDescription;values []any}
func(r *extensionRows)FieldDescriptions()[]pgconn.FieldDescription{return r.fields}
func(r *extensionRows)Scan(dest ...any)error{for i,f:=range r.fields{
 raw,err:=r.mapping.Encode(f.DataTypeOID,f.Format,r.values[i],nil);if err!=nil{return err}
 if err:=r.mapping.Scan(f.DataTypeOID,f.Format,raw,dest[i]);err!=nil{return err}
};return nil}
func TestExtensionBoundary(t *testing.T){
 for _,format:=range []int16{pgtype.TextFormatCode,pgtype.BinaryFormatCode}{
  rows:=&extensionRows{mapping:pgtype.NewMap(),fields:[]pgconn.FieldDescription{
   {Name:"amount",DataTypeOID:pgtype.Float8OID,Format:format},{Name:"at",DataTypeOID:pgtype.TimestamptzOID,Format:format},
  }}
  for _,sample:=range []struct{values []any;valid bool}{
   {[]any{nil,nil},true},{[]any{1.25,time.Date(0,1,1,0,0,0,0,time.UTC)},true},
   {[]any{math.NaN(),nil},false},{[]any{nil,time.Date(-1,1,1,0,0,0,0,time.UTC)},false},
  }{rows.values=sample.values;value,err:=scanReading(rows)
   if (err==nil)!=sample.valid{t.Fatalf("extension=%+v error=%v",value,err)}
   if !sample.valid&&value!=nil{t.Fatal("invalid partial extension returned")}
  }
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"Reading": {"id": 1, "amount": 2, "at": 3}}})
}

func TestGeneratedExtensionScalarImports(t *testing.T) {
	runGeneratedFixture(t, `extend Reading {
 at: DateTime?
 elapsed: [Duration]
 key: UUID?
 amount: Decimal
 payload: JSON?
 secret: String @hash
}
extend Reading { dates: [DateTime] }`, `package generated
import "testing"
func TestCompiledExtension(t *testing.T) { value:=Reading{};if value.At!=nil||value.Key!=nil||value.Payload!=nil{t.Fatal("invalid zero value")} }
`, StableIDs{ModelFields: map[string]map[string]int{"Reading": {"id": 1, "at": 2, "elapsed": 3, "key": 4, "amount": 5, "payload": 6, "dates": 7, "secret": 8}}})
}

func TestGeneratedScannerValidationBenchmark(t *testing.T) {
	model := &ast.ModelDecl{Name: "Reading", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "amount", Type: &ast.TypeRef{Name: "Float"}},
		{Name: "optional", Type: &ast.TypeRef{Name: "Float", Nullable: true}},
		{Name: "amounts", Type: &ast.TypeRef{Name: "Float", IsList: true}},
		{Name: "at", Type: &ast.TypeRef{Name: "DateTime"}},
		{Name: "times", Type: &ast.TypeRef{Name: "DateTime", IsList: true}},
	}}
	var current strings.Builder
	generateScanner(&current, model, nil)
	previous := strings.Replace(current.String(), "func scanReading(", "func scanReadingPrevious(", 1)
	for _, field := range model.Fields {
		if !checkedScanField(field, nil) {
			continue
		}
		name := strings.ToUpper(field.Name[:1]) + field.Name[1:]
		previous = strings.ReplaceAll(previous, "\tvar _check"+name+" bool\n", "")
		previous = strings.ReplaceAll(previous, "\t\t\t_check"+name+" = true\n", "")
		var check strings.Builder
		writeScanFieldValidation(&check, "value", field, nil)
		previous = strings.Replace(previous, check.String(), "", 1)
	}
	previous = strings.Replace(previous, "\tif err := rows.Scan(dests...); err != nil { return nil, err }\n", "", 1)
	previous = strings.Replace(previous, "return &value, nil", "return &value, rows.Scan(dests...)", 1)
	source := "package generated\nimport(\"time\";\"github.com/light-speak/luxo/pkg/lux/pg\")\ntype Reading struct {Id int64;Amount float64;Optional *float64;Amounts []float64;At time.Time;Times []time.Time}\n" + current.String() + previous
	runGeneratedProtocolPackage(t, source, scannerValidationBenchmark)
}

const scannerValidationBenchmark = `package generated
import("math";"testing";"time";"reflect";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/jackc/pgx/v5/pgconn";"github.com/jackc/pgx/v5/pgtype")
type scanValidationRows struct{pg.Rows;field pgconn.FieldDescription;raw []byte;mapping *pgtype.Map}
func(r *scanValidationRows)FieldDescriptions()[]pgconn.FieldDescription{return []pgconn.FieldDescription{r.field}}
func(r *scanValidationRows)Scan(dest ...any)error{return r.mapping.Scan(r.field.DataTypeOID,r.field.Format,r.raw,dest[0])}
func validationSamples(tb testing.TB)map[string]*scanValidationRows{
 tb.Helper();at:=time.Date(2026,1,1,0,0,0,123456000,time.UTC)
 floats:=make([]float64,128);times:=make([]time.Time,128)
 for i:=range floats{floats[i]=float64(i)+0.25;times[i]=at}
 samples:=[]struct{name,field string;oid uint32;value any}{
  {"unselected","id",pgtype.Int8OID,int64(42)},
  {"float","amount",pgtype.Float8OID,1.25},
  {"optional","optional",pgtype.Float8OID,math.SmallestNonzeroFloat64},
  {"null","optional",pgtype.Float8OID,nil},
  {"floats128","amounts",pgtype.Float8ArrayOID,floats},
  {"date","at",pgtype.TimestamptzOID,at},
  {"dates128","times",pgtype.TimestamptzArrayOID,times},
 }
 result:=make(map[string]*scanValidationRows,len(samples))
 for _,sample:=range samples{mapping:=pgtype.NewMap();raw,err:=mapping.Encode(sample.oid,pgtype.BinaryFormatCode,sample.value,nil);if err!=nil{tb.Fatal(err)}
  result[sample.name]=&scanValidationRows{field:pgconn.FieldDescription{Name:sample.field,DataTypeOID:sample.oid,Format:pgtype.BinaryFormatCode},raw:raw,mapping:mapping}
 };return result
}
func TestValidationBenchmarkParity(t *testing.T){
 for name,rows:=range validationSamples(t){before,err:=scanReadingPrevious(rows);if err!=nil{t.Fatal(err)};after,err:=scanReading(rows)
  if err!=nil||!reflect.DeepEqual(before,after){t.Fatalf("%s: mismatch %v %v %v",name,before,after,err)}
 }
}
var scannedReading *Reading
func BenchmarkScanValidation(b *testing.B){
 samples:=validationSamples(b)
 for _,name:=range []string{"unselected","float","optional","null","floats128","date","dates128"}{
  rows:=samples[name]
  for _,entry:=range []struct{name string;scan func(pg.Rows)(*Reading,error)}{{"before",scanReadingPrevious},{"after",scanReading}}{
   b.Run(name+"/"+entry.name,func(b *testing.B){if _,err:=entry.scan(rows);err!=nil{b.Fatal(err)};b.ReportAllocs();b.ResetTimer()
    for b.Loop(){value,err:=entry.scan(rows);if err!=nil{b.Fatal(err)};scannedReading=value}
   })
  }
 }
}
`

func TestGeneratedScannerDatabaseScalarBoundaries(t *testing.T) {
	runGeneratedFixture(t, `model Reading @noTime {
 id: Int @id
 amount: Float?
 amounts: [Float]?
 at: DateTime?
}`, `package generated
import("fmt";"math";"testing";"time";"github.com/light-speak/luxo/internal/testutil";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/jackc/pgx/v5";"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres")
func TestDatabaseScalarBoundary(t *testing.T){
 container,err:=postgres.Run(t.Context(),testutil.PostgresImage(),postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
 testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE readings(id bigint PRIMARY KEY,amount double precision,amounts double precision[],at timestamptz)");err!=nil{t.Fatal(err)}
 samples:=[]struct{name,amount,amounts,at string;valid bool}{
  {"null","NULL","NULL","NULL",true},
  {"finite","'-0'::float8","ARRAY[1.25,2.5]::float8[]","'2026-01-01 00:00:00.123456+00'::timestamptz",true},
  {"year-zero","1.25","ARRAY[]::float8[]","'0001-01-01 BC'::timestamptz",true},
  {"nan","'NaN'::float8","NULL","NULL",false},
  {"infinity","'Infinity'::float8","NULL","NULL",false},
  {"negative-infinity","'-Infinity'::float8","NULL","NULL",false},
  {"bad-list","NULL","ARRAY[1,'NaN'::float8]","NULL",false},
  {"upper-year","NULL","NULL","'10000-01-01+00'::timestamptz",false},
  {"lower-year","NULL","NULL","'0002-01-01 BC'::timestamptz",false},
  {"infinite-date","NULL","NULL","'infinity'::timestamptz",false},
 }
 for i,sample:=range samples{
  id:=int64(i+1)
  sql:="INSERT INTO readings VALUES ($1,"+sample.amount+","+sample.amounts+","+sample.at+")"
  if _,err:=pg.Exec(t.Context(),db,sql,id);err!=nil{t.Fatalf("seed %s: %v",sample.name,err)}
  for _,mode:=range []pgx.QueryExecMode{pgx.QueryExecModeCacheStatement,pgx.QueryExecModeSimpleProtocol}{t.Run(fmt.Sprintf("%s/%v",sample.name,mode),func(t *testing.T){
   value,err:=pg.QueryRow(t.Context(),db,scanReading,"SELECT id,amount,amounts,at FROM readings WHERE id=$1",mode,id)
   if (err==nil)!=sample.valid{t.Fatalf("value=%+v error=%v",value,err)}
   if !sample.valid&&value!=nil{t.Fatal("partial model escaped")}
   if sample.valid{
    if value==nil||value.Id!=id{t.Fatal("missing valid row")}
    switch sample.name{
     case "null":if value.Amount!=nil||value.At!=nil||value.Amounts!=nil{t.Fatal("NULL became a value")}
     case "finite":if value.Amount==nil||!math.Signbit(*value.Amount)||len(value.Amounts)!=2||value.Amounts[1]!=2.5||value.At==nil||!value.At.Equal(time.Date(2026,1,1,0,0,0,123456000,time.UTC)){t.Fatalf("valid precision lost: %+v",value)}
     case "year-zero":if value.At==nil||value.At.UTC().Year()!=0||len(value.Amounts)!=0{t.Fatal("year zero lost")}
    }
   }
   selected,err:=pg.QueryRow(t.Context(),db,scanReading,"SELECT id FROM readings WHERE id=$1",mode,id)
   if err!=nil||selected==nil||selected.Id!=id{t.Fatalf("unselected bad field affected projection: %+v %v",selected,err)}
  })}
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"Reading": {"id": 1, "amount": 2, "amounts": 3, "at": 4}}})
}

func TestGeneratedEnumScannerHTTPProjection(t *testing.T) {
	runGeneratedFixture(t, `
enum Role { USER ADMIN }
model Account @noTime { id: Int @id role: Role }
api accounts(): [Account] { return Account.where { it.id > 0 }.select().all() }
api fullAccounts(): [Account] { return Account.where { it.id > 0 }.all() }
`, `package generated
import("bytes";"encoding/json";"fmt";"net/http/httptest";"os";"testing"
"github.com/light-speak/luxo/pkg/lux/api"
"github.com/light-speak/luxo/pkg/lux/codec"
"github.com/light-speak/luxo/pkg/lux/pg"
"github.com/testcontainers/testcontainers-go"
"github.com/testcontainers/testcontainers-go/modules/postgres")
func TestEnumProjection(t *testing.T){
 image:=os.Getenv("LUXO_TEST_POSTGRES_IMAGE");if image==""{image="postgres:17-alpine"}
 container,err:=postgres.Run(t.Context(),image,postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
 testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE accounts(id bigint PRIMARY KEY,role text); INSERT INTO accounts VALUES(1,'UNKNOWN')");err!=nil{t.Fatal(err)}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,NewFromDB(db));err!=nil{t.Fatal(err)}
 for _,binary:=range []bool{false,true}{t.Run(fmt.Sprint(binary),func(t *testing.T){
  for _,role:=range []string{"UNKNOWN","","ADMIN"}{
   if _,err:=pg.Exec(t.Context(),db,"UPDATE accounts SET role=$1",role);err!=nil{t.Fatal(err)}
   full:=groupedResponse(t,router,"fullAccounts","id",[]byte{1},binary)
   if (full.Code==200)!=(role=="ADMIN"){t.Fatalf("full model read: role=%q status=%d",role,full.Code)}
   for _,selected:=range []bool{false,true}{
    fields,mask:="id",[]byte{1};if selected{fields,mask="id,role",[]byte{3}}
    response:=groupedResponse(t,router,"accounts",fields,mask,binary)
    if selected&&role!="ADMIN"{if response.Code==200{t.Fatalf("invalid enum returned success: %q",response.Body.Bytes())};continue}
    if response.Code!=200{t.Fatalf("projection=%v role=%q status=%d body=%s",selected,role,response.Code,response.Body.String())}
    if binary{
     values,err:=ReadColumnarAccount(response.Body.Bytes());if err!=nil||len(values)!=1{t.Fatalf("decode: %+v %v",values,err)}
     if values[0].Id!=1{t.Fatal("id lost")};if selected&&values[0].Role!=RoleADMIN{t.Fatal("enum lost")}
     if !selected&&values[0].Role!=""{t.Fatal("unselected enum leaked")}
    }else{
     var envelope struct{Data []map[string]json.RawMessage};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil{t.Fatal(err)}
     if len(envelope.Data)!=1||string(envelope.Data[0]["id"])!="1"{t.Fatal(response.Body.String())}
     value,present:=envelope.Data[0]["role"];if present!=selected{t.Fatal("projection changed")}
     if selected {var decoded string;if err:=json.Unmarshal(value,&decoded);err!=nil||decoded!="ADMIN"{t.Fatal("enum lost",err)}}
    }
   }
  }
 })}
}
`+typedGroupRequestHelper, StableIDs{ModelFields: map[string]map[string]int{"Account": {"id": 1, "role": 2}}})
}

func BenchmarkDurationScannerGeneration(b *testing.B) {
	for _, kind := range []string{"Int", "Duration", "Duration?", "[Duration]", "[Duration]?"} {
		b.Run(kind, func(b *testing.B) {
			input := analyzePermissionFixture(b, "model Reading @noTime { id: Int @id elapsed: "+kind+" }")
			model := input.Files[0].Models[0]
			b.ReportAllocs()
			for b.Loop() {
				var source strings.Builder
				generateScanner(&source, model, nil)
				if source.Len() == 0 {
					b.Fatal("empty scanner")
				}
			}
		})
	}
}
