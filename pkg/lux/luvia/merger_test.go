package luvia

import (
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestMerge_NoExtends(t *testing.T) {
	primary := []byte{0x00, 0x01, 0x02, 0x00} // some data + end marker
	result := Merge(primary, nil)
	if string(result) != string(primary) {
		t.Error("no extends should return primary unchanged")
	}
}

func TestMerge_WithExtend(t *testing.T) {
	// Primary: [arenaHeader=0] [field1=42] [0x00 end]
	var primary []byte
	primary = codec.AppendVarint(primary, 0) // arena header
	primary = codec.AppendVarint(primary, 1) // field ID 1
	primary = codec.AppendSvarint(primary, 42)
	primary = append(primary, 0x00) // end marker

	// Extend: posts data (already encoded, without fieldID prefix)
	var postsData []byte
	postsData = codec.AppendSvarint(postsData, 2) // count = 2
	// Post 1: [arenaHeader][field1=1][0x00]
	postsData = codec.AppendVarint(postsData, 0) // arena
	postsData = codec.AppendVarint(postsData, 1) // field 1
	postsData = codec.AppendSvarint(postsData, 1)
	postsData = append(postsData, 0x00)
	// Post 2: [arenaHeader][field1=2][0x00]
	postsData = codec.AppendVarint(postsData, 0) // arena
	postsData = codec.AppendVarint(postsData, 1) // field 1
	postsData = codec.AppendSvarint(postsData, 2)
	postsData = append(postsData, 0x00)

	extends := []ExtendResult{
		{FieldID: 10, Data: postsData},
	}

	result := Merge(primary, extends)

	// Result should be: [arenaHeader] [field1=42] [field10=postsData] [0x00]
	dec := codec.NewDecoder(result)
	dec.SkipArenaHeader()

	// Field 1: id = 42
	if !dec.NextField() || dec.FieldID() != 1 {
		t.Fatal("expected field 1")
	}
	if v := dec.ReadInt(); v != 42 {
		t.Fatalf("id = %d, want 42", v)
	}

	// Field 10: posts (the data is appended raw after fieldID)
	if !dec.NextField() || dec.FieldID() != 10 {
		t.Fatalf("expected field 10, got %d", dec.FieldID())
	}
	// Read the count
	count := dec.ReadInt() // svarint count = 2
	if count != 2 {
		t.Fatalf("posts count = %d, want 2", count)
	}
}

func TestMerge_MultipleExtends(t *testing.T) {
	var primary []byte
	primary = codec.AppendVarint(primary, 0) // arena
	primary = codec.AppendVarint(primary, 1) // field 1
	primary = codec.AppendSvarint(primary, 1)
	primary = append(primary, 0x00)

	ext1 := []byte{0x05} // some data for field 10
	ext2 := []byte{0x0A} // some data for field 11

	result := Merge(primary, []ExtendResult{
		{FieldID: 10, Data: ext1},
		{FieldID: 11, Data: ext2},
	})

	// Verify: arena header + field1 + field10 data + field11 data + end
	dec := codec.NewDecoder(result)
	dec.SkipArenaHeader()

	if !dec.NextField() || dec.FieldID() != 1 {
		t.Fatal("expected field 1")
	}
	dec.ReadInt()

	if !dec.NextField() || dec.FieldID() != 10 {
		t.Fatalf("expected field 10, got %d", dec.FieldID())
	}
	// 0x05 is data
	_ = result // data is raw, we verified fieldID ordering

	if dec.Err() != nil {
		t.Fatalf("decoder error: %v", dec.Err())
	}
}

func TestMerge_EmptyExtendData(t *testing.T) {
	var primary []byte
	primary = codec.AppendVarint(primary, 0)
	primary = codec.AppendVarint(primary, 1)
	primary = codec.AppendSvarint(primary, 1)
	primary = append(primary, 0x00)

	result := Merge(primary, []ExtendResult{
		{FieldID: 10, Data: nil},      // empty data
		{FieldID: 11, Data: []byte{}}, // empty data
	})

	// Should just be primary without the two empty extend fields
	dec := codec.NewDecoder(result)
	dec.SkipArenaHeader()
	if !dec.NextField() || dec.FieldID() != 1 {
		t.Fatal("expected field 1")
	}
	dec.ReadInt()
	if dec.NextField() {
		t.Fatalf("unexpected field %d after primary (empty extends should be skipped)", dec.FieldID())
	}
}

func TestExtractID(t *testing.T) {
	// Build: [arenaHeader=0] [field1(id)=42] [field2="Alice"] [0x00]
	var buf []byte
	buf = codec.AppendVarint(buf, 0) // arena
	buf = codec.AppendVarint(buf, 1) // field 1
	buf = codec.AppendSvarint(buf, 42)
	buf = codec.AppendVarint(buf, 2) // field 2
	buf = codec.AppendString(buf, "Alice")
	buf = append(buf, 0x00)

	fieldTypes := map[int]codec.FieldSkipType{
		2: codec.SkipBytes, // string
	}
	id, ok := ExtractID(buf, 1, fieldTypes)
	if !ok {
		t.Fatal("expected ok")
	}
	if id != 42 {
		t.Fatalf("id = %d, want 42", id)
	}
}

func TestExtractID_Empty(t *testing.T) {
	_, ok := ExtractID(nil, 1, nil)
	if ok {
		t.Fatal("empty buffer should return false")
	}
}

// --- Columnar (list) federation tests ---

func TestExtractIDColumn(t *testing.T) {
	// Build columnar: [count=3] [col:id(1)] [1,2,3] [col:name(2)] ["a","b","c"] [0x00]
	w := &codec.ColumnarWriter{}
	w.SetCount(3)
	w.WriteColumnInt(1, []int64{10, 20, 30})
	w.WriteColumnString(2, []string{"a", "b", "c"})
	data := w.Bytes()

	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name: "User",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "name", Type: schema.FieldString},
		},
	})

	ids := ExtractIDColumn(data, 1, s.Models["User"])
	if len(ids) != 3 {
		t.Fatalf("expected 3 ids, got %d", len(ids))
	}
	if ids[0] != 10 || ids[1] != 20 || ids[2] != 30 {
		t.Fatalf("ids = %v, want [10, 20, 30]", ids)
	}
}

