// Package i18n provides translation support for the Luxo runtime.
// Loads YAML translation files and substitutes ${var} templates.
package i18n

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Translator holds all loaded translations, keyed by locale.
type Translator struct {
	mu       sync.RWMutex
	locales  map[string]map[string]string // locale → flat key → template
	fallback string                       // default locale
}

// New creates a Translator with the given fallback locale.
func New(fallback string) *Translator {
	return &Translator{
		locales:  make(map[string]map[string]string),
		fallback: fallback,
	}
}

// LoadDir loads all .yaml/.yml files from a directory.
// Filename convention: en.yaml, zh.yaml — filename (minus ext) = locale.
func (t *Translator) LoadDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := filepath.Ext(name)
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		locale := strings.TrimSuffix(name, ext)
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if err := t.loadYAML(locale, data); err != nil {
			return fmt.Errorf("i18n: %s: %w", name, err)
		}
	}
	return nil
}

// loadYAML parses YAML bytes and flattens into dot-separated keys.
func (t *Translator) loadYAML(locale string, data []byte) error {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return err
	}
	flat := make(map[string]string)
	flatten("", raw, flat)
	t.mu.Lock()
	t.locales[locale] = flat
	t.mu.Unlock()
	return nil
}

// flatten recursively flattens nested maps into dot-separated keys.
func flatten(prefix string, m map[string]any, out map[string]string) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch val := v.(type) {
		case map[string]any:
			flatten(key, val, out)
		case string:
			out[key] = val
		}
	}
}

// Translate looks up a message key for the given locale and substitutes
// template variables from data. Fallback chain: locale → fallback → raw key.
func (t *Translator) Translate(locale, key string, data Data) string {
	tmpl := t.lookup(locale, key)
	if tmpl == "" {
		return key
	}
	return substitute(tmpl, data)
}

// lookup finds the template string under RLock, then releases immediately.
func (t *Translator) lookup(locale, key string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if msgs, ok := t.locales[locale]; ok {
		if tmpl, ok := msgs[key]; ok {
			return tmpl
		}
	}
	if locale != t.fallback {
		if msgs, ok := t.locales[t.fallback]; ok {
			if tmpl, ok := msgs[key]; ok {
				return tmpl
			}
		}
	}
	return ""
}

// substitute replaces ${varName} with values from data.
func substitute(tmpl string, data Data) string {
	if data == nil || !strings.Contains(tmpl, "${") {
		return tmpl
	}
	buf := interpolationBuffers.Get().(*interpolationBuffer)
	b, changed := appendSubstitution(slices.Grow(buf.bytes[:0], len(tmpl)), tmpl, data)
	result := tmpl
	if changed {
		result = string(b)
	}
	buf.bytes = b
	buf.reset()
	interpolationBuffers.Put(buf)
	return result
}

// appendSubstitution visits each placeholder once. A missing field preserves
// its original text, even if an implementation appended before returning false.
func appendSubstitution(b []byte, tmpl string, data Data) ([]byte, bool) {
	changed := false
	for i := 0; i < len(tmpl); {
		if i+1 < len(tmpl) && tmpl[i] == '$' && tmpl[i+1] == '{' {
			end := strings.IndexByte(tmpl[i+2:], '}')
			if end < 0 {
				return append(b, tmpl[i:]...), changed
			}
			end += i + 2
			prefix := len(b)
			var found bool
			b, found = data.AppendText(b, tmpl[i+2:end])
			changed = changed || found
			if !found {
				clear(b[prefix:])
				b = append(b[:prefix], tmpl[i:end+1]...)
			}
			i = end + 1
			continue
		}
		start := i
		i++
		for i < len(tmpl) && tmpl[i] != '$' {
			i++
		}
		b = append(b, tmpl[start:i]...)
	}
	return b, changed
}

const maxInterpolationCapacity = 4096

type interpolationBuffer struct{ bytes []byte }

var interpolationBuffers = sync.Pool{
	New: func() any { return new(interpolationBuffer) },
}

// reset prevents oversized or sensitive error data from staying in the pool.
func (b *interpolationBuffer) reset() {
	if cap(b.bytes) > maxInterpolationCapacity {
		b.bytes = nil
		return
	}
	clear(b.bytes)
	b.bytes = b.bytes[:0]
}

// Data supplies only the text of a requested field, without building a map or
// using fmt to inspect arbitrary runtime values.
// AppendText must preserve the prefix and must not retain dst after returning.
type Data interface {
	AppendText(dst []byte, field string) ([]byte, bool)
}

// Values is a convenience adapter for already-formatted translation strings.
type Values map[string]string

func (v Values) AppendText(dst []byte, field string) ([]byte, bool) {
	value, found := v[field]
	return append(dst, value...), found
}

// ParseAcceptLanguage extracts the preferred locale from an Accept-Language header.
// Returns the base language tag: "zh-CN,en;q=0.8" → "zh".
func ParseAcceptLanguage(header string) string {
	if header == "" {
		return ""
	}
	tag := header
	if idx := strings.IndexByte(header, ','); idx >= 0 {
		tag = header[:idx]
	}
	if idx := strings.IndexByte(tag, ';'); idx >= 0 {
		tag = tag[:idx]
	}
	tag = strings.TrimSpace(tag)
	if idx := strings.IndexByte(tag, '-'); idx >= 0 {
		tag = tag[:idx]
	}
	return strings.ToLower(tag)
}
