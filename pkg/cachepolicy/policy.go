// Package cachepolicy reads source-controlled cache contracts at build time.
// It has no backend dependencies and performs no reflective decoding.
package cachepolicy

import (
	"bytes"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"time"
	"unicode"
)

// Mode describes freshness behavior, not the deployment's storage backend.
type Mode string

const TTL Mode = "ttl"

// Scope describes which authorized callers may share a response.
type Scope string

const (
	Private Scope = "private"
	Public  Scope = "public"
)

// Policy is immutable after compilation. TTL bounds entry retention, not the
// age of the database snapshot; writes do not invalidate cached responses.
type Policy struct {
	Mode  Mode
	TTL   time.Duration
	Scope Scope
}

// Validate also checks contracts supplied directly by generated Go code.
func (p Policy) Validate() error {
	if p.Mode != TTL {
		return fmt.Errorf("unsupported cache mode %q: only explicit ttl is supported", p.Mode)
	}
	if p.Scope != Private && p.Scope != Public {
		return fmt.Errorf("scope must be private or public")
	}
	if p.TTL <= 0 {
		return fmt.Errorf("ttl must be explicitly positive")
	}
	return nil
}

// Catalog owns the validated policies. Lookup returns an independent value.
type Catalog struct {
	policies map[string]Policy
}

func (c *Catalog) Lookup(name string) (Policy, bool) {
	if c == nil {
		return Policy{}, false
	}
	policy, ok := c.policies[name]
	return policy, ok
}

// Parse rejects unknown fields, duplicate decoded names and trailing documents.
// A policy declaration never implies that a runtime supports that capability.
func Parse(raw []byte) (*Catalog, error) {
	decoder := jsontext.NewDecoder(bytes.NewReader(raw))
	catalog := &Catalog{}
	version := false
	err := visitObject(decoder, func(name string) error {
		switch name {
		case "version":
			value, err := decoder.ReadToken()
			if err != nil || value.Kind() != '0' || value.String() != "1" {
				return fmt.Errorf("cache policies require format version 1")
			}
			version = true
			return nil
		case "policies":
			return catalog.readPolicies(decoder)
		default:
			return fmt.Errorf("unknown cache catalog field %q", name)
		}
	})
	if err != nil {
		return nil, err
	}
	if _, err = decoder.ReadToken(); err != io.EOF {
		return nil, fmt.Errorf("unexpected data after cache catalog: %v", err)
	}
	if !version || catalog.policies == nil {
		return nil, fmt.Errorf("cache catalog requires version and policies")
	}
	return catalog, nil
}

func visitObject(decoder *jsontext.Decoder, visit func(string) error) error {
	start, err := decoder.ReadToken()
	if err != nil {
		return err
	}
	if start.Kind() != '{' {
		return fmt.Errorf("expected a JSON object")
	}
	for decoder.PeekKind() != '}' {
		name, err := decoder.ReadToken()
		if err != nil {
			return err
		}
		if err := visit(name.String()); err != nil {
			return err
		}
	}
	_, err = decoder.ReadToken()
	return err
}

func (c *Catalog) readPolicies(decoder *jsontext.Decoder) error {
	c.policies = make(map[string]Policy)
	return visitObject(decoder, func(name string) error {
		if name == "" {
			return fmt.Errorf("cache policy name must not be empty")
		}
		for _, char := range name {
			if unicode.IsSpace(char) || unicode.IsControl(char) {
				return fmt.Errorf("cache policy name %q contains whitespace or control characters", name)
			}
		}
		policy, err := readPolicy(decoder)
		if err != nil {
			return fmt.Errorf("cache policy %q: %w", name, err)
		}
		c.policies[name] = policy
		return nil
	})
}

func readPolicy(decoder *jsontext.Decoder) (Policy, error) {
	policy := Policy{Scope: Private}
	err := visitObject(decoder, func(name string) error {
		value, err := decoder.ReadToken()
		if err != nil {
			return err
		}
		return policy.readField(name, value)
	})
	if err != nil {
		return Policy{}, err
	}
	if err := policy.Validate(); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

func (p *Policy) readField(name string, value jsontext.Token) error {
	switch name {
	case "mode":
		if value.Kind() != '"' {
			return fmt.Errorf("mode must be ttl")
		}
		p.Mode = Mode(value.String())
	case "scope":
		if value.Kind() != '"' {
			return fmt.Errorf("scope must be private or public")
		}
		p.Scope = Scope(value.String())
	case "ttl":
		if value.Kind() != '0' && value.Kind() != '"' {
			return fmt.Errorf("ttl must be integer seconds or a duration string")
		}
		ttl, ok := ParseTTL(value.String(), value.Kind() == '"')
		if !ok {
			return fmt.Errorf("ttl must be a positive, non-overflowing duration or integer seconds")
		}
		p.TTL = ttl
	default:
		return fmt.Errorf("unknown policy field %q", name)
	}
	return nil
}