func TestExtractIDColumn_NotFirstColumn(t *testing.T) {
	// ID is second column — should still find it
	w := &codec.ColumnarWriter{}
	w.SetCount(2)
	w.WriteColumnString(2, []string{"x", "y"}) // name first
	w.WriteColumnInt(1, []int64{100, 200})     // id second
	data := w.Bytes()

	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name: "User",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "name", Type: schema.FieldString},
		},
	})

	ids := ExtractIDColumn(data, 1, s.Models["User"])
	if len(ids) != 2 {
		t.Fatalf("expected 2 ids, got %d", len(ids))
	}
	if ids[0] != 100 || ids[1] != 200 {
		t.Fatalf("ids = %v, want [100, 200]", ids)
	}
}

func TestMergeColumnar(t *testing.T) {
	w := &codec.ColumnarWriter{}
	w.SetCount(2)
	w.WriteColumnInt(1, []int64{1, 2})
	w.WriteColumnString(2, []string{"a", "b"})
	primary := w.Bytes()

	extends := []ExtendColumnResult{
		{
			FieldID: 10,
			Blobs:   [][]byte{{0xAA, 0xBB}, {0xCC}},
		},
	}

	result := MergeColumnar(primary, extends)

	// Read back: should have 3 columns now
	r := codec.NewColumnarReader(result)
	if r.Count() != 2 {
		t.Fatalf("count = %d, want 2", r.Count())
	}

	// Col 1: ids
	if !r.NextColumn() || r.FieldID() != 1 {
		t.Fatal("expected id column")
	}
	ids := r.ReadColumnInt()
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("ids = %v", ids)
	}

	// Col 2: names
	if !r.NextColumn() || r.FieldID() != 2 {
		t.Fatal("expected name column")
	}
	names := r.ReadColumnString()
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("names = %v", names)
	}

	// Col 3: extend blobs
	if !r.NextColumn() || r.FieldID() != 10 {
		t.Fatalf("expected extend column, got fieldID %d", r.FieldID())
	}
	blobs := r.ReadColumnBytes()
	if len(blobs) != 2 {
		t.Fatalf("blobs = %d, want 2", len(blobs))
	}
	if blobs[0][0] != 0xAA || blobs[0][1] != 0xBB {
		t.Fatalf("blob[0] = %x", blobs[0])
	}
}

func TestExtractID_NonFirstField(t *testing.T) {
	// Build: [arenaHeader=0] [field2="Alice"] [field1(id)=99] [0x00]
	// id is NOT the first field — tests schema-aware skipping
	var buf []byte
	buf = codec.AppendVarint(buf, 0) // arena
	buf = codec.AppendVarint(buf, 2) // field 2 (string)
	buf = codec.AppendString(buf, "Alice")
	buf = codec.AppendVarint(buf, 1) // field 1 (id)
	buf = codec.AppendSvarint(buf, 99)
	buf = append(buf, 0x00)

	fieldTypes := map[int]codec.FieldSkipType{
		2: codec.SkipBytes, // string
	}

	id, ok := ExtractID(buf, 1, fieldTypes)
	if !ok {
		t.Fatal("expected ok")
	}
	if id != 99 {
		t.Fatalf("id = %d, want 99", id)
	}
}

