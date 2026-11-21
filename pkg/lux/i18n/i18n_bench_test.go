package i18n

import (
	"fmt"
	"math"
	"strings"
	"testing"

	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

func BenchmarkStaticErrorInterpolation(b *testing.B) {
	// Router already holds ErrorData as an interface. Measure substitution, not
	// another interface conversion that does not occur in the production call.
	var data Data = luxerrors.ResourceError{Resource: "User", ID: luxerrors.IntID(math.MaxInt64)}
	formerData := map[string]any{"resource": "User", "id": int64(math.MaxInt64)}
	for _, template := range []string{"Resource not found", "${resource}:${id}:${missing}"} {
		for _, former := range []bool{true, false} {
			name := "static"
			if former {
				name = "former_map"
			}
			b.Run(template+"/"+name, func(b *testing.B) {
				var output string
				b.ReportAllocs()
				for b.Loop() {
					if former {
						output = substituteFormer(template, formerData)
					} else {
						output = substitute(template, data)
					}
				}
				if output != substitute(template, data) {
					b.Fatal("interpolation output changed")
				}
			})
		}
	}
}

func BenchmarkInterpolationBufferReuse(b *testing.B) {
	var resource Data = luxerrors.ResourceError{Resource: "User", ID: luxerrors.IntID(math.MaxInt64)}
	for _, tc := range []struct {
		name, template string
		data           Data
	}{
		{"no_fields", "Resource not found", resource},
		{"integer_id", "${resource}:${id}:${missing}", resource},
		{"short_string", "Hello ${name}", Values{"name": "world"}},
		{"long_string", "Hello ${name}", Values{"name": strings.Repeat("x", 4096)}},
		{"long_literal", strings.Repeat("prefix", 512) + "${name}", Values{"name": "world"}},
		{"missing", "${missing}", Values{}},
		{"unclosed", "${unclosed", resource},
	} {
		for _, former := range []bool{true, false} {
			name := "pooled"
			if former {
				name = "static_before_pool"
			}
			b.Run(tc.name+"/"+name, func(b *testing.B) {
				want := substituteUnpooled(tc.template, tc.data)
				var output string
				b.ReportAllocs()
				for b.Loop() {
					if former {
						output = substituteUnpooled(tc.template, tc.data)
					} else {
						output = substitute(tc.template, tc.data)
					}
				}
				if output != want {
					b.Fatalf("output = %q, want %q", output, want)
				}
			})
		}
	}
}

// Frozen static implementation before pooling. Do not share the production
// parser: this measures the entire substitution change, not only allocation.
func substituteUnpooled(tmpl string, data Data) string {
	if data == nil || !strings.Contains(tmpl, "${") {
		return tmpl
	}
	b := make([]byte, 0, len(tmpl))
	i := 0
	for i < len(tmpl) {
		if i+1 < len(tmpl) && tmpl[i] == '$' && tmpl[i+1] == '{' {
			end := strings.IndexByte(tmpl[i+2:], '}')
			if end >= 0 {
				varName := tmpl[i+2 : i+2+end]
				start := len(b)
				var found bool
				b, found = data.AppendText(b, varName)
				if !found {
					b = append(b[:start], tmpl[i:i+2+end+1]...)
				}
				i = i + 2 + end + 1
				continue
			}
		}
		b = append(b, tmpl[i])
		i++
	}
	return string(b)
}

// Frozen pre-migration implementation. Reflection is restricted to this baseline.
func substituteFormer(tmpl string, data map[string]any) string {
	if len(data) == 0 || !strings.Contains(tmpl, "${") {
		return tmpl
	}
	var b strings.Builder
	b.Grow(len(tmpl))
	i := 0
	for i < len(tmpl) {
		if i+1 < len(tmpl) && tmpl[i] == '$' && tmpl[i+1] == '{' {
			end := strings.IndexByte(tmpl[i+2:], '}')
			if end >= 0 {
				varName := tmpl[i+2 : i+2+end]
				if val, ok := data[varName]; ok {
					fmt.Fprint(&b, val)
				} else {
					b.WriteString(tmpl[i : i+2+end+1])
				}
				i = i + 2 + end + 1
				continue
			}
		}
		b.WriteByte(tmpl[i])
		i++
	}
	return b.String()
}
