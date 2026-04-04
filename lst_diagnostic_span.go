package syntaxa

/*
Diagnostic line/column are not authoritative on the LST: they are derived from byte offsets
and source text. SyntaxaLSTNode may hold an optional per-node cache (diagLineCache* fields)
filled by LSTNodeLineSpanFromSource / LSTNodeLineSpanForDiagnostics; it is invalidated when the
merged byte span changes or the node is marked dirty. The cache does not fingerprint source
text—if the buffer changes in place without span edits, clear caches or avoid reuse.
*/

func lstNodeInvalidateDiagnosticLineCache[TKind comparable](n *SyntaxaLSTNode[TKind]) {
	if n == nil {
		return
	}
	n.diagLineCacheValid = false
	n.diagLineCacheSL = 0
	n.diagLineCacheSC = 0
	n.diagLineCacheEL = 0
	n.diagLineCacheEC = 0
	n.diagLineCacheKeyS = 0
	n.diagLineCacheKeyE = 0
	n.diagLineCacheKeyTab = 0
}

func lstNodeDiagnosticLineSpanFromSource[TKind comparable](
	n *SyntaxaLSTNode[TKind],
	source string,
	tabWidth int,
) (sl, sc, el, ec int, ok bool) {
	if n == nil || !n.spanValid || source == "" {
		return 0, 0, 0, 0, false
	}
	if tabWidth <= 0 {
		tabWidth = 4
	}
	if n.diagLineCacheValid &&
		n.start == n.diagLineCacheKeyS &&
		n.end == n.diagLineCacheKeyE &&
		tabWidth == n.diagLineCacheKeyTab {
		return n.diagLineCacheSL, n.diagLineCacheSC, n.diagLineCacheEL, n.diagLineCacheEC, true
	}
	ssl, ssc, eel, eec, okb := LineSpanFromByteOffsets(n.start, n.end, source, tabWidth)
	if !okb || ssl < 1 || eel < 1 {
		return 0, 0, 0, 0, false
	}
	n.diagLineCacheSL = ssl
	n.diagLineCacheSC = ssc
	n.diagLineCacheEL = eel
	n.diagLineCacheEC = eec
	n.diagLineCacheKeyS = n.start
	n.diagLineCacheKeyE = n.end
	n.diagLineCacheKeyTab = tabWidth
	n.diagLineCacheValid = true
	return ssl, ssc, eel, eec, true
}

/*
LSTNodeHasMergedByteSpan reports whether n has a merged byte span from LSTEditor.ComputeSpans
or Freeze.
*/
func LSTNodeHasMergedByteSpan[TKind comparable](n *SyntaxaLSTNode[TKind]) bool {
	return n != nil && n.spanValid
}

/*
LSTNodeLineSpanForDiagnostics returns a 1-based line/column range for highlighting by mapping
the nearest ancestor’s merged byte span through source, using each node’s diagnostic line cache
when valid. tabWidth <= 0 defaults to 4.
*/
func LSTNodeLineSpanForDiagnostics[TKind comparable](
	n *SyntaxaLSTNode[TKind],
	source string,
	tabWidth int,
) (sl, sc, el, ec int, ok bool) {
	if n == nil || source == "" {
		return 0, 0, 0, 0, false
	}
	if tabWidth <= 0 {
		tabWidth = 4
	}
	for cur := n; cur != nil; cur = cur.Parent() {
		if ssl, ssc, eel, eec, okb := lstNodeDiagnosticLineSpanFromSource(cur, source, tabWidth); okb {
			return ssl, ssc, eel, eec, true
		}
	}
	return 0, 0, 0, 0, false
}