func TestExtractID_NullableField(t *testing.T) {
	// [arenaHeader=0] [field2=nullable float (null)] [field1=42] [0x00]
	var buf []byte
	buf = codec.AppendVarint(buf, 0) // arena
	buf = codec.AppendVarint(buf, 2) // field 2 (nullable float)
	buf = codec.AppendNull(buf)      // null
	buf = codec.AppendVarint(buf, 1) // field 1 (id)
	buf = codec.AppendSvarint(buf, 42)
	buf = append(buf, 0x00)

	fieldTypes := map[int]codec.FieldSkipType{
		2: codec.SkipNullFixed64,
	}
	id, ok := ExtractID(buf, 1, fieldTypes)
	if !ok {
		t.Fatal("expected ok")
	}
	if id != 42 {
		t.Fatalf("id = %d, want 42", id)
	}
}

// --- ParseGroupedResponse tests ---

func TestParseGroupedResponse_List(t *testing.T) {
	// Build grouped response: 2 keys, key0 has 2 items, key1 has 1 item
	var resp []byte
	resp = codec.AppendVarint(resp, 2) // key_count

	// Key 0: 2 items
	resp = codec.AppendVarint(resp, 2) // item_count
	item1 := []byte{0xAA, 0xBB}
	resp = codec.AppendBytes(resp, item1) // len-prefixed item 1
	item2 := []byte{0xCC}
	resp = codec.AppendBytes(resp, item2) // len-prefixed item 2

	// Key 1: 1 item
	resp = codec.AppendVarint(resp, 1)
	item3 := []byte{0xDD, 0xEE, 0xFF}
	resp = codec.AppendBytes(resp, item3)

	blobs := ParseGroupedResponse(resp, true)
	if len(blobs) != 2 {
		t.Fatalf("expected 2 blobs, got %d", len(blobs))
	}

	// Blob 0: [count svarint=2][item1 raw][item2 raw]
	dec := codec.NewDecoder(blobs[0])
	count := dec.ReadInt() // svarint
	if count != 2 {
		t.Fatalf("blob0 count = %d, want 2", count)
	}
	// Blob 1: [count svarint=1][item3 raw]
	dec = codec.NewDecoder(blobs[1])
	count = dec.ReadInt()
	if count != 1 {
		t.Fatalf("blob1 count = %d, want 1", count)
	}
}

func TestParseGroupedResponse_Single(t *testing.T) {
	var resp []byte
	resp = codec.AppendVarint(resp, 2) // 2 keys

	// Key 0: 1 item
	resp = codec.AppendVarint(resp, 1)
	resp = codec.AppendBytes(resp, []byte{0x01, 0x02})

	// Key 1: 0 items
	resp = codec.AppendVarint(resp, 0)

	blobs := ParseGroupedResponse(resp, false)
	if len(blobs) != 2 {
		t.Fatalf("expected 2 blobs, got %d", len(blobs))
	}
	if len(blobs[0]) != 2 || blobs[0][0] != 0x01 {
		t.Fatalf("blob0 = %x, want [01 02]", blobs[0])
	}
	if blobs[1] != nil {
		t.Fatalf("blob1 should be nil, got %x", blobs[1])
	}
}

func TestParseGroupedResponse_Empty(t *testing.T) {
	blobs := ParseGroupedResponse(nil, true)
	if blobs != nil {
		t.Fatal("nil input should return nil")
	}
}

func TestParseGroupedResponse_ZeroKeys(t *testing.T) {
	resp := codec.AppendVarint(nil, 0) // 0 keys
	blobs := ParseGroupedResponse(resp, true)
	if len(blobs) != 0 {
		t.Fatalf("expected 0 blobs, got %d", len(blobs))
	}
}

func TestExtractID_SkipAllTypes(t *testing.T) {
	// Build a response with multiple field types before id
	var buf []byte
	buf = codec.AppendVarint(buf, 0) // arena
	// field 2: float
	buf = codec.AppendVarint(buf, 2)
	buf = codec.AppendFixed64(buf, 3.14)
	// field 3: nullable string (present)
	buf = codec.AppendVarint(buf, 3)
	buf = codec.AppendPresent(buf)
	buf = codec.AppendString(buf, "hello")
	// field 4: nullable varint (null)
	buf = codec.AppendVarint(buf, 4)
	buf = codec.AppendNull(buf)
	// field 5: bytes
	buf = codec.AppendVarint(buf, 5)
	buf = codec.AppendBytes(buf, []byte{0x01, 0x02})
	// field 1: id = 77
	buf = codec.AppendVarint(buf, 1)
	buf = codec.AppendSvarint(buf, 77)
	buf = append(buf, 0x00)

	fieldTypes := map[int]codec.FieldSkipType{
		2: codec.SkipFixed64,
		3: codec.SkipNullBytes,
		4: codec.SkipNullVarint,
		5: codec.SkipBytes,
	}
	id, ok := ExtractID(buf, 1, fieldTypes)
	if !ok {
		t.Fatal("expected ok")
	}
	if id != 77 {
		t.Fatalf("id = %d, want 77", id)
	}
}

