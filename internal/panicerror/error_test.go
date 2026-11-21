package panicerror

import "testing"

type hostileValue struct{}

func (hostileValue) Error() string  { panic("must not invoke Error") }
func (hostileValue) String() string { panic("must not invoke String") }

func TestNew(t *testing.T) {
	for _, value := range []any{nil, 1, hostileValue{}, (*hostileValue)(nil), []byte("private")} {
		if got := New("panic: ", value).Error(); got != "handler panicked with a non-string value" {
			t.Fatal(got)
		}
	}
	for _, value := range []string{"", "boom"} {
		if got := New("panic: ", value).Error(); got != "panic: "+value {
			t.Fatal(got)
		}
	}
}
