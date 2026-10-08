package luvia

import (
	"bytes"
	"encoding/binary"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/light-speak/luxo/internal/testutil"
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

func TestAppendMergedDestination(t *testing.T) {
	for _, columnar := range []bool{false, true} {
		for _, primary := range [][]byte{nil, {0, 1, 84}, {0, 1, 84, 0}} {
			for _, body := range [][]byte{nil, {4, 0, 1, 2, 0}} {
				checkAppendMergedDestination(t, columnar, primary, body)
			}
		}
	}
}

func checkAppendMergedDestination(t *testing.T, columnar bool, primary, body []byte) {
	t.Helper()
	beforePrimary, beforeBody := bytes.Clone(primary), bytes.Clone(body)
	row := []ExtendResult{{FieldID: 300, Data: body}}
	columns := []ExtendColumnResult{{FieldID: 300, Blobs: [][]byte{body}}}
	want := Merge(primary, row)
	if columnar {
		want = MergeColumnar(primary, columns)
	}
	dst := make([]byte, 2, len(want)+2)
	dst[0], dst[1] = 0xaa, 0xbb
	start := &dst[0]
	var got []byte
	if columnar {
		got = AppendMergedColumnar(dst, primary, columns)
	} else {
		got = AppendMerged(dst, primary, row)
	}
	if len(got) < 2 || got[0] != 0xaa || got[1] != 0xbb || !bytes.Equal(got[2:], want) {
		t.Fatalf("columnar=%v: append changed prefix or wire: %x, want %x", columnar, got, want)
	}
	if &got[0] != start {
		t.Fatal("sufficient destination capacity was not reused")
	}
	if !bytes.Equal(primary, beforePrimary) || !bytes.Equal(body, beforeBody) {
		t.Fatal("merge modified borrowed source buffers")
	}
}

func TestAppendMergedNoExtends(t *testing.T) {
	for _, primary := range [][]byte{nil, {0, 1, 84, 0}} {
		for _, columnar := range []bool{false, true} {
			dst := []byte{0xaa}
			want := append(bytes.Clone(dst), primary...)
			var got []byte
			if columnar {
				got = AppendMergedColumnar(dst, primary, nil)
			} else {
				got = AppendMerged(dst, primary, nil)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("columnar=%v: got %x, want %x", columnar, got, want)
			}
		}
	}
}

func TestAppendMergedOwnedBufferAllocations(t *testing.T) {
	primary := []byte{0, 1, 84, 0}
	child := []byte{4, 0, 1, 2, 0}
	row := []ExtendResult{{FieldID: 300, Data: child}}
	columns := []ExtendColumnResult{{FieldID: 300, Blobs: [][]byte{child}}}
	storage := make([]byte, 0, 64)
	for _, columnar := range []bool{false, true} {
		var result []byte
		allocations := testing.AllocsPerRun(100, func() {
			if columnar {
				result = AppendMergedColumnar(storage[:0], primary, columns)
			} else {
				result = AppendMerged(storage[:0], primary, row)
			}
		})
		if allocations != 0 || len(result) == 0 {
			t.Fatalf("columnar=%v: %g allocations with reusable output", columnar, allocations)
		}
		owned := bytes.Clone(result)
		primary[2]++
		child[3]++
		if !bytes.Equal(result, owned) {
			t.Fatal("output retained borrowed message storage")
		}
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
	postsData = codec.AppendArrayHeader(postsData, 2) // count = 2
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
	count := dec.ReadArrayLength()
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
	buf = codec.AppendBytes(buf, []byte("Alice"))
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

func TestExtractPrimaryKeysPreservesStringWireEncoding(t *testing.T) {
	var enc codec.Encoder
	enc.WriteFieldString(7, "sku-42")
	enc.WriteEnd()
	primary := append(codec.AppendVarint(nil, 6), enc.Bytes()...)
	field := &schema.Field{ID: 7, Name: "sku", Type: schema.FieldString, PrimaryKey: true}

	model := &schema.Model{Name: "Product", Fields: []schema.Field{*field}}
	s := schema.New()
	s.RegisterModel(model)
	keys, ok := ExtractPrimaryKeys(primary, field, model, s)
	if !ok || keys.Len() != 1 {
		t.Fatalf("ExtractPrimaryKeys() = %#v, %v", keys, ok)
	}
	params := keys.EncodeParam(1)
	dec := codec.NewDecoder(params)
	if !dec.NextField() || dec.FieldID() != 1 {
		t.Fatal("missing encoded keys param")
	}
	got := dec.ReadStringArray()
	if dec.Err() != nil || len(got) != 1 || got[0] != "sku-42" {
		t.Fatalf("encoded keys = %v, err %v", got, dec.Err())
	}
}

func TestExtractPrimaryKeysValidatesDateTimePairs(t *testing.T) {
	for _, nullable := range []bool{false, true} {
		registry := schema.New()
		model := &schema.Model{Name: "Scheduled", Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt, PrimaryKey: true},
			{ID: 2, Name: "at", Type: schema.FieldDateTime, Nullable: nullable},
		}}
		registry.RegisterModel(model)
		for _, keyFirst := range []bool{false, true} {
			valid := testutil.Bytes(t)(codec.AppendDateTime(nil, time.Unix(-1, 999999999).UTC()))
			for index, date := range [][]byte{valid, {0, 128}, codec.AppendVarint([]byte{0}, 1_000_000_000)} {
				field := []byte{2}
				if nullable {
					field = append(field, 1)
				}
				field = append(field, date...)
				row := []byte{0}
				if keyFirst {
					row = append(row, 1, 84)
				}
				row = append(row, field...)
				if !keyFirst {
					row = append(row, 1, 84)
				}
				row = append(row, 0)
				keys, ok := ExtractPrimaryKeys(row, model.PrimaryKeyField(), model, registry)
				if ok != (index == 0) || (ok && keys.Len() != 1) {
					t.Fatalf("row nullable=%v keyFirst=%v date=%x: valid=%v", nullable, keyFirst, date, ok)
				}
				keys, ok = ExtractPrimaryKeyColumn(append([]byte{1}, row...), model.PrimaryKeyField(), model)
				if ok != (index == 0) || (ok && keys.Len() != 1) {
					t.Fatalf("column nullable=%v keyFirst=%v date=%x: valid=%v", nullable, keyFirst, date, ok)
				}
			}
		}
	}
}

func TestExtractPrimaryKeysSkipsListsAndNestedModels(t *testing.T) {
	child := &schema.Model{Name: "Child", Fields: []schema.Field{{ID: 1, Name: "name", Type: schema.FieldString}}}
	parent := &schema.Model{Name: "Parent", Fields: []schema.Field{
		{ID: 2, Name: "tags", Type: schema.FieldString, IsList: true},
		{ID: 3, Name: "child", Type: schema.FieldModel, TypeName: "Child", Relation: true},
		{ID: 7, Name: "key", Type: schema.FieldString, PrimaryKey: true},
	}}
	s := schema.New()
	s.RegisterModel(child)
	s.RegisterModel(parent)
	var primary []byte
	primary = codec.AppendVarint(primary, 0)
	primary = codec.AppendVarint(primary, 2)
	primary = codec.AppendVarint(primary, 2)
	primary = codec.AppendBytes(primary, []byte("a"))
	primary = codec.AppendBytes(primary, []byte("b"))
	primary = codec.AppendVarint(primary, 3)
	childRow := codec.AppendBytes([]byte{1, 1}, []byte("x"))
	primary = codec.AppendBytes(primary, append(childRow, 0))
	primary = codec.AppendVarint(primary, 7)
	primary = codec.AppendBytes(primary, []byte("parent-key"))
	primary = append(primary, 0)

	keys, ok := ExtractPrimaryKeys(primary, parent.PrimaryKeyField(), parent, s)
	if !ok {
		t.Fatal("primary key after complex fields was not extracted")
	}
	dec := codec.NewDecoder(keys.EncodeParam(1))
	dec.NextField()
	got := dec.ReadStringArray()
	if dec.Err() != nil || !reflect.DeepEqual(got, []string{"parent-key"}) {
		t.Fatalf("keys = %v, err %v", got, dec.Err())
	}
}

func TestExtractPrimaryKeyColumnPreservesUUIDWireEncoding(t *testing.T) {
	uuidA := [16]byte{1, 2, 3}
	uuidB := [16]byte{4, 5, 6}
	var writer codec.ColumnarWriter
	writer.SetCount(2)
	writer.WriteColumnUUID(7, [][16]byte{uuidA, uuidB})
	model := &schema.Model{Name: "Account", Fields: []schema.Field{{
		ID: 7, Name: "key", Type: schema.FieldUUID, PrimaryKey: true,
	}}}
	s := schema.New()
	s.RegisterModel(model)

	keys, ok := ExtractPrimaryKeyColumn(testutil.Bytes(t)(writer.Bytes()), model.FieldByID(7), model)
	if !ok || keys.Len() != 2 {
		t.Fatalf("ExtractPrimaryKeyColumn() = %#v, %v", keys, ok)
	}
	params := keys.EncodeParam(1)
	dec := codec.NewDecoder(params)
	if !dec.NextField() {
		t.Fatal("missing encoded keys param")
	}
	got := dec.ReadUUIDArray()
	if dec.Err() != nil || !reflect.DeepEqual(got, [][16]byte{uuidA, uuidB}) {
		t.Fatalf("encoded UUID keys = %v, err %v", got, dec.Err())
	}
}

func TestExtractPrimaryKeyColumnSkipsScalarListColumn(t *testing.T) {
	var listCell []byte
	listCell = codec.AppendVarint(listCell, 2)
	listCell = codec.AppendBytes(listCell, []byte("a"))
	listCell = codec.AppendBytes(listCell, []byte("b"))
	var writer codec.ColumnarWriter
	writer.SetCount(1)
	writer.WriteColumnBytes(2, [][]byte{listCell})
	writer.WriteColumnString(7, []string{"key"})
	model := &schema.Model{Name: "Product", Fields: []schema.Field{
		{ID: 2, Name: "tags", Type: schema.FieldString, IsList: true},
		{ID: 7, Name: "sku", Type: schema.FieldString, PrimaryKey: true},
	}}
	s := schema.New()
	s.RegisterModel(model)

	keys, ok := ExtractPrimaryKeyColumn(testutil.Bytes(t)(writer.Bytes()), model.PrimaryKeyField(), model)
	if !ok || keys.Len() != 1 {
		t.Fatalf("keys = %#v, ok %v", keys, ok)
	}
}

// --- Columnar (list) federation tests ---

func TestExtractIDColumn(t *testing.T) {
	// Build columnar: [count=3] [col:id(1)] [1,2,3] [col:name(2)] ["a","b","c"] [0x00]
	w := &codec.ColumnarWriter{}
	w.SetCount(3)
	w.WriteColumnInt(1, []int64{10, 20, 30})
	w.WriteColumnString(2, []string{"a", "b", "c"})
	data := testutil.Bytes(t)(w.Bytes())

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
	data := testutil.Bytes(t)(w.Bytes())

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
	primary := testutil.Bytes(t)(w.Bytes())

	extends := []ExtendColumnResult{
		{
			FieldID: 10,
			Blobs:   [][]byte{codec.AppendBytes(nil, []byte{0xAA, 0xBB}), codec.AppendBytes(nil, []byte{0xCC})},
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
	buf = codec.AppendBytes(buf, []byte("Alice"))
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
	resp := append([]byte{2}, resolvedValueFixture(t, 2, false)...)
	resp = append(resp, resolvedValueFixture(t, 1, false)...)
	assertResolvedListCounts(t, ParseGroupedResponse(resp, false), []int{2, 1})
}

func assertResolvedListCounts(t *testing.T, values [][]byte, counts []int) {
	t.Helper()
	if len(values) != len(counts) {
		t.Fatalf("groups = %d, want %d", len(values), len(counts))
	}
	for i, value := range values {
		body, n := codec.ReadBytes(value, 0)
		if n != len(value) {
			t.Fatalf("invalid field value %x", value)
		}
		reader := codec.NewColumnarReader(body)
		if reader.Err() != nil || reader.Count() != counts[i] {
			t.Fatalf("group %d: count %d, error %v", i, reader.Count(), reader.Err())
		}
	}
}

func TestParseGroupedResponse_Single(t *testing.T) {
	value := codec.AppendBytes([]byte{1}, []byte{0, 1, 2, 0})
	resp := append([]byte{2}, value...)
	resp = append(resp, 0)
	blobs := ParseGroupedResponse(resp, true)
	if len(blobs) != 2 || !bytes.Equal(blobs[0], value) || !bytes.Equal(blobs[1], []byte{0}) {
		t.Fatalf("nullable groups = %x", blobs)
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
	buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(3.14))
	// field 3: nullable string (present)
	buf = codec.AppendVarint(buf, 3)
	buf = codec.AppendPresent(buf)
	buf = codec.AppendBytes(buf, []byte("hello"))
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
	buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(1.0))
	buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(2.0))
	// col 3: bool
	buf = codec.AppendVarint(buf, 3)
	buf = codec.AppendBool(buf, true)
	buf = codec.AppendBool(buf, false)
	// col 4: nullable string
	buf = codec.AppendVarint(buf, 4)
	buf = codec.AppendPresent(buf)
	buf = codec.AppendBytes(buf, []byte("a"))
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
	data := testutil.Bytes(t)(w.Bytes())

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
	buf = codec.AppendBytes(buf, []byte("test"))
	// col 3: float
	buf = codec.AppendVarint(buf, 3)
	buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(1.5))
	// col 4: bool
	buf = codec.AppendVarint(buf, 4)
	buf = codec.AppendBool(buf, true)
	// col 5: nullable int (null)
	buf = codec.AppendVarint(buf, 5)
	buf = codec.AppendNull(buf)
	// col 6: nullable float (present)
	buf = codec.AppendVarint(buf, 6)
	buf = codec.AppendPresent(buf)
	buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(2.5))
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
		{schema.Field{Type: schema.FieldDateTime}, codec.SkipDateTime},
		{schema.Field{Type: schema.FieldDuration}, codec.SkipVarint},
		{schema.Field{Type: schema.FieldBool}, codec.SkipVarint},
		{schema.Field{Type: schema.FieldFloat}, codec.SkipFixed64},
		{schema.Field{Type: schema.FieldUUID}, codec.SkipFixed16},
		{schema.Field{Type: schema.FieldString}, codec.SkipString},
		{schema.Field{Type: schema.FieldEnum}, codec.SkipString},
		{schema.Field{Type: schema.FieldDecimal}, codec.SkipString},
		{schema.Field{Type: schema.FieldBytes}, codec.SkipBytes},
		{schema.Field{Type: schema.FieldModel}, codec.SkipBytes},
		// Nullable
		{schema.Field{Type: schema.FieldInt, Nullable: true}, codec.SkipNullVarint},
		{schema.Field{Type: schema.FieldFloat, Nullable: true}, codec.SkipNullFixed64},
		{schema.Field{Type: schema.FieldUUID, Nullable: true}, codec.SkipNullFixed16},
		{schema.Field{Type: schema.FieldString, Nullable: true}, codec.SkipNullString},
		{schema.Field{Type: schema.FieldDecimal, Nullable: true}, codec.SkipNullString},
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
	buf = codec.AppendBytes(buf, []byte("ADMIN"))
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
	// Truncation must not return the previously accepted partial result.
	if blobs != nil {
		t.Fatalf("accepted truncated response: %x", blobs)
	}
}