func TestExtractIDColumn_AllColumnTypes(t *testing.T) {
	// Columnar with float, bool, nullable string cols before id
	var buf []byte
	buf = codec.AppendVarint(buf, 2) // count = 2
	buf = codec.AppendVarint(buf, 0) // totalStringLen = 0 (no arena)
	// col 2: float
	buf = codec.AppendVarint(buf, 2)
	buf = codec.AppendFixed64(buf, 1.0)
	buf = codec.AppendFixed64(buf, 2.0)
	// col 3: bool
	buf = codec.AppendVarint(buf, 3)
	buf = codec.AppendBool(buf, true)
	buf = codec.AppendBool(buf, false)
	// col 4: nullable string
	buf = codec.AppendVarint(buf, 4)
	buf = codec.AppendPresent(buf)
	buf = codec.AppendString(buf, "a")
	buf = codec.AppendNull(buf)
	// col 1: id
	buf = codec.AppendVarint(buf, 1)
	buf = codec.AppendSvarint(buf, 100)
	buf = codec.AppendSvarint(buf, 200)
	buf = append(buf, 0x00)

	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name: "M",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "score", Type: schema.FieldFloat},
			{ID: 3, Name: "active", Type: schema.FieldBool},
			{ID: 4, Name: "bio", Type: schema.FieldString, Nullable: true},
		},
	})

	ids := ExtractIDColumn(buf, 1, s.Models["M"])
	if len(ids) != 2 || ids[0] != 100 || ids[1] != 200 {
		t.Fatalf("ids = %v, want [100 200]", ids)
	}
}

func TestExtractIDColumn_Empty(t *testing.T) {
	ids := ExtractIDColumn(nil, 1, nil)
	if ids != nil {
		t.Fatal("nil data should return nil")
	}
}

func TestExtractIDColumn_ZeroCount(t *testing.T) {
	buf := codec.AppendVarint(nil, 0) // count=0
	buf = append(buf, 0x00)
	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name:   "M",
		Fields: []schema.Field{{ID: 1, Name: "id", Type: schema.FieldInt}},
	})
	ids := ExtractIDColumn(buf, 1, s.Models["M"])
	if ids != nil {
		t.Fatal("zero count should return nil")
	}
}

func TestExtractIDColumn_SkipError(t *testing.T) {
	// Columnar with truncated float column → skipColumnarColumn errors
	var buf []byte
	buf = codec.AppendVarint(buf, 1) // count=1
	buf = codec.AppendVarint(buf, 0) // totalStringLen = 0 (no arena)
	buf = codec.AppendVarint(buf, 2) // fieldID=2 (float)
	buf = append(buf, 0x01, 0x02)    // only 2 bytes, float needs 8

	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name: "M",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "score", Type: schema.FieldFloat},
		},
	})
	ids := ExtractIDColumn(buf, 1, s.Models["M"])
	if ids != nil {
		t.Fatal("skip error should return nil")
	}
}

func TestSkipColumnarColumn_DurationAndDateTimeNullable(t *testing.T) {
	// Duration and DateTime nullable columns
	var buf []byte
	buf = codec.AppendVarint(buf, 1) // count=1
	buf = codec.AppendVarint(buf, 0) // totalStringLen = 0 (no arena)
	// col 2: nullable DateTime
	buf = codec.AppendVarint(buf, 2)
	buf = codec.AppendNull(buf)
	// col 3: nullable Duration
	buf = codec.AppendVarint(buf, 3)
	buf = codec.AppendPresent(buf)
	buf = codec.AppendSvarint(buf, 5000)
	// col 1: id
	buf = codec.AppendVarint(buf, 1)
	buf = codec.AppendSvarint(buf, 99)
	buf = append(buf, 0x00)

	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name: "M",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "at", Type: schema.FieldDateTime, Nullable: true},
			{ID: 3, Name: "dur", Type: schema.FieldDuration, Nullable: true},
		},
	})
	ids := ExtractIDColumn(buf, 1, s.Models["M"])
	if len(ids) != 1 || ids[0] != 99 {
		t.Fatalf("ids = %v, want [99]", ids)
	}
}

func TestExtractIDColumn_UnknownColumn(t *testing.T) {
	w := &codec.ColumnarWriter{}
	w.SetCount(1)
	w.WriteColumnInt(99, []int64{1}) // unknown field
	data := w.Bytes()

	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name:   "M",
		Fields: []schema.Field{{ID: 1, Name: "id", Type: schema.FieldInt}},
	})
	ids := ExtractIDColumn(data, 1, s.Models["M"])
	if ids != nil {
		t.Fatal("unknown column should return nil")
	}
}

