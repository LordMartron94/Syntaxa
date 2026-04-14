package lowering

import (
	"testing"
)

func TestCountTermsPerToken(t *testing.T) {
	terms := []gTerminal[uint32]{
		{token: 1},
		{token: 2},
		{token: 1},
	}
	m := countTermsPerToken[uint32](terms)
	if m[1] != 2 || m[2] != 1 {
		t.Fatalf("got %#v", m)
	}
}

func TestCountTermsPerToken_Empty(t *testing.T) {
	if len(countTermsPerToken[uint32](nil)) != 0 {
		t.Fatal("expected empty map")
	}
}
