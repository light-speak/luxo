package luvia

import (
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

// --- Helper: build a User model with extend fields from "post" module ---

func integrationUserModel() (*schema.Schema, *schema.Model) {
	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name: "User",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "name", Type: schema.FieldString},
			{ID: 3, Name: "email", Type: schema.FieldString},
			// Extend: posts from "post" module, resolved via Post.userId FK
			{ID: 10, Name: "posts", Type: schema.FieldModel, TypeName: "Post",
				IsList: true, Relation: true, Module: "post", ForeignKey: "userId"},
		},
	})
	// Also register the Post model (used by columnar extraction)
	s.RegisterModel(&schema.Model{
		Name: "Post",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "title", Type: schema.FieldString},
			{ID: 3, Name: "userId", Type: schema.FieldInt},
		},
	})
	return s, s.Models["User"]
}

// encodeUser builds a binary-encoded User: [arenaHeader=0][fields...][0x00]
func encodeUser(id int64, name, email string) []byte {
	var buf []byte
	buf = codec.AppendVarint(buf, 0) // arena header
	buf = codec.AppendVarint(buf, 1) // field 1 = id
	buf = codec.AppendSvarint(buf, id)
	buf = codec.AppendVarint(buf, 2) // field 2 = name
	buf = codec.AppendString(buf, name)
	buf = codec.AppendVarint(buf, 3) // field 3 = email
	buf = codec.AppendString(buf, email)
	buf = append(buf, 0x00) // end
	return buf
}

// encodePost builds a binary-encoded Post.
func encodePost(id, userID int64, title string) []byte {
	var buf []byte
	buf = codec.AppendVarint(buf, 0) // arena header
	buf = codec.AppendVarint(buf, 1) // field 1 = id
	buf = codec.AppendSvarint(buf, id)
	buf = codec.AppendVarint(buf, 2) // field 2 = title
	buf = codec.AppendString(buf, title)
	buf = codec.AppendVarint(buf, 3) // field 3 = userId
	buf = codec.AppendSvarint(buf, userID)
	buf = append(buf, 0x00) // end
	return buf
}

// --- Test 1: Gateway Federation Planner Integration (single record) ---

// TestIntegration_PlanSplitMerge tests the full Plan → simulate RPC → Merge path
// for a single User with extend posts.
func TestIntegration_PlanSplitMerge(t *testing.T) {
	_, userModel := integrationUserModel()

	// Client requests: id, name, posts (extend field)
	mask := codec.FieldMaskSet(nil, 1)  // id
	mask = codec.FieldMaskSet(mask, 2)  // name
	mask = codec.FieldMaskSet(mask, 10) // posts (extend)

	// Step 1: Plan
	plan := Plan(userModel, mask, "user")
	if plan == nil {
		t.Fatal("expected non-nil plan for request with extend fields")
	}

	// Verify plan structure
	if plan.Primary.Module != "user" {
		t.Errorf("primary module = %q, want %q", plan.Primary.Module, "user")
	}
	if !codec.FieldMaskHas(plan.Primary.Mask, 1) {
		t.Error("primary mask missing id (field 1)")
	}
	if !codec.FieldMaskHas(plan.Primary.Mask, 2) {
		t.Error("primary mask missing name (field 2)")
	}
	if codec.FieldMaskHas(plan.Primary.Mask, 10) {
		t.Error("primary mask should not include extend field posts (10)")
	}

	if len(plan.Extends) != 1 {
		t.Fatalf("expected 1 extend step, got %d", len(plan.Extends))
	}
	ext := plan.Extends[0]
	if ext.SvcName != "svc:resolve:Post:userId" {
		t.Errorf("extend svc = %q, want %q", ext.SvcName, "svc:resolve:Post:userId")
	}
	if ext.FieldID != 10 {
		t.Errorf("extend fieldID = %d, want 10", ext.FieldID)
	}

	// Step 2: Simulate primary response (User service returns id + name)
	primary := encodeUser(42, "Alice", "")

	// Step 3: Extract ID from primary response
	id, ok := ExtractID(primary, plan.IDFieldID, plan.FieldTypes)
	if !ok {
		t.Fatal("failed to extract ID from primary response")
	}
	if id != 42 {
		t.Fatalf("extracted id = %d, want 42", id)
	}

	// Step 4: Simulate extend RPC response (post service returns 2 posts for user 42)
	// Encode as a list: [count svarint][post1 raw][post2 raw]
	post1 := encodePost(101, 42, "Hello World")
	post2 := encodePost(102, 42, "Second Post")
	var postsData []byte
	postsData = codec.AppendSvarint(postsData, 2) // count = 2
	postsData = append(postsData, post1...)
	postsData = append(postsData, post2...)

	// Step 5: Merge primary + extend
	merged := Merge(primary, []ExtendResult{
		{FieldID: ext.FieldID, Data: postsData},
	})

	// Step 6: Verify merged response
	dec := codec.NewDecoder(merged)
	dec.SkipArenaHeader()

	// Read fields in order (id, name, email if present, then posts)
	fieldsSeen := map[int]bool{}
	var mergedID int64
	var mergedName string
	var postsCount int64

	for dec.NextField() {
		fid := dec.FieldID()
		fieldsSeen[fid] = true
		switch fid {
		case 1:
			mergedID = dec.ReadInt()
		case 2:
			mergedName = dec.ReadString()
		case 3:
			_ = dec.ReadString() // email (may be empty)
		case 10:
			postsCount = dec.ReadInt() // svarint count
		default:
			t.Fatalf("unexpected field ID %d in merged response", fid)
		}
	}
	if dec.Err() != nil {
		t.Fatalf("decoder error: %v", dec.Err())
	}

	if mergedID != 42 {
		t.Errorf("merged id = %d, want 42", mergedID)
	}
	if mergedName != "Alice" {
		t.Errorf("merged name = %q, want %q", mergedName, "Alice")
	}
	if !fieldsSeen[10] {
		t.Error("merged response missing posts field (10)")
	}
	if postsCount != 2 {
		t.Errorf("posts count = %d, want 2", postsCount)
	}
}

