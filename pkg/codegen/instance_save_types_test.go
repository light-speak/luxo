package codegen

import "testing"

func TestGeneratedInstanceSaveMutableAndScalarTypes(t *testing.T) {
	runGeneratedFixture(t, `
model Broad @noTime {
 id: Int @id
 document: JSON?
 blob: Bytes?
 documents: [JSON]
 blobs: [Bytes]
 amount: Decimal
 optionalAmount: Decimal?
 amounts: [Decimal]
 instant: DateTime
 optionalInstant: DateTime?
 instants: [DateTime]
 key: UUID
 optionalKey: UUID?
 keys: [UUID]
}
model Named @noTime { key: String @id value: String }
model Identified @noTime { key: UUID @id value: String }
fn persistBroad(value: Broad): Broad { return value.save() }
fn persistNamed(value: Named): Named { return value.save() }
fn persistIdentified(value: Identified): Identified { return value.save() }
`, instanceSaveTypeAssertions, StableIDs{ModelFields: map[string]map[string]int{
		"Broad": {"id": 1, "document": 2, "blob": 3, "documents": 4, "blobs": 5,
			"amount": 6, "optionalAmount": 7, "amounts": 8, "instant": 9, "optionalInstant": 10,
			"instants": 11, "key": 12, "optionalKey": 13, "keys": 14},
		"Named": {"key": 1, "value": 2}, "Identified": {"key": 1, "value": 2},
	}})
}

const instanceSaveTypeAssertions = `package generated
import("testing";"time";"encoding/json";"github.com/google/uuid";"github.com/shopspring/decimal")
func TestSaveTypedBaseline(t *testing.T) {
 document:=json.RawMessage("[1]");blob:=[]byte{1};amount:=decimal.NewFromInt(1)
 instant:=time.Unix(1,0).UTC();key:=uuid.MustParse("12345678-1234-1234-1234-123456789abc")
 value:=&Broad{Id:7,Document:&document,Blob:&blob,Documents:[]json.RawMessage{json.RawMessage("[1]")},Blobs:[][]byte{{1}},Amount:amount,OptionalAmount:&amount,Amounts:[]decimal.Decimal{amount},Instant:instant,OptionalInstant:&instant,Instants:[]time.Time{instant},Key:key,OptionalKey:&key,Keys:[]uuid.UUID{key}}
 value._luxoSaveCapture()
 if value._luxoSaveChangedDocument()||value._luxoSaveChangedBlob()||value._luxoSaveChangedDocuments()||value._luxoSaveChangedBlobs()||value._luxoSaveChangedAmount()||value._luxoSaveChangedOptionalAmount()||value._luxoSaveChangedAmounts()||value._luxoSaveChangedInstant()||value._luxoSaveChangedOptionalInstant()||value._luxoSaveChangedInstants()||value._luxoSaveChangedKey()||value._luxoSaveChangedOptionalKey()||value._luxoSaveChangedKeys(){t.Fatal("unchanged typed snapshot differs")}
 (*value.Document)[1]='2';(*value.Blob)[0]=2;value.Documents[0][1]='2';value.Blobs[0][0]=2
 if !value._luxoSaveChangedDocument()||!value._luxoSaveChangedBlob()||!value._luxoSaveChangedDocuments()||!value._luxoSaveChangedBlobs(){t.Fatal("snapshot aliases nullable/nested byte storage")}
 value.Amount=decimal.NewFromInt(2);*value.OptionalAmount=decimal.NewFromInt(2);value.Amounts[0]=decimal.NewFromInt(2)
 value.Instant=time.Unix(2,0);*value.OptionalInstant=time.Unix(2,0);value.Instants[0]=time.Unix(2,0)
 value.Key=uuid.Nil;*value.OptionalKey=uuid.Nil;value.Keys[0]=uuid.Nil
 if !value._luxoSaveChangedAmount()||!value._luxoSaveChangedOptionalAmount()||!value._luxoSaveChangedAmounts()||!value._luxoSaveChangedInstant()||!value._luxoSaveChangedOptionalInstant()||!value._luxoSaveChangedInstants()||!value._luxoSaveChangedKey()||!value._luxoSaveChangedOptionalKey()||!value._luxoSaveChangedKeys(){t.Fatal("typed scalar or list mutation not detected")}
 value._luxoSaveCapture();value.Document=nil;value.Blob=nil;value.OptionalAmount=nil;value.OptionalInstant=nil;value.OptionalKey=nil
 if !value._luxoSaveChangedDocument()||!value._luxoSaveChangedBlob()||!value._luxoSaveChangedOptionalAmount()||!value._luxoSaveChangedOptionalInstant()||!value._luxoSaveChangedOptionalKey(){t.Fatal("nullable transition lost")}
 value._luxoSaveCapture();literalNull:=json.RawMessage("null");value.Document=&literalNull
 if !value._luxoSaveChangedDocument(){t.Fatal("SQL null and JSON null baseline collapsed")}
 value.Documents=nil;value.Blobs=nil;value._luxoSaveCapture();value.Documents=[]json.RawMessage{};value.Blobs=[][]byte{}
 if !value._luxoSaveChangedDocuments()||!value._luxoSaveChangedBlobs(){t.Fatal("nil and empty list baseline collapsed")}
 named:=&Named{Key:"row",Value:"original"};named._luxoSaveLoaded[0]=3;named._luxoSaveCapture()
 if got,err:=(&NamedClient{}).save(t.Context(),named);err!=nil||got!=named{t.Fatal("String primary key no-op",got,err)}
 named.Key="different";if _,err:=(&NamedClient{}).save(t.Context(),named);err==nil{t.Fatal("String primary key mutation accepted")}
 identified:=&Identified{Key:uuid.MustParse("12345678-1234-1234-1234-123456789abc"),Value:"original"};identified._luxoSaveLoaded[0]=3;identified._luxoSaveCapture()
 if got,err:=(&IdentifiedClient{}).save(t.Context(),identified);err!=nil||got!=identified{t.Fatal("UUID primary key no-op",got,err)}
 identified.Key=uuid.Nil;if _,err:=(&IdentifiedClient{}).save(t.Context(),identified);err==nil{t.Fatal("UUID primary key mutation accepted")}
}
`
