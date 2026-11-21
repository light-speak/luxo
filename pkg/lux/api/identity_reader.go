package api

import "github.com/light-speak/luxo/pkg/lux/auth"

// IdentityReader tracks only claims actually read by a generated condition.
// Keep one reader per evaluation so invalid values cannot become valid through
// negation, while Go's normal short-circuit evaluation remains intact.
type IdentityReader struct {
	claims  auth.Claims
	invalid bool
}

func NewIdentityReader(identity any) IdentityReader {
	switch value := identity.(type) {
	case auth.Claims:
		return IdentityReader{claims: value}
	case *auth.Claims:
		if value != nil {
			return IdentityReader{claims: *value}
		}
	}
	return IdentityReader{}
}

// NewIdentityReaderForSubject exposes claims only for the expected subject.
// Unread claims do not invalidate a condition's normal short-circuit result.
func NewIdentityReaderForSubject(identity any, subject string) IdentityReader {
	reader := NewIdentityReader(identity)
	if subject == "" || reader.claims.Subject() != subject {
		reader.claims = auth.Claims{}
	}
	return reader
}

func (r *IdentityReader) String(key string) string {
	value, valid := r.claims.Get(key).String()
	r.invalid = r.invalid || !valid
	return value
}

func (r *IdentityReader) Int(key string) int64 {
	value, valid := r.claims.Get(key).Int64()
	r.invalid = r.invalid || !valid
	return value
}

func (r *IdentityReader) Bool(key string) bool {
	value, valid := r.claims.Get(key).Bool()
	r.invalid = r.invalid || !valid
	return value
}

func (r *IdentityReader) Float(key string) float64 {
	value, valid := r.claims.Get(key).Float64()
	r.invalid = r.invalid || !valid
	return value
}

// Allowed observes reads performed while evaluating result. A pointer receiver
// is required: copying the reader before evaluating the argument would lose them.
func (r *IdentityReader) Allowed(result bool) bool { return !r.invalid && result }

// ValidatedString retains the generated schema check without changing the value.
// Invalid reads remain invalid through negation and later valid reads.
func (r *IdentityReader) ValidatedString(value string, valid bool) string {
	r.invalid = r.invalid || !valid
	return value
}
