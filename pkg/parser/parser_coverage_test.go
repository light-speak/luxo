package parser

import (
	"testing"

	"github.com/light-speak/luxo/pkg/token"
)

// ========== DocComment then EOF triggers check(token.EOF) in Parse switch ==========

func TestCoverDocCommentThenEOF(t *testing.T) {
	// consumeDoc advances past DocComment, then the switch in Parse
	// hits the check(token.EOF) case and returns directly.
	tokens := []token.Token{
		{Type: token.DocComment, Val: "some doc"},
		{Type: token.EOF},
	}
	p := New(tokens)
	file, _ := p.Parse("test.luxo")
	if file == nil {
		t.Fatal("expected non-nil file")
	}
}
