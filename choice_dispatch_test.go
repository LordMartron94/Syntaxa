package syntaxa

import (
	"lexarch"
	"testing"
)

func TestChoiceCandidateIndicesFromAnalysis_NilAnalysis(t *testing.T) {
	g := &Grammar[lexarch.TokenKind, uint32]{Kind: GToken, Token: 1}
	p := NodePath("0")
	g.NodePath = &p

	indices, mode := ChoiceCandidateIndicesFromAnalysis[uint32](nil, []*Grammar[lexarch.TokenKind, uint32]{g}, func(int) Lexeme { return Lexeme{Token: 1} })
	if mode != ChoiceDispatchFallback {
		t.Fatalf("expected ChoiceDispatchFallback, got %v", mode)
	}
	if indices != nil {
		t.Fatalf("expected nil indices")
	}
}

func TestChoiceCandidateIndicesFromAnalysis_GuardFiltersSecondToken(t *testing.T) {
	// Two arms: token A at peek(0), one guarded with peek(1)==B, one unguarded.
	path0 := NodePath("0.0")
	path1 := NodePath("0.1")
	key0 := NodeKeyFromPath(path0)
	key1 := NodeKeyFromPath(path1)

	g0 := &Grammar[lexarch.TokenKind, uint32]{Kind: GToken, Token: 10, NodePath: &path0}
	g1 := &Grammar[lexarch.TokenKind, uint32]{Kind: GToken, Token: 10, NodePath: &path1}

	analysis := &GrammarAnalysis{
		Nullable: map[NodeKey]bool{key0: false, key1: false},
		First: map[NodeKey]TokenSet{
			key0: {10: struct{}{}},
			key1: {10: struct{}{}},
		},
		Follow: map[NodeKey]TokenSet{},
		ArmPredict: map[NodeKey]GuardedArm{
			key0: {
				First: TokenSet{10: struct{}{}},
				Guard: []Lookahead[lexarch.TokenKind]{{Offset: 0, Expected: 10}, {Offset: 1, Expected: 20}},
			},
		},
	}

	peek10_99 := func(n int) Lexeme {
		switch n {
		case 0:
			return Lexeme{Token: 10}
		case 1:
			return Lexeme{Token: 99}
		default:
			return Lexeme{}
		}
	}
	indices, mode := ChoiceCandidateIndicesFromAnalysis(analysis, []*Grammar[lexarch.TokenKind, uint32]{g0, g1}, peek10_99)
	if mode != ChoiceDispatchCandidates {
		t.Fatalf("expected ChoiceDispatchCandidates, got %v", mode)
	}
	// Guard fails for g0 (peek(1)!=20); only g1
	if len(indices) != 1 || indices[0] != 1 {
		t.Fatalf("expected [1], got %v", indices)
	}

	peek10_20 := func(n int) Lexeme {
		switch n {
		case 0:
			return Lexeme{Token: 10}
		case 1:
			return Lexeme{Token: 20}
		default:
			return Lexeme{}
		}
	}
	indices2, mode2 := ChoiceCandidateIndicesFromAnalysis(analysis, []*Grammar[lexarch.TokenKind, uint32]{g0, g1}, peek10_20)
	if mode2 != ChoiceDispatchCandidates {
		t.Fatalf("expected ChoiceDispatchCandidates, got %v", mode2)
	}
	if len(indices2) != 2 {
		t.Fatalf("expected both arms for 10,20 prefix, got %v", indices2)
	}
}
