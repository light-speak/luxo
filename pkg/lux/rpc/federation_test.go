package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
)

// encodePost builds a binary-encoded Post model for testing.
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

// --- Test: End-to-End RPC BatchLoad ---

// TestFederation_RPCBatchLoad tests the full RPC batch load flow:
// 1. Start an in-process RPC server with a batch load handler
// 2. Create an RPC client
// 3. Call the batch load endpoint with a set of keys
// 4. Verify the response contains correct models
// 5. Verify binary encoding/decoding roundtrips
func TestFederation_RPCBatchLoad(t *testing.T) {
	rt := api.NewRouter()

	// Register a "svc:resolve:Post:userId" handler that simulates batch loading.
	// It receives user IDs as a param and returns a grouped response:
	// [key_count varint] per-key: [item_count varint] [len-prefixed items...]
	rt.Handle("svc:resolve:Post:userId", func(ctx context.Context, req *api.Request) error {
		// In a real system, params would contain the FK keys.
		// For this test, we simulate returning posts for 3 users: IDs 10, 20, 30.
		// User 10: 2 posts, User 20: 0 posts, User 30: 1 post
		buf := req.Buf

		// key_count = 3
		buf.B = codec.AppendVarint(buf.B, 3)

		// Key 0 (user 10): 2 posts
		buf.B = codec.AppendVarint(buf.B, 2)
		buf.B = codec.AppendBytes(buf.B, encodePost(101, 10, "Post A"))
		buf.B = codec.AppendBytes(buf.B, encodePost(102, 10, "Post B"))

		// Key 1 (user 20): 0 posts
		buf.B = codec.AppendVarint(buf.B, 0)

		// Key 2 (user 30): 1 post
		buf.B = codec.AppendVarint(buf.B, 1)
		buf.B = codec.AppendBytes(buf.B, encodePost(103, 30, "Post C"))

		return nil
	})
	rt.Registry.Register("svc:resolve:Post:userId", 100)
	rt.Registry.RegisterParams("svc:resolve:Post:userId", nil)

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19900")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19900")
	defer client.Close()

	// Call the batch load endpoint
	resp, err := client.Call(100, nil)
	if err != nil {
		t.Fatalf("RPC call failed: %v", err)
	}
	if len(resp) == 0 {
		t.Fatal("empty response")
	}

	// Parse grouped response
	off := 0
	keyCount, n := codec.ReadVarint(resp, off)
	if n <= 0 {
		t.Fatal("failed to read key_count")
	}
	off += n

	if keyCount != 3 {
		t.Fatalf("key_count = %d, want 3", keyCount)
	}

	type keyResult struct {
		itemCount int
		posts     []struct {
			id     int64
			title  string
			userID int64
		}
	}

	results := make([]keyResult, keyCount)
	for k := 0; k < int(keyCount); k++ {
		itemCount, n := codec.ReadVarint(resp, off)
		if n <= 0 {
			t.Fatalf("key %d: failed to read item_count", k)
		}
		off += n
		results[k].itemCount = int(itemCount)

		for i := 0; i < int(itemCount); i++ {
			item, rn := codec.ReadBytes(resp, off)
			if rn == 0 {
				t.Fatalf("key %d, item %d: failed to read item", k, i)
			}
			off += rn

			// Decode the post binary
			dec := codec.NewDecoder(item)
			dec.SkipArenaHeader()
			var post struct {
				id     int64
				title  string
				userID int64
			}
			for dec.NextField() {
				switch dec.FieldID() {
				case 1:
					post.id = dec.ReadInt()
				case 2:
					post.title = dec.ReadString()
				case 3:
					post.userID = dec.ReadInt()
				}
			}
			if dec.Err() != nil {
				t.Fatalf("post decode error: %v", dec.Err())
			}
			results[k].posts = append(results[k].posts, post)
		}
	}

	// Verify key 0: 2 posts for user 10
	if results[0].itemCount != 2 {
		t.Errorf("key0 itemCount = %d, want 2", results[0].itemCount)
	}
	if len(results[0].posts) != 2 {
		t.Fatalf("key0 posts = %d, want 2", len(results[0].posts))
	}
	if results[0].posts[0].id != 101 || results[0].posts[0].title != "Post A" {
		t.Errorf("key0.post0 = %+v", results[0].posts[0])
	}
	if results[0].posts[1].id != 102 || results[0].posts[1].userID != 10 {
		t.Errorf("key0.post1 = %+v", results[0].posts[1])
	}

	// Verify key 1: 0 posts for user 20
	if results[1].itemCount != 0 {
		t.Errorf("key1 itemCount = %d, want 0", results[1].itemCount)
	}

	// Verify key 2: 1 post for user 30
	if results[2].itemCount != 1 {
		t.Errorf("key2 itemCount = %d, want 1", results[2].itemCount)
	}
	if len(results[2].posts) != 1 || results[2].posts[0].id != 103 {
		t.Errorf("key2.post0 = %+v", results[2].posts)
	}
}

// --- Test: RPC BatchLoad with field mask ---

