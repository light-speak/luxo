package rpc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Frozen before the nil-result guard, for local comparisons only.
func beforeNilContextGuard(s *Server, ctx context.Context, token string) (context.Context, error) {
	if token == "" {
		return ctx, nil
	}
	if s.internalRequestContext == nil {
		return nil, fmt.Errorf("RPC bearer verification is not configured")
	}
	return s.internalRequestContext(ctx, token)
}

// Frozen after nil-result validation and before callback recovery.
func beforeAuthenticationRecovery(s *Server, ctx context.Context, token string) (context.Context, error) {
	if token == "" {
		return ctx, nil
	}
	if s.internalRequestContext == nil {
		return nil, fmt.Errorf("RPC bearer verification is not configured")
	}
	verified, err := s.internalRequestContext(ctx, token)
	if err == nil && verified == nil {
		return nil, fmt.Errorf("RPC bearer verifier returned no context")
	}
	return verified, err
}

var authContextSink context.Context
var authContextErrorSink error

func BenchmarkRPCNilContextGuard(b *testing.B) {
	ctx := context.Background()
	rejected := errors.New("invalid token")
	for _, sample := range []struct {
		name, token string
		verify      func(context.Context, string) (context.Context, error)
	}{
		{"NoToken", "", nil},
		{"Verified", "token", func(ctx context.Context, _ string) (context.Context, error) { return ctx, nil }},
		{"Rejected", "token", func(context.Context, string) (context.Context, error) { return nil, rejected }},
		{"Unconfigured", "token", nil},
	} {
		b.Run(sample.name, func(b *testing.B) {
			server := &Server{internalRequestContext: sample.verify}
			for _, version := range []struct {
				name string
				run  func() (context.Context, error)
			}{
				{"Before", func() (context.Context, error) { return beforeNilContextGuard(server, ctx, sample.token) }},
				{"GuardOnly", func() (context.Context, error) { return beforeAuthenticationRecovery(server, ctx, sample.token) }},
				{"After", func() (context.Context, error) { return server.requestContext(ctx, sample.token) }},
			} {
				b.Run(version.name, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						authContextSink, authContextErrorSink = version.run()
					}
					if sample.name == "Verified" || sample.name == "NoToken" {
						if authContextSink != ctx || authContextErrorSink != nil {
							b.Fatal("valid context changed")
						}
					} else if authContextErrorSink == nil {
						b.Fatal("rejection lost")
					}
				})
			}
		})
	}
}

var authEncodedSink []byte

func BenchmarkRPCAuthenticationErrorRecovery(b *testing.B) {
	for _, sample := range []struct{ name, message string }{
		{"Short", "invalid token"}, {"Long", strings.Repeat("x", 4096)},
	} {
		b.Run(sample.name, func(b *testing.B) {
			err := errors.New(sample.message)
			before := func() []byte { return encodeError(401, "Unauthorized", err.Error()) }
			after := func() []byte { return encodeAuthenticationError(err) }
			if !bytes.Equal(before(), after()) {
				b.Fatal("authentication response changed")
			}
			for _, version := range []struct {
				name string
				run  func() []byte
			}{{"Before", before}, {"After", after}} {
				b.Run(version.name, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						authEncodedSink = version.run()
					}
				})
			}
		})
	}
}