func TestParseGroupedResponse_ListTruncatedItem(t *testing.T) {
	resp := resolvedResponseFixture(t, 1, 2, false)
	if blobs := ParseGroupedResponse(resp[:len(resp)-1], false); blobs != nil {
		t.Fatalf("accepted truncated columnar body: %x", blobs)
	}
}

func TestParseGroupedResponse_SingleTruncatedItem(t *testing.T) {
	// One present nullable value, but its length and body are missing.
	if blobs := ParseGroupedResponse([]byte{1, 1}, true); blobs != nil {
		t.Fatalf("accepted truncated nullable object: %x", blobs)
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

func TestParseGroupedResponse_TrailingGroupValues(t *testing.T) {
	// The declared key count must consume exactly one canonical field value.
	resp := codec.AppendBytes([]byte{1}, []byte{0, 0})
	resp = codec.AppendBytes(resp, []byte{0, 0})
	if blobs := ParseGroupedResponse(resp, false); blobs != nil {
		t.Fatalf("accepted trailing group: %x", blobs)
	}
}

func TestParseGroupedResponse_LaterGroupTruncated(t *testing.T) {
	resp := codec.AppendBytes([]byte{2}, []byte{0, 0})
	resp = append(resp, 2, 0) // The second group declares two bytes, but has one.
	if blobs := ParseGroupedResponse(resp, false); blobs != nil {
		t.Fatalf("accepted partial grouped response: %x", blobs)
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
	data := testutil.Bytes(t)(w.Bytes())

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
	buf = codec.AppendBytes(buf, []byte("ADMIN"))
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
	resp := []byte{3}
	for _, count := range []int{2, 0, 1} {
		resp = append(resp, resolvedValueFixture(t, count, false)...)
	}
	assertResolvedListCounts(t, ParseGroupedResponse(resp, false), []int{2, 0, 1})
}

func TestSkipNestedRowFieldHandlesSchemaAndWireFailures(t *testing.T) {
	registry := schema.New()
	missing := &schema.Field{Type: schema.FieldModel, TypeName: "Missing", Relation: true}
	if skipNestedRowField(codec.NewDecoder(nil), missing, registry, 1) {
		t.Fatal("missing nested schema was accepted")
	}

	registry.RegisterType(&schema.TypeDecl{Name: "Child", Fields: []schema.Field{{ID: 1, Name: "value", Type: schema.FieldInt}}})
	nullable := &schema.Field{Type: schema.FieldModel, TypeName: "Child", Relation: true, Nullable: true}
	if !skipNestedRowField(codec.NewDecoder(codec.AppendNull(nil)), nullable, registry, 1) {
		t.Fatal("nullable nested type declaration was not skipped")
	}
	if skipNestedRowField(codec.NewDecoder(nil), nullable, registry, 1) {
		t.Fatal("truncated nullable marker was accepted")
	}

	list := &schema.Field{Type: schema.FieldModel, TypeName: "Child", Relation: true, IsList: true}
	if skipNestedRowField(codec.NewDecoder(nil), list, registry, 1) {
		t.Fatal("truncated nested list header was accepted")
	}
	row := codec.AppendArrayHeader(nil, 1)
	row = codec.AppendVarint(row, 0)
	row = codec.AppendVarint(row, 1)
	row = codec.AppendSvarint(row, 42)
	row = append(row, 0)
	if !skipNestedRowField(codec.NewDecoder(codec.AppendBytes(nil, row)), list, registry, 1) {
		t.Fatal("valid nested list was not skipped")
	}
	unknownField := codec.AppendArrayHeader(nil, 1)
	unknownField = codec.AppendVarint(unknownField, 0)
	unknownField = codec.AppendVarint(unknownField, 2)
	unknownField = codec.AppendSvarint(unknownField, 42)
	unknownField = append(unknownField, 0)
	if skipNestedRowField(codec.NewDecoder(codec.AppendBytes(nil, unknownField)), list, registry, 1) {
		t.Fatal("unknown nested row field was accepted")
	}
}

func TestExtractPrimaryKeysRejectsInvalidInputs(t *testing.T) {
	registry := schema.New()
	key := &schema.Field{ID: 1, Name: "key", Type: schema.FieldString, PrimaryKey: true}
	model := &schema.Model{Name: "Item", Fields: []schema.Field{*key}}
	registry.RegisterModel(model)
	model = registry.Models[model.Name]
	key = model.PrimaryKeyField()
	for name, test := range map[string]struct {
		primary  []byte
		keyField *schema.Field
		model    *schema.Model
		registry *schema.Schema
	}{
		"empty response":   {keyField: key, model: model, registry: registry},
		"missing key":      {primary: []byte{0}, model: model, registry: registry},
		"missing model":    {primary: []byte{0}, keyField: key, registry: registry},
		"missing registry": {primary: []byte{0}, keyField: key, model: model},
		"invalid key type": {primary: []byte{0}, keyField: &schema.Field{Type: schema.FieldFloat}, model: model, registry: registry},
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := ExtractPrimaryKeys(test.primary, test.keyField, test.model, test.registry); ok {
				t.Fatal("invalid primary-key extraction input was accepted")
			}
		})
	}

	unknownField := codec.AppendVarint(nil, 0)
	unknownField = codec.AppendVarint(unknownField, 2)
	unknownField = codec.AppendBytes(unknownField, []byte("value"))
	unknownField = append(unknownField, 0)
	if _, ok := ExtractPrimaryKeys(unknownField, key, model, registry); ok {
		t.Fatal("unknown row field was accepted")
	}
	truncatedKey := codec.AppendVarint(nil, 0)
	truncatedKey = codec.AppendVarint(truncatedKey, 1)
	truncatedKey = codec.AppendVarint(truncatedKey, 10)
	if _, ok := ExtractPrimaryKeys(truncatedKey, key, model, registry); ok {
		t.Fatal("truncated primary key was accepted")
	}
}

func TestExtractPrimaryKeyColumnRejectsInvalidInputs(t *testing.T) {
	key := schema.Field{ID: 1, Name: "key", Type: schema.FieldInt, PrimaryKey: true}
	model := &schema.Model{Name: "Item", Fields: []schema.Field{key, {ID: 2, Name: "value", Type: schema.FieldInt}}}
	registry := schema.New()
	registry.RegisterModel(model)
	model = registry.Models[model.Name]
	keyField := model.PrimaryKeyField()

	if _, ok := ExtractPrimaryKeyColumn(nil, keyField, model); ok {
		t.Fatal("empty columnar response was accepted")
	}
	if _, ok := ExtractPrimaryKeyColumn([]byte{0xff}, keyField, model); ok {
		t.Fatal("malformed columnar response was accepted")
	}
	if _, ok := ExtractPrimaryKeyColumn(codec.AppendVarint(nil, 0), keyField, model); ok {
		t.Fatal("zero-row columnar response was accepted")
	}
	if _, ok := ExtractPrimaryKeyColumn([]byte{1, 0}, &schema.Field{Type: schema.FieldFloat}, model); ok {
		t.Fatal("invalid columnar key type was accepted")
	}

	var unknown codec.ColumnarWriter
	unknown.SetCount(1)
	unknown.WriteColumnInt(3, []int64{1})
	if _, ok := ExtractPrimaryKeyColumn(testutil.Bytes(t)(unknown.Bytes()), keyField, model); ok {
		t.Fatal("unknown column was accepted")
	}
	var missing codec.ColumnarWriter
	missing.SetCount(1)
	missing.WriteColumnInt(2, []int64{1})
	if _, ok := ExtractPrimaryKeyColumn(testutil.Bytes(t)(missing.Bytes()), keyField, model); ok {
		t.Fatal("response without key column was accepted")
	}
	truncated := []byte{1, 0, 2}
	if _, ok := ExtractPrimaryKeyColumn(truncated, keyField, model); ok {
		t.Fatal("truncated column was accepted")
	}
}
