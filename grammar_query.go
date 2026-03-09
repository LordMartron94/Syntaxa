package syntaxa

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
