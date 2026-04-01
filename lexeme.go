package syntaxa

import (
	"lexarch"
)

type ObservationFormatter func(rune) string

type Lexeme struct {
	Token       lexarch.TokenKind
	Role        lexarch.TokenRole
	Raw         []byte
	Start       int
	End         int
	TokenNumber int

	source       string
	tabWidth     int
	originalSpan lexarch.ByteSpan
}

func (l Lexeme) FormatRawDiagnostic() string {
	if len(l.Raw) == 0 {
		return ""
	}

	return string(l.Raw)
}

func LexemeFromToken(
	token *lexarch.Token,
	source string,
	tabWidth int,
	tokenNumber int,
) Lexeme {
	var out Lexeme
	if token == nil {
		return out
	}

	start := int(token.Span.Offset)
	end := start + int(token.Span.Length)
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if end > len(source) {
		end = len(source)
	}

	out.Start = start
	out.End = end
	out.TokenNumber = tokenNumber
	out.Raw = []byte(source[start:end])
	out.Token = token.Kind
	out.Role = token.Role
	out.source = source
	out.tabWidth = tabWidth
	out.originalSpan = token.Span
	return out
}

func LexemeLineSpan(
	lexeme Lexeme,
) (startLine, startColumn, endLine, endColumn int) {
	start := int(lexeme.originalSpan.Offset)
	end := start + int(lexeme.originalSpan.Length)
	sl, sc, el, ec, ok := LineSpanFromByteOffsets(start, end, lexeme.source, lexeme.tabWidth)
	if !ok {
		return 0, 0, 0, 0
	}
	return sl, sc, el, ec
}

func LexemeStartLineColumn(
	lexeme Lexeme,
) (line, column int) {
	sl, sc, _, _ := LexemeLineSpan(lexeme)
	return sl, sc
}

func LexemeEndLineColumn(
	lexeme Lexeme,
) (line, column int) {
	_, _, el, ec := LexemeLineSpan(lexeme)
	return el, ec
}
