package testutil

import (
	"bytes"
	"errors"
	"testing"
)

type recordingTest struct {
	testing.TB
	err     error
	helpers int
}

func (r *recordingTest) Helper()           { r.helpers++ }
func (r *recordingTest) Fatal(args ...any) { r.err = args[0].(error) }

func TestBytes(t *testing.T) {
	data := []byte{1, 2}
	want := errors.New("fixture failed")
	for _, err := range []error{nil, want} {
		recorder := &recordingTest{}
		got := Bytes(recorder)(data, err)
		if recorder.err != err || recorder.helpers != 2 || !bytes.Equal(got, data) {
			t.Fatalf("assertion result: %x, %v, %d", got, recorder.err, recorder.helpers)
		}
	}
}
