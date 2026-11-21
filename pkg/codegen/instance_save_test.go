package codegen

import (
	"os"
	"strings"
	"testing"
)

const instanceSaveSource = `
model Entry @noTime {
 id: Int @id
 name: String
 count: Int @beforeSave { it + 1 }
 optional: Int?
 tags: [String]
 data: Bytes
 secret: String @hash
 locked: String @immutable
 privateValue: String @internal
}
model Untouched @noTime { id: Int @id name: String }
model Timed { id: Int @id value: Int optional: Int? @beforeSave { it + 1 } updatedAt: DateTime }
fn persist(value: Entry): Entry { value.save() return value }
fn clearName(value: Entry): Entry { value.name = "" value.save() return value }
fn safePersist(value: Entry?): Entry? { return value?.save() }
fn returnPersist(value: Entry): Entry { return value.save() }
fn literalPersist(id: Int): Entry { return (Entry { id: id name: "" }).save() }
fn queryPersist(id: Int): Entry { return Entry.find(id: id).save() }
fn persistTimed(value: Timed): Timed { return value.save() }
`

func instanceSaveIDs() StableIDs {
	return StableIDs{ModelFields: map[string]map[string]int{
		"Entry":     {"id": 1, "name": 2, "count": 3, "optional": 4, "tags": 5, "data": 6, "secret": 7, "locked": 8, "privateValue": 9},
		"Untouched": {"id": 1, "name": 2},
		"Timed":     {"id": 1, "value": 2, "optional": 3, "updatedAt": 4},
	}}
}

func TestGenerateRejectsSaveQueryReceivers(t *testing.T) {
	for _, expression := range []string{"Entry.save()", "Entry.where(id > 0).save()", "val query = Entry.where(id > 0) query.save()", "val value = Entry.save", "val value = Entry.where(id > 0).save", "val value = row.save"} {
		t.Run(expression, func(t *testing.T) {
			input := analyzePermissionFixture(t, "model Entry @noTime { id: Int @id name: String }\nfn change(row: Entry) { "+expression+" }")
			generated, err := Generate(input, "generated", GeneratorConfig{})
			if generated != nil || err == nil || !strings.Contains(err.Error(), "save requires a") {
				t.Fatalf("formal generation accepted a query save or returned partial output: %v", err)
			}
			if len(input.Errors) != 1 || input.Errors[0].Pos.Line != 2 {
				t.Fatalf("query save lost its source diagnostic: %v", input.Errors)
			}
		})
	}
}

