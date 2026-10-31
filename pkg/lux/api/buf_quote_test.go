package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

// Keep the benchmark entry point attached to the actual public writer.
func appendStrictJSONQuote(buf *ResponseBuf, value string) error {
	return buf.AppendJSONString(value)
}

var errQuoteUTF8 = errors.New("invalid UTF-8")

// Alternative candidate: prevalidate, then retain the existing manual writer.
func appendStrictManualQuote(buf *ResponseBuf, value string) error {
	if !utf8.ValidString(value) {
		return errQuoteUTF8
	}
	buf.appendJSONStringBeforeStrict(value)
	return nil
}

func invalidQuoteInputs() []string {
	return []string{"\x80", "\xff", "\xc0\x80", "\xc2", "\xc2x", "\xe0\x80\x80",
		"\xe2\x82", "\xed\xa0\x80", "\xf0\x80\x80\x80", "\xf0\x9f\x92", "\xf4\x90\x80\x80"}
}

func TestJSONStringWriterRejectsInvalidUTF8(t *testing.T) {
	checkInvalidQuoteCandidate(t, (*ResponseBuf).AppendJSONString)
}

func TestJSONQuoteCandidateRejectsInvalidUTF8(t *testing.T) {
	for _, write := range []func(*ResponseBuf, string) error{appendStrictJSONQuote, appendStrictManualQuote} {
		checkInvalidQuoteCandidate(t, write)
	}
}

func checkInvalidQuoteCandidate(t *testing.T, write func(*ResponseBuf, string) error) {
	t.Helper()
	for _, invalid := range invalidQuoteInputs() {
		for _, value := range []string{invalid, "valid" + invalid, invalid + "valid", "valid" + invalid + "tail"} {
			for _, capacity := range []int{6, 4096} {
				buf := ResponseBuf{B: make([]byte, 6, capacity)}
				copy(buf.B, "prefix")
				if err := write(&buf, value); err == nil || string(buf.B) != "prefix" {
					t.Fatalf("invalid text published: %x -> %q / %v", value, buf.B, err)
				}
				if err := write(&buf, "ok"); err != nil || string(buf.B) != `prefix"ok"` {
					t.Fatalf("buffer not reusable after error: %q / %v", buf.B, err)
				}
			}
		}
	}
}

func TestJSONQuoteCandidatePreservesText(t *testing.T) {
	for _, value := range []string{"", "hello", "日本語", "\ufeff東京", "\ufffd", "e\u0301", "\U0001f642", "\U0010ffff",
		"\x00\x1f\t\n\r", "say \"hi\"\\", "<>&\u2028\u2029", "\b\f"} {
		checkJSONQuoteCandidate(t, value)
	}
}

func checkJSONQuoteCandidate(t *testing.T, value string) {
	t.Helper()
	buf := ResponseBuf{B: []byte("prefix")}
	err := appendStrictJSONQuote(&buf, value)
	if !utf8.ValidString(value) {
		if err == nil || string(buf.B) != "prefix" {
			t.Fatalf("invalid text accepted: %x / %v", value, err)
		}
		return
	}
	var got string
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(buf.B[6:], &got); err != nil || got != value {
		t.Fatalf("text changed: %x -> %x / %v", value, got, err)
	}
	old := ResponseBuf{}
	old.appendJSONStringBeforeStrict(value)
	// Backspace/form feed have shorter RFC 8785 escapes. Literal backslash-u
	// sequences are not control bytes and must remain byte-for-byte unchanged.
	if !strings.ContainsAny(value, "\b\f") && !bytes.Equal(buf.B[6:], old.B) {
		t.Fatalf("unexpected representation change: %x -> %q / %q", value, old.B, buf.B[6:])
	}
}

func TestJSONQuoteCandidateControlEscapes(t *testing.T) {
	buf := ResponseBuf{}
	if err := appendStrictJSONQuote(&buf, "\b\f"); err != nil || string(buf.B) != `"\b\f"` {
		t.Fatalf("unexpected control representation: %q / %v", buf.B, err)
	}
}

func TestJSONQuoteCandidateReusesCapacity(t *testing.T) {
	buf := ResponseBuf{B: make([]byte, 0, 4096)}
	for _, value := range []string{"", "ASCII", "\ufeff東京", "\x00\b\f\n", strings.Repeat("東京", 171)} {
		if count := testing.AllocsPerRun(100, func() { buf.B = buf.B[:0]; _ = appendStrictJSONQuote(&buf, value) }); count != 0 {
			t.Fatalf("valid candidate allocated: %v", count)
		}
	}
}

func FuzzJSONQuoteCandidate(f *testing.F) {
	for _, value := range append(invalidQuoteInputs(), "\b\f", "\\u0008", "<>&\u2028\u2029", "\ufeff東京") {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, value string) { checkJSONQuoteCandidate(t, value) })
}

func BenchmarkJSONQuoteCandidate(b *testing.B) {
	fixtures := []struct{ name, value string }{
		{"empty", ""}, {"ascii16", "0123456789abcdef"}, {"ascii1024", strings.Repeat("a", 1024)},
		{"unicode24", strings.Repeat("東京", 4)}, {"unicode1026", strings.Repeat("東京", 171)},
		{"escapes24", strings.Repeat("\x00\b\f\n\"\\", 4)}, {"html", "<>&\u2028\u2029"},
	}
	for _, fixture := range fixtures {
		b.Run(fixture.name, func(b *testing.B) {
			for _, candidate := range []struct {
				name  string
				write func(*ResponseBuf, string) error
			}{
				{"current", func(buf *ResponseBuf, value string) error { buf.appendJSONStringBeforeStrict(value); return nil }},
				{"manual-strict", appendStrictManualQuote}, {"stdlib", appendStrictJSONQuote},
			} {
				b.Run(candidate.name, func(b *testing.B) {
					buf := ResponseBuf{B: make([]byte, 0, len(fixture.value)*6+2)}
					if err := candidate.write(&buf, fixture.value); err != nil {
						b.Fatal(err)
					}
					b.ReportAllocs()
					for b.Loop() {
						buf.B = buf.B[:0]
						_ = candidate.write(&buf, fixture.value)
					}
				})
			}
		})
	}
}

func BenchmarkJSONQuoteInvalidCandidate(b *testing.B) {
	for _, fixture := range []struct{ name, value string }{
		{"first16", "\xff0123456789abcdef"}, {"last16", "0123456789abcdef\xff"},
		{"first1024", "\xff" + strings.Repeat("a", 1024)}, {"last1024", strings.Repeat("a", 1024) + "\xff"},
	} {
		for _, candidate := range []struct {
			name  string
			write func(*ResponseBuf, string) error
		}{
			{"manual-strict", appendStrictManualQuote}, {"stdlib", appendStrictJSONQuote},
		} {
			b.Run(fixture.name+"/"+candidate.name, func(b *testing.B) {
				buf := ResponseBuf{B: make([]byte, 6, len(fixture.value)*6+8)}
				copy(buf.B, "prefix")
				if err := candidate.write(&buf, fixture.value); err == nil || string(buf.B) != "prefix" {
					b.Fatal("invalid benchmark result")
				}
				b.ReportAllocs()
				for b.Loop() {
					_ = candidate.write(&buf, fixture.value)
				}
			})
		}
	}
}
