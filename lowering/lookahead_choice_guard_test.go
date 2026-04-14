package lowering

import (
	"foundation/hash"
	"lexarch"
	"syntaxa"
	"testing"
)

func tokenHashUint64(t lexarch.TokenKind) uint64 {
	return uint64(t)
}

func nodeKindHashUint32(k uint32) uint64 {
	return uint64(k)
}

func TestLookaheadChoice_GuardSplitsSameFirstToken(t *testing.T) {
	path0 := syntaxa.NodePath("0.0")
	path1 := syntaxa.NodePath("0.1")
	key0 := syntaxa.NodeKeyFromPath(path0)
	key1 := syntaxa.NodeKeyFromPath(path1)

	g0 := &syntaxa.Grammar[lexarch.TokenKind, uint32]{Kind: syntaxa.GToken, Token: 10, NodePath: &path0}
	g1 := &syntaxa.Grammar[lexarch.TokenKind, uint32]{Kind: syntaxa.GToken, Token: 10, NodePath: &path1}

	pathChoice := syntaxa.NodePath("0")
	choice := &syntaxa.Grammar[lexarch.TokenKind, uint32]{
		Kind:     syntaxa.GChoice,
		NodePath: &pathChoice,
		Children: []*syntaxa.Grammar[lexarch.TokenKind, uint32]{g0, g1},
	}

	analysis := &syntaxa.GrammarAnalysis{
		Nullable: map[syntaxa.NodeKey]bool{key0: false, key1: false},
		First: map[syntaxa.NodeKey]syntaxa.TokenSet{
			key0: {10: struct{}{}},
			key1: {10: struct{}{}},
		},
		Follow: map[syntaxa.NodeKey]syntaxa.TokenSet{},
		ArmPredict: map[syntaxa.NodeKey]syntaxa.GuardedArm{
			key0: {
				First: syntaxa.TokenSet{10: struct{}{}},
				Guard: []syntaxa.Lookahead[lexarch.TokenKind]{
					{Offset: 0, Expected: 10},
					{Offset: 1, Expected: 20},
				},
			},
		},
	}

	ts, _ := lookahead(choice, nil, make(visiting), analysis)
	if len(ts) != 2 {
		t.Fatalf("expected 2 lookahead terminals, got %d", len(ts))
	}
	if ts[0].token != 10 || ts[1].token != 10 {
		t.Fatalf("expected both tokens 10, got %v %v", ts[0].token, ts[1].token)
	}
	hasGuard := false
	withoutGuard := false
	for _, x := range ts {
		if len(x.choiceGuard) > 0 {
			hasGuard = true
		} else {
			withoutGuard = true
		}
	}
	if !hasGuard || !withoutGuard {
		t.Fatalf("expected one guarded and one unguarded terminal, guards: %v / %v",
			ts[0].choiceGuard, ts[1].choiceGuard)
	}

	h := hash.XXH3HasherCreateWithSeed(0)
	k0 := terminalKey(ts[0], tokenHashUint64, nodeKindHashUint32, h)
	k1 := terminalKey(ts[1], tokenHashUint64, nodeKindHashUint32, h)
	if k0 == k1 {
		t.Fatal("expected distinct terminal keys for guarded vs unguarded arm")
	}
}

func TestShiftChoiceGuardAfterConsume_DropsPeek0WhenSatisfied(t *testing.T) {
	g := []syntaxa.Lookahead[lexarch.TokenKind]{
		{Offset: 0, Expected: 10},
		{Offset: 1, Expected: 20},
	}
	out := shiftChoiceGuardAfterConsume(g, 10)
	if len(out) != 1 || out[0].Offset != 0 || out[0].Expected != 20 {
		t.Fatalf("expected {{0,20}}, got %#v", out)
	}
}