func TestSkipColumnarColumn_AllTypes(t *testing.T) {
	// Build columnar with every type, id at the end
	var buf []byte
	buf = codec.AppendVarint(buf, 1) // count = 1
	buf = codec.AppendVarint(buf, 0) // totalStringLen = 0 (no arena)
	// col 2: string
	buf = codec.AppendVarint(buf, 2)
	buf = codec.AppendString(buf, "test")
	// col 3: float
	buf = codec.AppendVarint(buf, 3)
	buf = codec.AppendFixed64(buf, 1.5)
	// col 4: bool
	buf = codec.AppendVarint(buf, 4)
	buf = codec.AppendBool(buf, true)
	// col 5: nullable int (null)
	buf = codec.AppendVarint(buf, 5)
	buf = codec.AppendNull(buf)
	// col 6: nullable float (present)
	buf = codec.AppendVarint(buf, 6)
	buf = codec.AppendPresent(buf)
	buf = codec.AppendFixed64(buf, 2.5)
	// col 7: nullable bool (null)
	buf = codec.AppendVarint(buf, 7)
	buf = codec.AppendNull(buf)
	// col 8: bytes (blob)
	buf = codec.AppendVarint(buf, 8)
	buf = codec.AppendBytes(buf, []byte{0xFF})
	// col 1: id
	buf = codec.AppendVarint(buf, 1)
	buf = codec.AppendSvarint(buf, 42)
	buf = append(buf, 0x00)

	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name: "M",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "name", Type: schema.FieldString},
			{ID: 3, Name: "score", Type: schema.FieldFloat},
			{ID: 4, Name: "active", Type: schema.FieldBool},
			{ID: 5, Name: "age", Type: schema.FieldInt, Nullable: true},
			{ID: 6, Name: "rating", Type: schema.FieldFloat, Nullable: true},
			{ID: 7, Name: "verified", Type: schema.FieldBool, Nullable: true},
			{ID: 8, Name: "data", Type: schema.FieldModel},
		},
	})

	ids := ExtractIDColumn(buf, 1, s.Models["M"])
	if len(ids) != 1 || ids[0] != 42 {
		t.Fatalf("ids = %v, want [42]", ids)
	}
}

func TestSchemaFieldToSkipType_AllTypes(t *testing.T) {
	tests := []struct {
		field schema.Field
		want  codec.FieldSkipType
	}{
		{schema.Field{Type: schema.FieldInt}, codec.SkipVarint},
		{schema.Field{Type: schema.FieldDateTime}, codec.SkipVarint},
		{schema.Field{Type: schema.FieldDuration}, codec.SkipVarint},
		{schema.Field{Type: schema.FieldBool}, codec.SkipVarint},
		{schema.Field{Type: schema.FieldFloat}, codec.SkipFixed64},
		{schema.Field{Type: schema.FieldString}, codec.SkipBytes},
		{schema.Field{Type: schema.FieldEnum}, codec.SkipBytes},
		{schema.Field{Type: schema.FieldBytes}, codec.SkipBytes},
		{schema.Field{Type: schema.FieldModel}, codec.SkipBytes},
		// Nullable
		{schema.Field{Type: schema.FieldInt, Nullable: true}, codec.SkipNullVarint},
		{schema.Field{Type: schema.FieldFloat, Nullable: true}, codec.SkipNullFixed64},
		{schema.Field{Type: schema.FieldString, Nullable: true}, codec.SkipNullBytes},
		{schema.Field{Type: schema.FieldBool, Nullable: true}, codec.SkipNullVarint},
	}
	for _, tt := range tests {
		got := schemaFieldToSkipType(&tt.field)
		if got != tt.want {
			t.Errorf("schemaFieldToSkipType(%v) = %d, want %d", tt.field, got, tt.want)
		}
	}
}

func TestMerge_PrimaryNoEndMarker(t *testing.T) {
	// Primary without trailing 0x00 — should still merge
	primary := []byte{0x01, 0x02}
	extends := []ExtendResult{{FieldID: 10, Data: []byte{0xAA}}}
	result := Merge(primary, extends)
	// Should append extend + end marker
	if result[len(result)-1] != 0x00 {
		t.Fatal("merged should end with 0x00")
	}
}

func TestMergeColumnar_NoExtends(t *testing.T) {
	primary := []byte{0x01, 0x02, 0x00}
	result := MergeColumnar(primary, nil)
	if string(result) != string(primary) {
		t.Fatal("no extends should return primary unchanged")
	}
}

func TestMergeColumnar_EmptyBlobs(t *testing.T) {
	primary := []byte{0x01, 0x02, 0x00}
	extends := []ExtendColumnResult{{FieldID: 10, Blobs: nil}}
	result := MergeColumnar(primary, extends)
	// Empty blobs should be skipped
	if result[len(result)-1] != 0x00 {
		t.Fatal("result should end with 0x00")
	}
}

