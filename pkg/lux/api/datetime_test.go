package api_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
)

// Observe the business value before a response encoder can hide precision loss.
func TestDateTimeBusinessValueParity(t *testing.T) {
	for _, input := range []string{
		"2026-09-17T12:00:00.123456789Z",
		"1969-12-31T23:59:59.999999999Z",
		"9999-12-31T23:59:59.999999999Z",
	} {
		value, err := time.Parse(time.RFC3339Nano, input)
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{"json", "binary"} {
			for _, canonical := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/forward=%t", input, mode, canonical), func(t *testing.T) {
					seconds, nanos := inspectValue(t, input, mode, canonical)
					t.Logf("business value seconds=%d nanos=%d", seconds, nanos)
					if seconds != value.Unix() || nanos != value.Nanosecond() {
						t.Errorf("business value changed: want seconds=%d nanos=%d", value.Unix(), value.Nanosecond())
					}
				})
			}
		}
	}
}

func inspectValue(t *testing.T, input, mode string, canonical bool) (int64, int) {
	t.Helper()
	router := api.NewRouter()
	meta := []api.ParamMeta{{Name: "value", Type: "DateTime", FieldID: 1}}
	router.Registry.Register("inspectTimestamp", 1)
	router.Registry.RegisterParams("inspectTimestamp", meta)
	var seconds int64
	nanos, called := 0, false
	router.Handle("inspectTimestamp", func(_ context.Context, req *api.Request) error {
		businessRequest := req
		if canonical {
			var err error
			businessRequest, err = router.Registry.ParseBinaryRequest(req.BinaryRequest())
			if err != nil {
				return err
			}
		}
		value, err := businessRequest.ParamDateTime("value")
		if err != nil {
			return err
		}
		seconds, nanos, called = value.Unix(), value.Nanosecond(), true
		if req.BinaryMode {
			req.Buf.AppendBytes(codec.AppendSvarint(nil, int64(nanos)))
		} else {
			req.Buf.AppendInt(int64(nanos))
		}
		return nil
	})
	data := []byte(`{"$api":"inspectTimestamp","value":` + strconv.Quote(input) + `}`)
	if mode == "binary" {
		var err error
		data, err = api.EncodeBinaryRequest(1, nil, map[string]any{"value": input}, meta)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/luvia", bytes.NewReader(data))
	request.Header.Set("X-Luxo-Mode", mode)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !called {
		t.Fatalf("handler was not called successfully: %d %s", response.Code, response.Body.String())
	}
	return seconds, nanos
}
