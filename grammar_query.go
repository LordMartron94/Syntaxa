package syntaxa

/*
TokenNestPair is one (token, nest ID) pair from a GConcat's adjacent GToken-then-GNest children.
*/
type TokenNestPair[TToken comparable] struct {
	Token  TToken
	NestID GrammarLabel
}

/*
GrammarConcatTokenNestPairs returns (token, nest GrammarLabel) for each adjacent GToken–GNest pair in a GConcat's children.

Returns nil for nil or non-GConcat nodes. Used by consumers that need to attach IDs or labels to these structural pairs.
*/
func GrammarConcatTokenNestPairs[TToken, TNodeKind comparable](concat *Grammar[TToken, TNodeKind]) []TokenNestPair[TToken] {
	return GrammarSequenceTokenNestPairs(nil, concat)
}

/*
GrammarSequenceTokenNestPairs returns (token, nest) pairs for each adjacent prev–next in a sequence where next is a nest:
for each token in First(prev), adds (token, next.GrammarLabel). Uses analysis when non-nil; when nil, only GToken prev is considered (one pair per GToken–GNest).
Works for GConcat; other node kinds return nil. Enables sequence triggers for any prev that has a First set (e.g. GOptional, GChoice).
*/
func GrammarSequenceTokenNestPairs[TToken, TNodeKind comparable](analysis *GrammarAnalysis[TToken], node *Grammar[TToken, TNodeKind]) []TokenNestPair[TToken] {
	if node == nil || node.Kind != GConcat || len(node.Children) < 2 {
		return nil
	}
	var out []TokenNestPair[TToken]
	for i := 0; i < len(node.Children)-1; i++ {
		prev := node.Children[i]
		next := node.Children[i+1]
		if prev == nil || next == nil || next.Kind != GNest {
			continue
		}
		nestID := next.GrammarLabel
		if analysis != nil && prev.NodePath != nil {
			first := GrammarAnalysisFirst(analysis, prev)
			for tok := range first {
				out = append(out, TokenNestPair[TToken]{Token: tok, NestID: nestID})
			}
		} else if prev.Kind == GToken {
			out = append(out, TokenNestPair[TToken]{Token: prev.Token, NestID: nestID})
		}
	}
	return out
}

/*
GrammarNestBody returns the body grammar of a GNest (the content between open and close).

Returns nil if nest is nil, not a GNest, or has no children. Keeps nest-body semantics in one place.
*/
func GrammarNestBody[TToken, TNodeKind comparable](nest *Grammar[TToken, TNodeKind]) *Grammar[TToken, TNodeKind] {
	if nest == nil || nest.Kind != GNest || len(nest.Children) == 0 {
		return nil
	}
	return nest.Children[0]
}
