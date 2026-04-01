package syntaxa

/*
LSTNodeUsableLineSpan reports whether n has a non-empty merged line/column range suitable
for diagnostics (1-based lines). Nodes with no attached source (e.g. only virtual tokens)
leave spanValid false after span computation.
*/
func LSTNodeUsableLineSpan[TKind comparable](n *SyntaxaLSTNode[TKind]) (sl, sc, el, ec int, ok bool) {
	if n == nil || !n.spanValid {
		return 0, 0, 0, 0, false
	}
	sl = n.startLine
	sc = n.startColumn
	el = n.endLine
	ec = n.endColumn
	if sl < 1 || el < 1 {
		return 0, 0, 0, 0, false
	}
	return sl, sc, el, ec, true
}

/*
LSTNodeLineSpanForDiagnostics returns a line/column range for highlighting: n if usable,
otherwise the nearest ancestor with a usable span.
*/
func LSTNodeLineSpanForDiagnostics[TKind comparable](n *SyntaxaLSTNode[TKind]) (sl, sc, el, ec int, ok bool) {
	for cur := n; cur != nil; cur = cur.Parent() {
		if sl, sc, el, ec, ok := LSTNodeUsableLineSpan(cur); ok {
			return sl, sc, el, ec, true
		}
	}
	return 0, 0, 0, 0, false
}