// --- Test 2: Columnar Federation Integration (list response) ---

// buildColumnarPrimary builds a columnar response with 3 users.
func buildColumnarPrimary() []byte {
	cw := &codec.ColumnarWriter{}
	cw.SetCount(3)
	cw.WriteColumnInt(1, []int64{10, 20, 30})
	cw.WriteColumnString(2, []string{"Alice", "Bob", "Carol"})
	cw.WriteColumnString(3, []string{"a@x", "b@x", "c@x"})
	return cw.Bytes()
}

// buildGroupedPostResponse simulates RPC response: user 10→2 posts, 20→0, 30→1.
func buildGroupedPostResponse() []byte {
	var resp []byte
	resp = codec.AppendVarint(resp, 3)
	resp = codec.AppendVarint(resp, 2)
	resp = codec.AppendBytes(resp, encodePost(101, 10, "Post A"))
	resp = codec.AppendBytes(resp, encodePost(102, 10, "Post B"))
	resp = codec.AppendVarint(resp, 0)
	resp = codec.AppendVarint(resp, 1)
	resp = codec.AppendBytes(resp, encodePost(103, 30, "Post C"))
	return resp
}

// verifyBlobCounts checks the item count in each grouped response blob.
func verifyBlobCounts(t *testing.T, blobs [][]byte, expected []int64) {
	t.Helper()
	for i, want := range expected {
		dec := codec.NewDecoder(blobs[i])
		got := dec.ReadInt()
		if got != want {
			t.Errorf("blob[%d] count = %d, want %d", i, got, want)
		}
	}
}

// TestIntegration_ColumnarFederation tests Plan + columnar extraction + MergeColumnar
// for a list of Users with extend posts resolved per-user.
func TestIntegration_ColumnarFederation(t *testing.T) {
	_, userModel := integrationUserModel()

	plan := Plan(userModel, nil, "user")
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}

	primaryColumnar := buildColumnarPrimary()

	ids := ExtractIDColumn(primaryColumnar, plan.IDFieldID, userModel)
	if len(ids) != 3 || ids[0] != 10 || ids[1] != 20 || ids[2] != 30 {
		t.Fatalf("ids = %v, want [10 20 30]", ids)
	}

	groupedResp := buildGroupedPostResponse()
	blobs := ParseGroupedResponse(groupedResp, true)
	if len(blobs) != 3 {
		t.Fatalf("expected 3 blobs, got %d", len(blobs))
	}
	verifyBlobCounts(t, blobs, []int64{2, 0, 1})

	// Step 5: MergeColumnar — add extend column to primary columnar data
	extStep := plan.Extends[0]
	merged := MergeColumnar(primaryColumnar, []ExtendColumnResult{
		{FieldID: extStep.FieldID, Blobs: blobs},
	})

	verifyMergedColumnar(t, merged)
}

