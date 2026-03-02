package syntaxa

/*
TokenNestPair is one (token, nest ID) pair from a GConcat's adjacent GToken-then-GNest children.
*/
type TokenNestPair[TToken comparable] struct {
	Token  TToken
	NestID GrammarID
}

/*
GrammarConcatTokenNestPairs returns (token, nest GrammarID) for each adjacent GToken–GNest pair in a GConcat's children.

Returns nil for nil or non-GConcat nodes. Used by consumers that need to attach IDs or labels to these structural pairs.
*/
func GrammarConcatTokenNestPairs[TToken comparable](concat *Grammar[TToken]) []TokenNestPair[TToken] {
	if concat == nil || concat.Kind != GConcat || len(concat.Children) < 2 {
		return nil
	}
	var out []TokenNestPair[TToken]
	for i := 0; i < len(concat.Children)-1; i++ {
		curr := concat.Children[i]
		next := concat.Children[i+1]
		if curr != nil && next != nil && curr.Kind == GToken && next.Kind == GNest {
			out = append(out, TokenNestPair[TToken]{Token: curr.Token, NestID: next.GrammarID})
		}
	}
	return out
}
