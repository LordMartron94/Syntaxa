package syntaxa

import (
	"lexarch"
	"testing"
)

func TestGuardsMutuallyExclusive(t *testing.T) {
	a := []Lookahead[lexarch.TokenKind]{{Offset: 1, Expected: 10}}
	b := []Lookahead[lexarch.TokenKind]{{Offset: 1, Expected: 11}}
	if !GuardsMutuallyExclusive(a, b) {
		t.Fatal("expected exclusive at same offset with different tokens")
	}
	if GuardsMutuallyExclusive(a, a) {
		t.Fatal("identical guards are not mutually exclusive")
	}
	if GuardsMutuallyExclusive(a, nil) {
		t.Fatal("unconstrained branch should not be exclusive with constrained")
	}
}

func TestGuardMatchesLookahead(t *testing.T) {
	stream := []Lexeme{
		{Token: 1},
		{Token: 2},
		{Token: 3},
	}
	peek := func(n int) Lexeme {
		if n < 0 || n >= len(stream) {
			return Lexeme{}
		}
		return stream[n]
	}
	g := []Lookahead[lexarch.TokenKind]{{Offset: 1, Expected: 2}}
	if !GuardMatchesLookahead(peek, g) {
		t.Fatal("expected match")
	}
}