// verifyMergedColumnar checks the merged columnar has correct columns and extend blobs.
func verifyMergedColumnar(t *testing.T, merged []byte) {
	t.Helper()
	r := codec.NewColumnarReader(merged)
	if r.Count() != 3 {
		t.Fatalf("count = %d, want 3", r.Count())
	}

	if !r.NextColumn() || r.FieldID() != 1 {
		t.Fatal("expected id column")
	}
	readIDs := r.ReadColumnInt()
	if len(readIDs) != 3 || readIDs[0] != 10 || readIDs[1] != 20 || readIDs[2] != 30 {
		t.Fatalf("ids = %v", readIDs)
	}

	if !r.NextColumn() || r.FieldID() != 2 {
		t.Fatal("expected name column")
	}
	names := r.ReadColumnString()
	if len(names) != 3 || names[0] != "Alice" {
		t.Fatalf("names = %v", names)
	}

	if !r.NextColumn() || r.FieldID() != 3 {
		t.Fatal("expected email column")
	}
	r.ReadColumnString() // skip verification, covered above

	if !r.NextColumn() || r.FieldID() != 10 {
		t.Fatalf("expected posts column (10), got fieldID %d", r.FieldID())
	}
	postBlobs := r.ReadColumnBytes()
	if len(postBlobs) != 3 {
		t.Fatalf("expected 3 post blobs, got %d", len(postBlobs))
	}
	verifyBlobCounts(t, postBlobs, []int64{2, 0, 1})
}

// --- Test 3: Multi-module federation (User with posts + comments) ---

// TestIntegration_MultiModuleFederation tests federation where a single model
// has extend fields from two different modules, verifying both are planned,
// resolved, and merged correctly.
func TestIntegration_MultiModuleFederation(t *testing.T) {
	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name: "User",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "name", Type: schema.FieldString},
			// Extend from post module
			{ID: 10, Name: "posts", Type: schema.FieldModel, TypeName: "Post",
				IsList: true, Relation: true, Module: "post", ForeignKey: "userId"},
			// Extend from comment module
			{ID: 11, Name: "comments", Type: schema.FieldModel, TypeName: "Comment",
				IsList: true, Relation: true, Module: "comment", ForeignKey: "userId"},
		},
	})
	userModel := s.Models["User"]

	// Plan with all fields
	plan := Plan(userModel, nil, "user")
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}
	if len(plan.Extends) != 2 {
		t.Fatalf("expected 2 extend steps, got %d", len(plan.Extends))
	}

	// Verify both modules are represented
	modules := map[string]ExtendStep{}
	for _, ext := range plan.Extends {
		modules[ext.Module] = ext
	}
	if _, ok := modules["post"]; !ok {
		t.Error("missing extend step for post module")
	}
	if _, ok := modules["comment"]; !ok {
		t.Error("missing extend step for comment module")
	}

	// Simulate primary response
	primary := encodeUser(1, "Alice", "a@x.com")

	// Simulate extend responses
	// Posts: 1 post
	var postsData []byte
	postsData = codec.AppendSvarint(postsData, 1)
	postsData = append(postsData, encodePost(100, 1, "Hello")...)

	// Comments: 2 comments (encoded like posts for simplicity)
	var commentsData []byte
	commentsData = codec.AppendSvarint(commentsData, 2)
	commentsData = append(commentsData, encodePost(200, 1, "Comment1")...) // reusing encodePost format
	commentsData = append(commentsData, encodePost(201, 1, "Comment2")...)

	// Merge
	merged := Merge(primary, []ExtendResult{
		{FieldID: modules["post"].FieldID, Data: postsData},
		{FieldID: modules["comment"].FieldID, Data: commentsData},
	})

	// Verify merged response contains the right structure.
	// We check that the merged binary starts with the primary fields and ends
	// with extend fields appended. Since the extend data is raw list binary
	// (count + items), full decoding would require knowing the nested model schema.
	// Instead, verify field presence by checking the binary contains the expected
	// field IDs as varints at the right positions.
	dec := codec.NewDecoder(merged)
	dec.SkipArenaHeader()

	// Read primary fields
	if !dec.NextField() || dec.FieldID() != 1 {
		t.Fatal("expected field 1 (id)")
	}
	if v := dec.ReadInt(); v != 1 {
		t.Errorf("id = %d, want 1", v)
	}

	if !dec.NextField() || dec.FieldID() != 2 {
		t.Fatal("expected field 2 (name)")
	}
	if v := dec.ReadString(); v != "Alice" {
		t.Errorf("name = %q, want %q", v, "Alice")
	}

	if !dec.NextField() || dec.FieldID() != 3 {
		t.Fatal("expected field 3 (email)")
	}
	_ = dec.ReadString()

	// Field 10 (posts) — read the list count header
	if !dec.NextField() || dec.FieldID() != 10 {
		t.Fatal("expected field 10 (posts)")
	}
	postsCount := dec.ReadInt()
	if postsCount != 1 {
		t.Errorf("posts count = %d, want 1", postsCount)
	}
	// Skip past the post items (each is a full binary message with arena header)
	for i := int64(0); i < postsCount; i++ {
		dec.SkipArenaHeader()
		for dec.NextField() {
			switch dec.FieldID() {
			case 1:
				_ = dec.ReadInt()
			case 2:
				_ = dec.ReadString()
			case 3:
				_ = dec.ReadInt()
			}
		}
	}

	// Field 11 (comments) — read the list count header
	if !dec.NextField() || dec.FieldID() != 11 {
		t.Fatalf("expected field 11 (comments), got EOF or fieldID %d", dec.FieldID())
	}
	commentsCount := dec.ReadInt()
	if commentsCount != 2 {
		t.Errorf("comments count = %d, want 2", commentsCount)
	}
}

