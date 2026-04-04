package syntaxa

import (
	"lexarch"
)

func LineSpanFromByteOffsets(start, end int, source string, tabWidth int) (startLine, startColumn, endLine, endColumn int, ok bool) {
	if source == "" {
		return 0, 0, 0, 0, false
	}
	if tabWidth <= 0 {
		tabWidth = 4
	}

	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}

	span := lexarch.ByteSpan{
		Offset: uint32(start),
		Length: uint32(end - start),
	}
	pos := lexarch.LexerByteSpanToPosition(span, source, tabWidth)
	return pos.StartLine, pos.StartColumn, pos.EndLine, pos.EndColumn, true
}

/*
LSTNodeLineSpanFromSource maps one node’s merged byte span to line/column, using the node’s
diagnostic line cache when the byte span and tab width match the last fill.

For “nearest spanning ancestor” behavior use LSTNodeLineSpanForDiagnostics.
*/
func LSTNodeLineSpanFromSource[TNodeKind comparable](
	node *SyntaxaLSTNode[TNodeKind],
	source string,
	tabWidth int,
) (startLine, startColumn, endLine, endColumn int, ok bool) {
	return lstNodeDiagnosticLineSpanFromSource(node, source, tabWidth)
}
