package rpc

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/light-speak/luxo/internal/testutil"
	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
)

// TestFederation_RPCBatchLoad sends explicit keys/shape and canonical columnar
// groups through real TCP RPC. This tests transport, not database execution.
func TestFederation_RPCBatchLoad(t *testing.T) {
	rt := api.NewRouter()
	groups := [][]byte{
		postColumns(t, []int64{101, 102}, []int64{10, 10}, []string{"Post A", "Post B"}),
		postColumns(t, nil, nil, nil),
		postColumns(t, []int64{103}, []int64{30}, []string{"Post C"}),
	}
	rt.Handle("svc:resolve:Post:userId", func(_ context.Context, req *api.Request) error {
		keys, err := req.ParamIntArray("keys")
		if err != nil {
			return err
		}
		shape, err := req.ParamInt("shape")
		if err != nil {
			return err
		}
		if !slices.Equal(keys, []int64{10, 20, 30}) || shape != 1 {
			return fmt.Errorf("invalid resolver parameters")
		}
		req.Buf.B = codec.AppendVarint(req.Buf.B, uint64(len(groups)))
		for _, group := range groups {
			req.Buf.B = codec.AppendBytes(req.Buf.B, group)
		}
		return nil
	})
	rt.Registry.Register("svc:resolve:Post:userId", 100)
	rt.Registry.RegisterParams("svc:resolve:Post:userId", []api.ParamMeta{{FieldID: 1, Name: "keys", Type: "Int", IsList: true}, {FieldID: 2, Name: "shape", Type: "Int"}})
	srv := NewServer(rt)
	addr := startTestRPCServer(t, srv)
	defer srv.Close()
	client := NewClient(addr)
	defer client.Close()
	params := []byte{1, 3}
	for _, key := range []int64{10, 20, 30} {
		params = codec.AppendSvarint(params, key)
	}
	params = codec.AppendSvarint(append(params, 2), 1)
	params = append(params, 0)
	resp, err := client.Call(100, params)
	if err != nil {
		t.Fatal(err)
	}
	count, n := codec.ReadVarint(resp, 0)
	if n <= 0 || count != uint64(len(groups)) {
		t.Fatalf("invalid group count: %x", resp)
	}
	for i, want := range groups {
		body, size := codec.ReadBytes(resp, n)
		if size <= 0 || !bytes.Equal(body, want) {
			t.Fatalf("group %d changed: %x", i, body)
		}
		n += size
	}
	if n != len(resp) {
		t.Fatal("trailing resolver bytes")
	}
}

func postColumns(t testing.TB, ids, userIDs []int64, titles []string) []byte {
	w := &codec.ColumnarWriter{}
	w.SetCount(len(ids))
	if len(ids) > 0 {
		w.WriteColumnInt(1, ids)
		w.WriteColumnString(2, titles)
		w.WriteColumnInt(3, userIDs)
	}
	return testutil.Bytes(t)(w.Bytes())
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
	addr := startTestRPCServer(t, srv)
	defer srv.Close()

	client := NewClient(addr)
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
	addr := startTestRPCServer(t, srv)
	defer srv.Close()

	client := NewClient(addr)
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
		if encoded, err := codec.AppendString(buf.B, "Alice"); err != nil {
			return err
		} else {
			buf.B = encoded
		}
		buf.B = codec.AppendVarint(buf.B, 3) // field 3 = score
		encoded, err := codec.AppendFixed64(buf.B, 99.5)
		if err != nil {
			return err
		}
		buf.B = encoded
		buf.B = codec.AppendVarint(buf.B, 4) // field 4 = active
		buf.B = codec.AppendBool(buf.B, true)
		buf.B = append(buf.B, 0x00)
		return nil
	})
	rt.Registry.Register("svc:resolve:Profile", 103)
	rt.Registry.RegisterParams("svc:resolve:Profile", nil)

	srv := NewServer(rt)
	addr := startTestRPCServer(t, srv)
	defer srv.Close()

	client := NewClient(addr)
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