func TestExtractIDColumn_WithBlobColumn(t *testing.T) {
	// Columnar with a FieldModel (blob) column before id
	var buf []byte
	buf = codec.AppendVarint(buf, 1)           // count=1
	buf = codec.AppendVarint(buf, 0)           // totalStringLen = 0 (no arena)
	buf = codec.AppendVarint(buf, 5)           // fieldID=5 (blob)
	buf = codec.AppendBytes(buf, []byte{0xFF}) // one blob
	buf = codec.AppendVarint(buf, 1)           // fieldID=1 (id)
	buf = codec.AppendSvarint(buf, 77)
	buf = append(buf, 0x00)

	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name: "M",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 5, Name: "data", Type: schema.FieldModel},
		},
	})
	ids := ExtractIDColumn(buf, 1, s.Models["M"])
	if len(ids) != 1 || ids[0] != 77 {
		t.Fatalf("ids = %v, want [77]", ids)
	}
}

func TestExtractIDColumn_NullableEnum(t *testing.T) {
	// Nullable string (enum) column before id
	var buf []byte
	buf = codec.AppendVarint(buf, 1) // count=1
	buf = codec.AppendVarint(buf, 0) // totalStringLen = 0 (no arena)
	buf = codec.AppendVarint(buf, 3) // fieldID=3 (nullable enum)
	buf = codec.AppendPresent(buf)
	buf = codec.AppendString(buf, "ADMIN")
	buf = codec.AppendVarint(buf, 1) // fieldID=1 (id)
	buf = codec.AppendSvarint(buf, 55)
	buf = append(buf, 0x00)

	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name: "M",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 3, Name: "role", Type: schema.FieldEnum, Nullable: true},
		},
	})
	ids := ExtractIDColumn(buf, 1, s.Models["M"])
	if len(ids) != 1 || ids[0] != 55 {
		t.Fatalf("ids = %v, want [55]", ids)
	}
}

func TestParseGroupedResponse_Truncated(t *testing.T) {
	// key_count=1 but no data after
	resp := codec.AppendVarint(nil, 1)
	blobs := ParseGroupedResponse(resp, true)
	// Should return partial result without panic
	if len(blobs) != 1 {
		t.Fatalf("expected 1 blob, got %d", len(blobs))
	}
}

func TestParseGroupedResponse_ListTruncatedItem(t *testing.T) {
	// key_count=1, item_count=2, but only 1 item provided
	var resp []byte
	resp = codec.AppendVarint(resp, 1) // 1 key
	resp = codec.AppendVarint(resp, 2) // 2 items
	resp = codec.AppendBytes(resp, []byte{0xAA})
	// missing second item
	blobs := ParseGroupedResponse(resp, true)
	if len(blobs) != 1 {
		t.Fatalf("expected 1 blob, got %d", len(blobs))
	}
}

func TestParseGroupedResponse_SingleTruncatedItem(t *testing.T) {
	var resp []byte
	resp = codec.AppendVarint(resp, 1) // 1 key
	resp = codec.AppendVarint(resp, 1) // 1 item
	// missing item data
	blobs := ParseGroupedResponse(resp, false)
	if len(blobs) != 1 {
		t.Fatalf("expected 1 blob, got %d", len(blobs))
	}
}

func TestExtractID_UnknownFieldType(t *testing.T) {
	// Field type not in fieldTypes map → should return false
	var buf []byte
	buf = codec.AppendVarint(buf, 0) // arena
	buf = codec.AppendVarint(buf, 2) // field 2 (unknown type)
	buf = codec.AppendSvarint(buf, 99)
	buf = append(buf, 0x00)

	fieldTypes := map[int]codec.FieldSkipType{} // empty: field 2 unknown
	_, ok := ExtractID(buf, 1, fieldTypes)
	if ok {
		t.Fatal("unknown field type should return false")
	}
}

func TestExtractID_NotFound(t *testing.T) {
	// All fields skipped, id field not present
	var buf []byte
	buf = codec.AppendVarint(buf, 0) // arena
	buf = codec.AppendVarint(buf, 2) // field 2
	buf = codec.AppendSvarint(buf, 42)
	buf = append(buf, 0x00)

	fieldTypes := map[int]codec.FieldSkipType{2: codec.SkipVarint}
	_, ok := ExtractID(buf, 99, fieldTypes) // looking for field 99 which doesn't exist
	if ok {
		t.Fatal("should return false when id field not found")
	}
}

