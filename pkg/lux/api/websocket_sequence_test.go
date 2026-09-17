package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/jsonutil"
	"nhooyr.io/websocket"
)

func TestWSRequestSequenceEncodingParity(t *testing.T) {
	rt := testWSRouter()
	rt.Registry.Register("failAPI", 11)
	rt.Registry.RegisterParams("failAPI", []ParamMeta{{Name: "id", Type: "Int", FieldID: 1}})
	definition := *rt.Schema.APIs["getUser"]
	definition.Name, definition.ID = "failAPI", 11
	rt.Schema.RegisterAPI(&definition)
	srv := httptest.NewServer(rt)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	ids := []uint64{0, 1, 253, 1 << 32, 1<<53 - 1, 1 << 53, 1<<53 + 1, 1<<63 - 1, 1 << 63, 1<<64 - 1}
	for _, id := range ids {
		for _, mode := range []websocket.MessageType{websocket.MessageText, websocket.MessageBinary} {
			for _, api := range []string{"getUser", "failAPI"} {
				if err := conn.Write(ctx, mode, sequenceRequest(mode, api, id)); err != nil {
					t.Fatal(err)
				}
				responseMode, data, err := conn.Read(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if responseMode != mode {
					t.Fatalf("response mode %v, want %v", responseMode, mode)
				}
				assertSequenceResponse(t, mode, api, id, data)
			}
		}
	}
}

func sequenceRequest(mode websocket.MessageType, api string, id uint64) []byte {
	if mode == websocket.MessageText {
		return []byte(`{"$id":` + strconv.FormatUint(id, 10) + `,"$api":"` + api + `","$select":"id,name","id":42}`)
	}
	apiID := uint64(10)
	if api == "failAPI" {
		apiID = 11
	}
	data := codec.AppendVarint([]byte{BinaryFrameCallRequest}, id)
	data = codec.AppendVarint(data, apiID)
	mask := codec.AppendSelectionMask(nil, []byte{0x06}, nil)
	data = codec.AppendVarint(data, uint64(len(mask)))
	data = append(data, mask...)
	var encoder codec.Encoder
	encoder.WriteFieldInt(1, 42)
	encoder.WriteEnd()
	return append(data, encoder.Bytes()...)
}

func assertSequenceResponse(t *testing.T, mode websocket.MessageType, api string, id uint64, data []byte) {
	t.Helper()
	if mode == websocket.MessageText {
		var response map[string]json.RawMessage
		if err := json.Unmarshal(data, &response); err != nil {
			t.Fatal(err)
		}
		if string(response["$id"]) != strconv.FormatUint(id, 10) {
			t.Errorf("%s: response %s does not preserve ID %d", api, data, id)
		}
		if (response["error"] != nil) != (api == "failAPI") {
			t.Errorf("%s: unexpected response %s", api, data)
		}
		if api == "failAPI" && string(response["code"]) != "404" {
			t.Errorf("handler error was not propagated: %s", data)
		}
		return
	}
	if len(data) < 2 {
		t.Fatalf("truncated frame: %x", data)
	}
	gotID, n := codec.ReadVarint(data, 1)
	wantType := byte(BinaryFrameCallSuccess)
	if api == "failAPI" {
		wantType = BinaryFrameCallError
	}
	if n <= 0 || gotID != id || data[0] != wantType {
		t.Errorf("%s: frame %x, want type %d and ID %d", api, data, wantType, id)
	}
}

func TestWSJSONSequenceRejectsInvalidTokensBeforeDispatch(t *testing.T) {
	rt := NewRouter()
	rt.Handle("mustNotRun", func(context.Context, *Request) error {
		t.Fatal("invalid ID must not dispatch the handler")
		return nil
	})
	for _, token := range []string{
		"", "null", "true", "false", `"1"`, "[]", "{}", "-1", "-0", "1.0", "1e0", "1.5",
		"18446744073709551616", "18446744073709551617", "999999999999999999999999999999999",
	} {
		t.Run(token, func(t *testing.T) {
			body := `{"$api":"mustNotRun"}`
			if token != "" {
				body = `{"$id":` + token + `,"$api":"mustNotRun"}`
			}
			// Like malformed binary sequences, invalid JSON IDs cannot be correlated.
			// No socket is needed: neither a handler nor a response may run.
			raw, err := jsonutil.ParseObject([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
			rt.handleWSJSON(context.Background(), nil, raw)
		})
	}
}

func TestParseWSJSONSequence(t *testing.T) {
	for _, token := range []string{"0", "1", "253", "9007199254740993", "9223372036854775808", "18446744073709551615"} {
		id, ok := parseWSJSONSequence([]byte(token))
		if !ok || strconv.FormatUint(id, 10) != token {
			t.Errorf("rejected or changed valid ID %q: %d, %v", token, id, ok)
		}
	}
	for _, token := range []string{"", "+1", "-1", "01", " 1", "1 ", "1.0", "1e0", "1x", "18446744073709551616"} {
		if _, ok := parseWSJSONSequence([]byte(token)); ok {
			t.Errorf("accepted invalid ID %q", token)
		}
	}
}

func TestWSJSONSequenceValidTokensDoNotAllocate(t *testing.T) {
	for _, token := range []string{"0", "1", "9007199254740993", "18446744073709551615"} {
		raw := []byte(token)
		allocations := testing.AllocsPerRun(1000, func() {
			if _, ok := parseWSJSONSequence(raw); !ok {
				t.Fatal("valid ID rejected")
			}
		})
		if allocations != 0 {
			t.Errorf("ID %s: %g allocations, want zero", token, allocations)
		}
	}
}