// TestFederation_RPCBatchLoadWithMask tests that field masks are correctly
// forwarded through the RPC layer (the handler receives the mask).
func TestFederation_RPCBatchLoadWithMask(t *testing.T) {
	rt := api.NewRouter()

	// Handler echoes back the field mask length as the response
	rt.Handle("svc:resolve:Post:userId", func(ctx context.Context, req *api.Request) error {
		maskLen := int64(len(req.FieldMask))
		req.Buf.B = codec.AppendVarint(req.Buf.B, 1) // field 1
		req.Buf.B = codec.AppendSvarint(req.Buf.B, maskLen)
		req.Buf.B = append(req.Buf.B, 0x00)
		return nil
	})
	rt.Registry.Register("svc:resolve:Post:userId", 101)
	rt.Registry.RegisterParams("svc:resolve:Post:userId", nil)

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19901")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19901")
	defer client.Close()

	// Send request with a field mask
	mask := codec.FieldMaskSet(nil, 1) // id
	mask = codec.FieldMaskSet(mask, 2) // title
	mask = codec.AppendSelectionMask(nil, mask, nil)
	resp, err := client.CallWithMask(101, mask, nil)
	if err != nil {
		t.Fatalf("RPC call failed: %v", err)
	}

	dec := codec.NewDecoder(resp)
	if !dec.NextField() || dec.FieldID() != 1 {
		t.Fatal("expected field 1")
	}
	maskLen := dec.ReadInt()
	if maskLen != int64(len(mask)) {
		t.Errorf("mask length = %d, want %d", maskLen, len(mask))
	}
}

// --- Test: RPC multiple sequential calls on same connection ---

// TestFederation_RPCMultipleCalls verifies that connection pooling works
// across multiple sequential RPC calls (simulates a gateway calling
// multiple batch loads in sequence).
func TestFederation_RPCMultipleCalls(t *testing.T) {
	rt := api.NewRouter()

	callCount := 0
	rt.Handle("svc:batchLoad", func(ctx context.Context, req *api.Request) error {
		callCount++
		req.Buf.B = codec.AppendVarint(req.Buf.B, 1)
		req.Buf.B = codec.AppendSvarint(req.Buf.B, int64(callCount))
		req.Buf.B = append(req.Buf.B, 0x00)
		return nil
	})
	rt.Registry.Register("svc:batchLoad", 102)
	rt.Registry.RegisterParams("svc:batchLoad", nil)

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19902")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19902")
	defer client.Close()

	// Make 5 sequential calls (simulates federation resolving multiple extends)
	for i := 1; i <= 5; i++ {
		resp, err := client.Call(102, nil)
		if err != nil {
			t.Fatalf("call %d failed: %v", i, err)
		}
		dec := codec.NewDecoder(resp)
		if !dec.NextField() {
			t.Fatalf("call %d: no field", i)
		}
		v := dec.ReadInt()
		if v != int64(i) {
			t.Errorf("call %d: got %d, want %d", i, v, i)
		}
	}
}

// --- Test: RPC binary encoding roundtrip for different data types ---

// TestFederation_RPCBinaryRoundtrip verifies that a response with mixed field
// types (int, string, float, bool) encodes/decodes correctly through the RPC layer.
func TestFederation_RPCBinaryRoundtrip(t *testing.T) {
	rt := api.NewRouter()

	rt.Handle("svc:resolve:Profile", func(ctx context.Context, req *api.Request) error {
		buf := req.Buf
		// Encode a rich model: id(int), name(string), score(float), active(bool)
		buf.B = codec.AppendVarint(buf.B, 0) // arena header
		buf.B = codec.AppendVarint(buf.B, 1) // field 1 = id
		buf.B = codec.AppendSvarint(buf.B, 42)
		buf.B = codec.AppendVarint(buf.B, 2) // field 2 = name
		buf.B = codec.AppendString(buf.B, "Alice")
		buf.B = codec.AppendVarint(buf.B, 3) // field 3 = score
		buf.B = codec.AppendFixed64(buf.B, 99.5)
		buf.B = codec.AppendVarint(buf.B, 4) // field 4 = active
		buf.B = codec.AppendBool(buf.B, true)
		buf.B = append(buf.B, 0x00)
		return nil
	})
	rt.Registry.Register("svc:resolve:Profile", 103)
	rt.Registry.RegisterParams("svc:resolve:Profile", nil)

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19903")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19903")
	defer client.Close()

	resp, err := client.Call(103, nil)
	if err != nil {
		t.Fatalf("RPC call failed: %v", err)
	}

	// Decode and verify each field
	dec := codec.NewDecoder(resp)
	dec.SkipArenaHeader()

	var id int64
	var name string
	var score float64
	var active bool

	for dec.NextField() {
		switch dec.FieldID() {
		case 1:
			id = dec.ReadInt()
		case 2:
			name = dec.ReadString()
		case 3:
			score = dec.ReadFloat()
		case 4:
			active = dec.ReadBool()
		}
	}
	if dec.Err() != nil {
		t.Fatalf("decoder error: %v", dec.Err())
	}
	if id != 42 {
		t.Errorf("id = %d, want 42", id)
	}
	if name != "Alice" {
		t.Errorf("name = %q, want %q", name, "Alice")
	}
	if score != 99.5 {
		t.Errorf("score = %f, want 99.5", score)
	}
	if !active {
		t.Error("active should be true")
	}
}