func TestGeneratedInstanceSaveReceiverContracts(t *testing.T) {
	runGeneratedFixture(t, `model Entry @noTime { id: Int @id name: String }
model SavedField @noTime { id: Int @id save: String }
fn persist(value: Entry): Entry { return value.save() }
fn safePersist(value: Entry?): Entry? { return value?.save() }
fn shadowPersist(Entry: Entry): Entry { return Entry.save() }
fn queryPersist(): Entry { return Entry.find(id: 1).save() }
fn createPersist(): Entry { return Entry.create(id: 1, name: "new").save() }
fn literalPersist(): Entry { return (Entry { id: 1 name: "new" }).save() }
fn countEntries(): Int { val query = Entry.where(id > 0) return query.count() }
fn readSaved(value: SavedField): String { return value.save }
`, `package generated
import "testing"
func TestInstanceReceiverNoOp(t *testing.T) {
 app:=&App{Entry:&EntryClient{}}
 value:=&Entry{Id:1,Name:"ready",_luxoSaveLoaded:[1]uint64{3}};value._luxoSaveCapture()
 if got,err:=app.persist(t.Context(),value);err!=nil||got!=value{t.Fatal(got,err)}
 if got,err:=app.shadowPersist(t.Context(),value);err!=nil||got!=value{t.Fatal(got,err)}
 if got,err:=app.safePersist(t.Context(),nil);err!=nil||got!=nil{t.Fatal(got,err)}
 if got,err:=app.readSaved(t.Context(),&SavedField{Save:"kept"});err!=nil||got!="kept"{t.Fatal(got,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Entry": {"id": 1, "name": 2}, "SavedField": {"id": 1, "save": 2}}})
}

func TestGeneratedInstanceSavePostgres(t *testing.T) {
	if os.Getenv("LUXO_AUDIT_POSTGRES_URL") == "" {
		t.Skip("requires an isolated LUXO_AUDIT_POSTGRES_URL")
	}
	runGeneratedFixture(t, instanceSaveSource, instanceSavePostgresAssertions, instanceSaveIDs())
}

const instanceSavePostgresAssertions = `package generated
import("bytes";"context";"fmt";"math";"net/url";"os";"testing";"time";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/light-speak/luxo/pkg/lux/crypto")
func saveDatabase(t testing.TB)*pg.DB{
 t.Helper();ctx:=t.Context();connection:=os.Getenv("LUXO_AUDIT_POSTGRES_URL");admin,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)}
 name:=fmt.Sprintf("instance_save_%d",time.Now().UnixNano());if _,err:=pg.Exec(ctx,admin,"CREATE SCHEMA "+name);err!=nil{admin.Close();t.Fatal(err)}
 t.Cleanup(func(){cleanup,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel();if _,err:=pg.Exec(cleanup,admin,"DROP SCHEMA "+name+" CASCADE");err!=nil{t.Error(err)};admin.Close()})
 parsed,err:=url.Parse(connection);if err!=nil{t.Fatal(err)};query:=parsed.Query();query.Set("search_path",name);parsed.RawQuery=query.Encode();db,err:=pg.NewDB(ctx,parsed.String());if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE entrys(id bigint PRIMARY KEY,name text NOT NULL CHECK(name <> 'reject'),count bigint NOT NULL,optional bigint,tags text[],data bytea,secret text NOT NULL,locked text NOT NULL,private_value text NOT NULL); INSERT INTO entrys VALUES(7,'original',10,2,ARRAY['one'],decode('01','hex'),'existing-hash','locked','private'); CREATE TABLE timeds(id bigint PRIMARY KEY,value bigint NOT NULL,optional bigint,updated_at timestamptz NOT NULL); INSERT INTO timeds VALUES(1,10,2,'2000-01-01T00:00:00Z')");err!=nil{t.Fatal(err)}
 return db
}
func TestInstanceSavePostgres(t *testing.T){
 db:=saveDatabase(t);app:=NewFromDB(db);ctx:=t.Context()
 if got,err:=app.queryPersist(ctx,7);err!=nil||got==nil||got.Count!=10{t.Fatal("query receiver",got,err)}
 read:=func()*Entry{t.Helper();v,err:=app.Entry.Find(ctx,7);if err!=nil{t.Fatal(err)};return v}
 partial:=func(columns ...string)*Entry{t.Helper();v,err:=app.Entry.Where(EntryWhere.Id.Eq(7)).Select(columns...).First(ctx);if err!=nil||v==nil{t.Fatal(v,err)};return v}
 value:=partial("id","name");value.Name="changed";if got,err:=app.persist(ctx,value);err!=nil||got!=value{t.Fatal(got,err)}
 stored:=read();if stored.Name!="changed"||stored.Count!=10||stored.Secret!="existing-hash"||stored.Locked!="locked"||stored.PrivateValue!="private"{t.Fatal("partial save overwrote unloaded/protected field",stored)}
 value=partial("id");if _,err:=app.clearName(ctx,value);err!=nil{t.Fatal(err)};if read().Name!=""{t.Fatal("explicit zero on unloaded field was lost")}
 value=partial("name");value.Name="bad-target";if _,err:=app.persist(ctx,value);err==nil{t.Fatal("missing primary key wrote a row")}
 value=read();value.Count=20;value.Secret=" plaintext ";value.Tags[0]="two";value.Data[0]=2;*value.Optional=3
 if _,err:=app.returnPersist(ctx,value);err!=nil{t.Fatal(err)}
 if value.Count!=21||!crypto.VerifyPassword(" plaintext ",value.Secret)||value.Tags[0]!="two"||value.Data[0]!=2||*value.Optional!=3{t.Fatal("prepared result/native mutation not reflected",value)}
 hash:=value.Secret;if _,err:=app.persist(ctx,value);err!=nil{t.Fatal(err)};if value.Count!=21||value.Secret!=hash||read().Secret!=hash{t.Fatal("repeated save applied hash/beforeSave twice")}
 value.Count=math.MaxInt64;if _,err:=app.persist(ctx,value);err==nil{t.Fatal("beforeSave preparation error swallowed")};if value.Count!=math.MaxInt64||read().Count!=21{t.Fatal("failed preparation mutated source/database")}
 value=read();value.Name="reject";value.Count=30;value.Secret="retry";prior:=value._luxoSaveOriginal.Count
 if _,err:=app.persist(ctx,value);err==nil{t.Fatal("database failure swallowed")};if value.Count!=30||value.Secret!="retry"||value._luxoSaveOriginal.Count!=prior{t.Fatal("failed save mutated source/snapshot")}
 value.Name="retried";if _,err:=app.persist(ctx,value);err!=nil{t.Fatal(err)};if value.Count!=31||!crypto.VerifyPassword("retry",value.Secret){t.Fatal("retry lost pending changes")}
 value=read();value.Locked="forged";value.PrivateValue="forged";value.Name="allowed";if _,err:=app.persist(ctx,value);err!=nil{t.Fatal(err)};stored=read();if stored.Locked!="locked"||stored.PrivateValue!="private"{t.Fatal("save expanded writable fields")}
 value=read();value.Id=8;value.Name="bad-id";if _,err:=app.persist(ctx,value);err==nil{t.Fatal("primary key replacement accepted")};if read().Name!="allowed"{t.Fatal("wrong row changed")}
 if _,err:=app.literalPersist(ctx,7);err!=nil{t.Fatal("literal receiver",err)};if read().Name!=""||read().Count!=31{t.Fatal("partial literal wrote missing zero fields")}
 native:=&Entry{Id:7,Name:"native",Count:0,Secret:"native-secret",Tags:[]string{},Data:[]byte{}}
 if _,err:=app.persist(ctx,native);err!=nil{t.Fatal("native complete Go model rejected",err)};stored=read();if stored.Name!="native"||stored.Count!=1||stored.Optional!=nil||len(stored.Tags)!=0||!bytes.Equal(stored.Data,[]byte{})||!crypto.VerifyPassword("native-secret",stored.Secret){t.Fatal("native complete zero-valued fields lost",stored)}
 if got,err:=app.safePersist(ctx,nil);err!=nil||got!=nil{t.Fatal("nil safe save",got,err)};if got,err:=app.safePersist(ctx,native);err!=nil||got!=native{t.Fatal("safe save",got,err)}
 timed,err:=app.Timed.Find(ctx,1);if err!=nil{t.Fatal(err)};previous:=timed.UpdatedAt;alias:=timed.Optional;*alias=20
 if _,err:=app.persistTimed(ctx,timed);err!=nil{t.Fatal(err)};if *timed.Optional!=21||*alias!=20||!timed.UpdatedAt.After(previous){t.Fatal("nullable preparation/automatic timestamp failed",timed,*alias)}
 previous=timed.UpdatedAt;if _,err:=app.persistTimed(ctx,timed);err!=nil{t.Fatal(err)};if !timed.UpdatedAt.Equal(previous)||*timed.Optional!=21{t.Fatal("unchanged save transformed nullable field or timestamp twice")}
 alias=timed.Optional;*alias=math.MaxInt64;if _,err:=app.persistTimed(ctx,timed);err==nil{t.Fatal("nullable preparation failure swallowed")};if *alias!=math.MaxInt64||timed._luxoSaveOriginal.Optional.value!=21{t.Fatal("nullable failure changed source/baseline")}
 value=read();if _,err:=pg.Exec(ctx,db,"DELETE FROM entrys WHERE id=7");err!=nil{t.Fatal(err)};value.Name="gone";if _,err:=app.persist(ctx,value);err==nil{t.Fatal("missing row reported success")}
}

func BenchmarkInstanceSaveSQL(b *testing.B){
 db:=saveDatabase(b);app:=NewFromDB(db);ctx:=b.Context()
 for _,method:=range []string{"typed-update","instance-save"}{b.Run(method,func(b *testing.B){
  value,err:=app.Entry.Find(ctx,7);if err!=nil{b.Fatal(err)};next:=false;b.ReportAllocs()
  for b.Loop(){next=!next;name:="first";if next{name="second"};if method=="typed-update"{_,err=newEntryUpdateBuilder(db,"entrys",7).SetName(name).Exec(ctx)}else{value.Name=name;_,err=app.persist(ctx,value)};if err!=nil{b.Fatal(err)}}
 })}
}

`

func TestInstanceSaveRequiresDeclaredPrimaryKey(t *testing.T) {
	input := analyzePermissionFixture(t, `model Entry @noTime { name: String } fn persist(value: Entry): Entry { value.save() return value }`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	generated, err := Generate(input, "generated", GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"Entry": {"name": 1}}}})
	if generated != nil || err == nil || !strings.Contains(err.Error(), "save model Entry requires a declared primary key") || !strings.Contains(err.Error(), ":1:") {
		t.Fatalf("missing primary key emitted partial output or lost location: %v", err)
	}
}

func TestGeneratedInstanceSaveTypeCoverage(t *testing.T) {
	runGeneratedFixture(t, `
enum State { ON OFF }
model Complex @noTime @soft {
 id: UUID @id
 moment: DateTime?
 number: Decimal?
 document: JSON?
 documents: [JSON]
 blobs: [Bytes]
 states: [State]?
 moments: [DateTime]
 numbers: [Decimal]
 enabled: Boolean?
 _luxoSaveOriginal: String
}
fn persist(value: Complex): Complex { return value.save() }
`, `package generated
import("encoding/json";"testing";"time";"github.com/google/uuid";"github.com/shopspring/decimal";"github.com/light-speak/luxo/pkg/lux/codec")
func TestStaticSnapshotTypes(t *testing.T){
 now:=time.Now();number:=decimal.New(12,-1);doc:=json.RawMessage("null");enabled:=true
 value:=&Complex{Id:uuid.New(),Moment:&now,Number:&number,Document:&doc,Documents:[]json.RawMessage{json.RawMessage("{}")},Blobs:[][]byte{{1}},States:[]State{StateON},Moments:[]time.Time{now},Numbers:[]decimal.Decimal{number},Enabled:&enabled,_luxoSaveOriginal:"user-field"}
 value._luxoSave_Capture()
 if value._luxoSave_ChangedMoment()||value._luxoSave_ChangedNumber()||value._luxoSave_ChangedDocument()||value._luxoSave_ChangedStates()||value._luxoSave_ChangedMoments()||value._luxoSave_ChangedNumbers()||value._luxoSave_ChangedEnabled()||value._luxoSave_Changed_luxoSaveOriginal(){t.Fatal("unchanged values treated as dirty")}
 now=now.Add(time.Second);number=decimal.New(13,-1);doc[0]='N';value.Documents[0][0]='[';value.Blobs[0][0]=2;value.States[0]=StateOFF;value.Moments[0]=now;value.Numbers[0]=number;enabled=false;value._luxoSaveOriginal="modified"
 if !value._luxoSave_ChangedMoment()||!value._luxoSave_ChangedNumber()||!value._luxoSave_ChangedDocument()||!value._luxoSave_ChangedDocuments()||!value._luxoSave_ChangedBlobs()||!value._luxoSave_ChangedStates()||!value._luxoSave_ChangedMoments()||!value._luxoSave_ChangedNumbers()||!value._luxoSave_ChangedEnabled()||!value._luxoSave_Changed_luxoSaveOriginal(){t.Fatal("native mutation was lost")}
 value.Moment=nil;value.Number=nil;value.Document=nil;value.Enabled=nil;value.States=nil;value._luxoSave_Capture()
 if value._luxoSave_ChangedMoment()||value._luxoSave_ChangedNumber()||value._luxoSave_ChangedDocument()||value._luxoSave_ChangedEnabled()||value._luxoSave_ChangedStates(){t.Fatal("null snapshot not refreshed")}
 value.States=[]State{};if !value._luxoSave_ChangedStates(){t.Fatal("null/empty array distinction lost")}
 var input Complex;if err:=input.ReadJSONInput([]byte("{\"id\":\"00000000-0000-0000-0000-000000000001\",\"states\":null}"));err!=nil{t.Fatal(err)};if !input._luxoSave_ChangedStates()||input._luxoSave_ChangedNumber(){t.Fatal("nullable JSON input presence lost")}
 encoder:=codec.NewEncoder([]byte{0});encoder.WriteFieldUUID(1,uuid.Nil);encoder.WriteFieldBool(7,false);encoder.WriteEnd();var decoded Complex;decoder:=codec.NewDecoder(encoder.Bytes());decoded.ReadLuxoInput(decoder);if err:=decoder.Finish();err!=nil{t.Fatal(err)};if !decoded._luxoSave_ChangedStates(){t.Fatal("nullable Binary input presence lost")}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Complex": {"id": 1, "moment": 2, "number": 3, "document": 4, "documents": 5, "blobs": 6, "states": 7, "moments": 8, "numbers": 9, "enabled": 10, "_luxoSaveOriginal": 11}}})
}

func TestGeneratedInstanceSaveState(t *testing.T) {
	runGeneratedFixture(t, instanceSaveSource, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/api";"github.com/jackc/pgx/v5/pgconn")
type saveRows struct { pg.Rows; fields []pgconn.FieldDescription }
func(r *saveRows)FieldDescriptions()[]pgconn.FieldDescription{return r.fields}
func(r *saveRows)Scan(dest ...any)error{for i,field:=range r.fields{switch field.Name{case "id":*dest[i].(*int64)=7;case "name":*dest[i].(*string)="original";case "tags":*dest[i].(*[]string)=[]string{"one"};case "data":*dest[i].(*[]byte)=[]byte{1};case "optional":v:=int64(2);*dest[i].(**int64)=&v}};return nil}
func TestSaveSnapshot(t *testing.T){
 value,err:=scanEntry(&saveRows{fields:[]pgconn.FieldDescription{{Name:"id"},{Name:"name"},{Name:"tags"},{Name:"data"},{Name:"optional"}}});if err!=nil{t.Fatal(err)}
 if !value._luxoSaveHasSnapshot{t.Fatal("scan did not retain snapshot")}
 if value._luxoSaveChangedName()||value._luxoSaveChangedCount(){t.Fatal("unchanged/unselected fields marked changed")}
 value.Tags[0]="two";value.Data[0]=2;*value.Optional=3
 if !value._luxoSaveChangedTags()||!value._luxoSaveChangedData()||!value._luxoSaveChangedOptional(){t.Fatal("native mutable field change lost")}
 if value._luxoSaveOriginal.Tags[0]!="one"||value._luxoSaveOriginal.Data[0]!=1||value._luxoSaveOriginal.Optional.value!=2{t.Fatal("snapshot aliases mutable values")}
 value._luxoSaveCapture();if value._luxoSaveChangedTags()||value._luxoSaveChangedData()||value._luxoSaveChangedOptional(){t.Fatal("snapshot not refreshed")}
 client:=&EntryClient{}
 if _,err:=client.save(t.Context(),&Entry{_luxoSaveConstructed:true});err==nil{t.Fatal("unloaded primary key accepted")}
 var missing *Entry;if _,err:=client.save(t.Context(),missing);err==nil{t.Fatal("nil instance accepted")}
 value.Id=8;if _,err:=client.save(t.Context(),value);err==nil{t.Fatal("changed primary key accepted")}
 value.Id=7;value.Name="original";if got,err:=client.save(t.Context(),value);err!=nil||got!=value{t.Fatal("no-op save executed SQL",got,err)}
 var input Entry;if err:=input.ReadJSONInput([]byte("{\"id\":7,\"name\":\"\"}"));err!=nil{t.Fatal(err)}
 if !input._luxoSaveChangedName()||input._luxoSaveChangedCount(){t.Fatal("JSON input lost partial presence")}
 encoder:=codec.NewEncoder([]byte{0});encoder.WriteFieldInt(1,7);if err:=encoder.WriteFieldString(2,"");err!=nil{t.Fatal(err)};encoder.WriteEnd()
 var binary Entry;decoder:=codec.NewDecoder(encoder.Bytes());binary.ReadLuxoInput(decoder);if err:=decoder.Finish();err!=nil{t.Fatal(err)}
 if !binary._luxoSaveChangedName()||binary._luxoSaveChangedCount(){t.Fatal("Binary input lost partial presence")}
 var response Entry;decoder=codec.NewDecoder(encoder.Bytes());response.ReadLuxo(decoder);if err:=decoder.Finish();err!=nil{t.Fatal(err)}
 if !response._luxoSaveHasSnapshot||response._luxoSaveChangedName()||response._luxoSaveChangedCount(){t.Fatal("response decoded as fresh native full model")}
 mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)};out:=&api.ResponseBuf{};if err:=WriteColumnarEntry(out,[]*Entry{value},mask);err!=nil{t.Fatal(err)};items,err:=ReadColumnarEntry(out.B);if err!=nil||len(items)!=1{t.Fatal(items,err)}
 if !items[0]._luxoSaveHasSnapshot||items[0]._luxoSaveChangedName(){t.Fatal("columnar response lost snapshot")}
}
`, instanceSaveIDs())
}

func TestGeneratedInstanceSaveRelationAssignment(t *testing.T) {
	runGeneratedFixture(t, `
model Child @noTime { id: Int @id parentId: Int }
model Parent @noTime { id: Int @id child: Child }
fn reassociate(value: Parent, next: Child): Parent { value.child = next return value.save() }
`, `package generated
import "testing"
func TestSavePreservesRelationIdentity(t *testing.T){
 value:=&Parent{Id:1,_luxoSaveLoaded:[1]uint64{1}};value._luxoSaveCapture();next:=&Child{Id:2}
 app:=&App{Parent:&ParentClient{}}
 got,err:=app.reassociate(t.Context(),value,next);if err!=nil||got!=value||got.Child!=next{t.Fatal("relation assignment became a database setter or lost identity",got,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Child": {"id": 1, "parentId": 2}, "Parent": {"id": 1, "child": 2}}})
}
