package schema

import (
	"testing"
	"time"

	"github.com/light-speak/luxo/internal/testutil"
	"github.com/light-speak/luxo/pkg/lux/codec"
)

func TestDateTimeNanosecondJSONRoundTrip(t *testing.T) {
	s := New()
	m := &Model{Name: "Timestamp", Fields: []Field{
		{ID: 1, Name: "value", Type: FieldDateTime},
		{ID: 2, Name: "optional", Type: FieldDateTime, Nullable: true},
		{ID: 3, Name: "values", Type: FieldDateTime, IsList: true},
	}}
	s.RegisterModel(m)
	for _, stamp := range []string{
		"2026-09-17T12:00:00.123456789Z",
		"1969-12-31T23:59:59.999999999Z",
		"9999-12-31T23:59:59.999999999Z",
	} {
		t.Run(stamp, func(t *testing.T) {
			input := `{"value":"` + stamp + `","optional":"` + stamp + `","values":["` + stamp + `"]}`
			wire, err := s.JSONToBinary(m.Name, []byte(input))
			if err != nil {
				t.Fatal(err)
			}
			if got := string(checkedJSON(t)(BinaryToJSON(nil, wire, m, s))); got != input {
				t.Fatalf("precision lost: %s", got)
			}
			value, err := time.Parse(time.RFC3339Nano, stamp)
			if err != nil {
				t.Fatal(err)
			}
			d := codec.NewDecoder(wire)
			d.SkipArenaHeader()
			assertNextField(t, d, 1)
			if actual := d.ReadDateTime(); !actual.Equal(value) || d.Err() != nil {
				t.Fatalf("not a seconds/nanoseconds pair: %v / %v", actual, d.Err())
			}
		})
	}
}

func TestDateTimeNanosecondColumns(t *testing.T) {
	s := New()
	m := &Model{Name: "Timestamp", Fields: []Field{
		{ID: 1, Name: "value", Type: FieldDateTime},
		{ID: 2, Name: "optional", Type: FieldDateTime, Nullable: true},
	}}
	s.RegisterModel(m)
	value := time.Unix(-1, 999999999).UTC()
	w := &codec.ColumnarWriter{}
	w.SetCount(2)
	w.WriteColumnDateTime(1, []time.Time{value, value})
	w.WriteColumnDateTimePtr(2, []*time.Time{&value, nil})
	wire := testutil.Bytes(t)(w.Bytes())
	want := `[{"value":"1969-12-31T23:59:59.999999999Z","optional":"1969-12-31T23:59:59.999999999Z"},{"value":"1969-12-31T23:59:59.999999999Z","optional":null}]`
	if got := string(checkedJSON(t)(BinaryListToJSON(nil, wire, m, s))); got != want {
		t.Fatalf("column precision lost: %s", got)
	}
}