func TestMerge_MultipleExtendsSomeEmpty(t *testing.T) {
	var primary []byte
	primary = codec.AppendVarint(primary, 0) // arena
	primary = codec.AppendVarint(primary, 1)
	primary = codec.AppendSvarint(primary, 1)
	primary = append(primary, 0x00)

	extends := []ExtendResult{
		{FieldID: 10, Data: []byte{0xAA}},
		{FieldID: 11, Data: nil},      // empty
		{FieldID: 12, Data: []byte{}}, // empty
		{FieldID: 13, Data: []byte{0xBB, 0xCC}},
	}
	result := Merge(primary, extends)

	// Should include fields 10 and 13, skip 11 and 12
	dec := codec.NewDecoder(result)
	dec.SkipArenaHeader()
	if !dec.NextField() || dec.FieldID() != 1 {
		t.Fatal("expected field 1")
	}
	dec.ReadInt()
	if !dec.NextField() || dec.FieldID() != 10 {
		t.Fatal("expected field 10")
	}
}

func TestParseGroupedResponse_SingleWithMultipleItems(t *testing.T) {
	// Single mode with >1 items per key — tests the skip-remaining-items path
	var resp []byte
	resp = codec.AppendVarint(resp, 1) // 1 key
	resp = codec.AppendVarint(resp, 3) // 3 items (only first should be used)
	resp = codec.AppendBytes(resp, []byte{0x01, 0x02})
	resp = codec.AppendBytes(resp, []byte{0x03, 0x04}) // should be skipped
	resp = codec.AppendBytes(resp, []byte{0x05})       // should be skipped

	blobs := ParseGroupedResponse(resp, false)
	if len(blobs) != 1 {
		t.Fatalf("expected 1 blob, got %d", len(blobs))
	}
	if len(blobs[0]) != 2 || blobs[0][0] != 0x01 || blobs[0][1] != 0x02 {
		t.Fatalf("blob[0] = %x, want [01 02]", blobs[0])
	}
}

func TestParseGroupedResponse_SingleSkipRemainingTruncated(t *testing.T) {
	// Single mode: 1 key, 3 items, first item present, second truncated
	var resp []byte
	resp = codec.AppendVarint(resp, 1) // 1 key
	resp = codec.AppendVarint(resp, 3) // 3 items
	resp = codec.AppendBytes(resp, []byte{0xAA})
	// remaining items truncated — no more data
	blobs := ParseGroupedResponse(resp, false)
	if len(blobs) != 1 {
		t.Fatalf("expected 1 blob, got %d", len(blobs))
	}
	if blobs[0][0] != 0xAA {
		t.Fatalf("blob[0] = %x, want [AA]", blobs[0])
	}
}

func TestParseGroupedResponse_InvalidVarint(t *testing.T) {
	// Invalid varint — should return nil
	blobs := ParseGroupedResponse([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, true)
	if blobs != nil {
		t.Fatal("invalid varint should return nil")
	}
}

func TestMergeColumnar_PrimaryNoEndMarker(t *testing.T) {
	// Primary without trailing 0x00
	primary := []byte{0x01, 0x02}
	extends := []ExtendColumnResult{
		{FieldID: 10, Blobs: [][]byte{{0xAA}}},
	}
	result := MergeColumnar(primary, extends)
	// Should append extend + end marker
	if result[len(result)-1] != 0x00 {
		t.Fatal("merged should end with 0x00")
	}
}

func TestSkipColumnarColumn_DefaultType(t *testing.T) {
	// Use a field type that falls through to default case in skipColumnarColumn
	// FieldBytes type hits the default case
	var buf []byte
	buf = codec.AppendVarint(buf, 1) // count=1
	buf = codec.AppendVarint(buf, 0) // totalStringLen = 0 (no arena)
	buf = codec.AppendVarint(buf, 2) // fieldID=2 (FieldBytes)
	buf = codec.AppendBytes(buf, []byte{0xAA, 0xBB})
	buf = codec.AppendVarint(buf, 1) // fieldID=1 (id)
	buf = codec.AppendSvarint(buf, 88)
	buf = append(buf, 0x00)

	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name: "M",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "payload", Type: schema.FieldBytes},
		},
	})
	ids := ExtractIDColumn(buf, 1, s.Models["M"])
	if len(ids) != 1 || ids[0] != 88 {
		t.Fatalf("ids = %v, want [88]", ids)
	}
}

func TestSchemaFieldToSkipType_Default(t *testing.T) {
	// Test the default case with an unknown field type
	f := &schema.Field{Type: schema.FieldType(99)} // unknown type
	got := schemaFieldToSkipType(f)
	if got != codec.SkipBytes {
		t.Errorf("default skip type = %d, want SkipBytes (%d)", got, codec.SkipBytes)
	}
}

func TestExtractID_SkipFieldError(t *testing.T) {
	// Build a buffer where skipField encounters an error (truncated string)
	// field 2 is a string but data is truncated — skipField will fail
	var buf []byte
	buf = codec.AppendVarint(buf, 0)   // arena
	buf = codec.AppendVarint(buf, 2)   // field 2 (string)
	buf = codec.AppendVarint(buf, 100) // len-prefix says 100 bytes, but no data follows

	fieldTypes := map[int]codec.FieldSkipType{
		2: codec.SkipBytes,
	}
	_, ok := ExtractID(buf, 1, fieldTypes)
	if ok {
		t.Fatal("skip error should return false")
	}
}

