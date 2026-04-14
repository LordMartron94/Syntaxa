package lowering

import (
	"lexarch"
	"syntaxa"
	"testing"
)

func TestPeekAfterMatchFromAdvTerminals_IncludesNonZeroOffsets(t *testing.T) {
	g0 := []syntaxa.Lookahead[lexarch.TokenKind]{
		{Offset: 0, Expected: 10},
		{Offset: 1, Expected: 20},
	}
	adv := []gTerminal[uint32]{
		{token: 1, choiceGuard: g0},
	}
	got := peekAfterMatchFromAdvTerminals(adv)
	if len(got) != 2 || got[0].Offset != 0 || got[1].Offset != 1 {
		t.Fatalf("expected full guard {{0,10},{1,20}}, got %#v", got)
	}
}
