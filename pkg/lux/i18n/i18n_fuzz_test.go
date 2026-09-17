package i18n

import "testing"

func FuzzSubstitute(f *testing.F) {
	for _, template := range []string{
		"hello", "${}", "${name}", "${name}${name}", "${name}-${missing}",
		"${", "${name", "$${name}", "${${name}}", "\xff${name}\x00", "数${name}值",
	} {
		f.Add(template, "数\x00\xff", "")
	}
	f.Fuzz(func(t *testing.T, template, name, empty string) {
		if len(template)+len(name)+len(empty) > 4096 {
			t.Skip()
		}
		values := Values{"name": name, "": empty}
		// The independent frozen formatter covers malformed templates as well
		// as replacement syntax. Only string values are compared here.
		want := substituteFormer(template, map[string]any{"name": name, "": empty})
		if got := substitute(template, values); got != want {
			t.Fatalf("template %q: got %q, want %q", template, got, want)
		}
	})
}