// --- Test 4: End-to-end binary roundtrip for federation ---

// TestIntegration_BinaryRoundtrip verifies that encoding → Plan → ExtractID → Merge
// produces a valid binary response that can be fully decoded.
func TestIntegration_BinaryRoundtrip(t *testing.T) {
	_, userModel := integrationUserModel()

	// Build a realistic User binary response
	var primary []byte
	primary = codec.AppendVarint(primary, 0) // arena header
	primary = codec.AppendVarint(primary, 1) // id
	primary = codec.AppendSvarint(primary, 7)
	primary = codec.AppendVarint(primary, 2) // name
	primary = codec.AppendString(primary, "Bob")
	primary = codec.AppendVarint(primary, 3) // email
	primary = codec.AppendString(primary, "bob@test.com")
	primary = append(primary, 0x00) // end

	plan := Plan(userModel, nil, "user")
	if plan == nil {
		t.Fatal("expected plan")
	}

	// Extract ID
	id, ok := ExtractID(primary, plan.IDFieldID, plan.FieldTypes)
	if !ok || id != 7 {
		t.Fatalf("extracted id = %d, ok = %v", id, ok)
	}

	// Simulate extend response: 3 posts for user 7
	var postsData []byte
	postsData = codec.AppendSvarint(postsData, 3)
	for i := int64(1); i <= 3; i++ {
		postsData = append(postsData, encodePost(i, 7, "Post")...)
	}

	merged := Merge(primary, []ExtendResult{
		{FieldID: 10, Data: postsData},
	})

	// Full decode of merged response
	dec := codec.NewDecoder(merged)
	dec.SkipArenaHeader()

	var gotID int64
	var gotName, gotEmail string
	var gotPostCount int64

	for dec.NextField() {
		switch dec.FieldID() {
		case 1:
			gotID = dec.ReadInt()
		case 2:
			gotName = dec.ReadString()
		case 3:
			gotEmail = dec.ReadString()
		case 10:
			gotPostCount = dec.ReadInt()
			// Skip past the post items (each is a full message)
			for i := int64(0); i < gotPostCount; i++ {
				// Skip arena header
				dec.SkipArenaHeader()
				// Read each post's fields until end marker
				for dec.NextField() {
					fid := dec.FieldID()
					switch fid {
					case 1:
						_ = dec.ReadInt() // post id
					case 2:
						_ = dec.ReadString() // title
					case 3:
						_ = dec.ReadInt() // userId
					}
				}
			}
		}
	}
	if dec.Err() != nil {
		t.Fatalf("decoder error: %v", dec.Err())
	}
	if gotID != 7 {
		t.Errorf("id = %d, want 7", gotID)
	}
	if gotName != "Bob" {
		t.Errorf("name = %q, want %q", gotName, "Bob")
	}
	if gotEmail != "bob@test.com" {
		t.Errorf("email = %q, want %q", gotEmail, "bob@test.com")
	}
	if gotPostCount != 3 {
		t.Errorf("post count = %d, want 3", gotPostCount)
	}
}
