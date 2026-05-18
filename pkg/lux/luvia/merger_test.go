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
