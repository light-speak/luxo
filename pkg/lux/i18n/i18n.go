// Package i18n provides translation support for the Luxo runtime.
// Loads YAML translation files and substitutes ${var} templates.
package i18n

import (
	"fmt"
	"os"
	"path/filepath"
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
func (t *Translator) Translate(locale, key string, data map[string]any) string {
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
func substitute(tmpl string, data map[string]any) string {
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