func TestExtractIDColumn_IDNotFound(t *testing.T) {
	// All columns are iterated but id column is not found
	w := &codec.ColumnarWriter{}
	w.SetCount(1)
	w.WriteColumnString(2, []string{"hello"})
	data := w.Bytes()

	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name: "M",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "name", Type: schema.FieldString},
		},
	})
	// Look for field 1 (id) but only field 2 exists in data
	ids := ExtractIDColumn(data, 1, s.Models["M"])
	if ids != nil {
		t.Fatal("should return nil when id column not found")
	}
}

func TestExtractID_DecoderError(t *testing.T) {
	// Build a buffer where the id field value is truncated, causing a decoder error
	// This tests the dec.Err() == nil check on line 75
	var buf []byte
	buf = codec.AppendVarint(buf, 0) // arena
	buf = codec.AppendVarint(buf, 1) // field 1 (id)
	// No value data after field ID — truncated, ReadInt will fail
	id, ok := ExtractID(buf, 1, nil)
	if ok {
		t.Fatalf("expected not ok, got id=%d", id)
	}
}

func TestExtractIDColumn_ColumnarReaderError(t *testing.T) {
	// Minimal invalid data (just some garbage bytes that fail ColumnarReader init)
	ids := ExtractIDColumn([]byte{0xFF, 0xFF}, 1, nil)
	if ids != nil {
		t.Fatal("invalid data should return nil")
	}
}

func TestSkipColumnarColumn_NonNullableIntBeforeID(t *testing.T) {
	// Non-nullable FieldInt column (not the id) before the actual id column
	// Tests the else branch of FieldInt/DateTime/Duration nullable check
	var buf []byte
	buf = codec.AppendVarint(buf, 1) // count=1
	buf = codec.AppendVarint(buf, 0) // totalStringLen = 0
	buf = codec.AppendVarint(buf, 2) // fieldID=2 (age, non-nullable int)
	buf = codec.AppendSvarint(buf, 25)
	buf = codec.AppendVarint(buf, 1) // fieldID=1 (id)
	buf = codec.AppendSvarint(buf, 42)
	buf = append(buf, 0x00)

	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name: "M",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "age", Type: schema.FieldInt},
		},
	})
	ids := ExtractIDColumn(buf, 1, s.Models["M"])
	if len(ids) != 1 || ids[0] != 42 {
		t.Fatalf("ids = %v, want [42]", ids)
	}
}

func TestSkipColumnarColumn_EnumNonNullable(t *testing.T) {
	// Enum (non-nullable) column before id — tests FieldEnum + !Nullable path
	var buf []byte
	buf = codec.AppendVarint(buf, 1) // count=1
	buf = codec.AppendVarint(buf, 0) // totalStringLen = 0
	buf = codec.AppendVarint(buf, 2) // fieldID=2 (enum)
	buf = codec.AppendString(buf, "ADMIN")
	buf = codec.AppendVarint(buf, 1) // fieldID=1 (id)
	buf = codec.AppendSvarint(buf, 77)
	buf = append(buf, 0x00)

	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name: "M",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "role", Type: schema.FieldEnum},
		},
	})
	ids := ExtractIDColumn(buf, 1, s.Models["M"])
	if len(ids) != 1 || ids[0] != 77 {
		t.Fatalf("ids = %v, want [77]", ids)
	}
}

func TestParseGroupedResponse_MultipleKeys(t *testing.T) {
	// 3 keys: key0=2 items, key1=0 items, key2=1 item
	var resp []byte
	resp = codec.AppendVarint(resp, 3) // 3 keys
	resp = codec.AppendVarint(resp, 2) // key0: 2 items
	resp = codec.AppendBytes(resp, []byte{0x01})
	resp = codec.AppendBytes(resp, []byte{0x02})
	resp = codec.AppendVarint(resp, 0) // key1: 0 items
	resp = codec.AppendVarint(resp, 1) // key2: 1 item
	resp = codec.AppendBytes(resp, []byte{0x03})

	blobs := ParseGroupedResponse(resp, true)
	if len(blobs) != 3 {
		t.Fatalf("expected 3 blobs, got %d", len(blobs))
	}
	// key0: list with 2 items
	if len(blobs[0]) == 0 {
		t.Fatal("key0 blob should not be empty")
	}
	// key1: empty list
	dec := codec.NewDecoder(blobs[1])
	count := dec.ReadInt()
	if count != 0 {
		t.Fatalf("key1 count should be 0, got %d", count)
	}
	// key2: list with 1 item
	if len(blobs[2]) == 0 {
		t.Fatal("key2 blob should not be empty")
	}
}
