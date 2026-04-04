package syntaxa

import "lexarch"

/*
GuardMatchesLookahead returns true iff every constraint holds: for each entry,
peek(Offset).Token == Expected. Empty guard is vacuously true.

peek must use the same offset convention as SelectRuleContext.Peek (and PredictLookahead).
*/
func GuardMatchesLookahead(peek func(int) Lexeme, guard []Lookahead[lexarch.TokenKind]) bool {
	for _, l := range guard {
		if peek(l.Offset).Token != l.Expected {
			return false
		}
	}
	return true
}

/*
GuardsMutuallyExclusive reports whether two guard conjunctions cannot both be satisfied.

If either guard contradicts itself at the same offset (two different required tokens),
the function returns false (conservative: not treated as mutually exclusive).

Mutually exclusive means: there exists an offset where both guards fix a token and those
tokens differ.
*/
func GuardsMutuallyExclusive(a, b []Lookahead[lexarch.TokenKind]) bool {
	ma, badA := guardConstrainedTokens(a)
	mb, badB := guardConstrainedTokens(b)
	if badA || badB {
		return false
	}
	for o, ta := range ma {
		if tb, ok := mb[o]; ok && ta != tb {
			return true
		}
	}
	return false
}

func guardConstrainedTokens(g []Lookahead[lexarch.TokenKind]) (map[int]lexarch.TokenKind, bool) {
	m := make(map[int]lexarch.TokenKind)
	for _, x := range g {
		if prev, ok := m[x.Offset]; ok && prev != x.Expected {
			return nil, true
		}
		m[x.Offset] = x.Expected
	}
	return m, false
}
