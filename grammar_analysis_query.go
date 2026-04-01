package syntaxa

// ============================================================
// ANALYSIS QUERY HELPERS
// ============================================================
//
// Query existing GrammarAnalysis by node. Nodes must have NodePath set
// (e.g. after ProducePackage or FinalizeNodePaths).

/*
GrammarAnalysisNullable returns whether the given node can derive the empty string.

Caller must ensure node != nil and node.NodePath != nil (e.g. node is from a tree
that was used in ProducePackage or had FinalizeNodePaths called).
*/
func GrammarAnalysisNullable[TToken, TNodeKind comparable](
	analysis *GrammarAnalysis,
	node *Grammar[TToken, TNodeKind],
) bool {
	if analysis == nil || node == nil || node.NodePath == nil {
		return false
	}
	key := NodeKeyFromPath(*node.NodePath)
	return analysis.Nullable[key]
}

/*
GrammarAnalysisFirst returns the First set for the given node.

Returns a non-nil set (empty if the node is not in the analysis or has no first tokens).
Caller must ensure node != nil and node.NodePath != nil.
*/
func GrammarAnalysisFirst[TToken, TNodeKind comparable](
	analysis *GrammarAnalysis,
	node *Grammar[TToken, TNodeKind],
) TokenSet {
	if analysis == nil || node == nil || node.NodePath == nil {
		return make(TokenSet)
	}
	key := NodeKeyFromPath(*node.NodePath)
	set := analysis.First[key]
	if set == nil {
		return make(TokenSet)
	}
	return set
}

/*
GrammarAnalysisFirstOfSuffix returns the First set of the concat suffix from start.

Union of First(concatNode.Children[j]) for j from start until the first non-nullable
child (or end of children). Used for lookahead over a suffix of a sequence.
Returns non-nil set. concatNode and its children must have NodePath set.
*/
func GrammarAnalysisFirstOfSuffix[TToken, TNodeKind comparable](
	analysis *GrammarAnalysis,
	concatNode *Grammar[TToken, TNodeKind],
	start int,
) TokenSet {
	out := make(TokenSet)
	if analysis == nil || concatNode == nil || start < 0 || start >= len(concatNode.Children) {
		return out
	}
	for j := start; j < len(concatNode.Children); j++ {
		child := concatNode.Children[j]
		if child == nil {
			continue
		}
		mergeInto(out, GrammarAnalysisFirst(analysis, child))
		if !GrammarAnalysisNullable(analysis, child) {
			break
		}
	}
	return out
}
