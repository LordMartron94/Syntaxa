package syntaxa

import (
	"cmp"
	"fmt"
	"lexarch"
	"reflect"
)

type ObservationFormatter[TObservation cmp.Ordered] func(TObservation) string

type Lexeme[TObservation cmp.Ordered, TToken, TTokenRole comparable] struct {
	Token       TToken
	Role        TTokenRole
	Raw         []byte
	Start       int
	End         int
	TokenNumber int

	source       string
	tabWidth     int
	originalSpan lexarch.ByteSpan
}

func (l Lexeme[_, _, _]) FormatRawDiagnostic() string {
	if len(l.Raw) == 0 {
		return ""
	}

	return string(l.Raw)
}

func LexemeFromToken[TObservation cmp.Ordered, TToken, TTokenRole comparable](
	token *lexarch.Token,
	source string,
	tabWidth int,
	tokenNumber int,
) Lexeme[TObservation, TToken, TTokenRole] {
	var out Lexeme[TObservation, TToken, TTokenRole]
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
	out.Token = lexemeConvertTokenKind[TToken](token.Kind)
	out.Role = lexemeConvertTokenRole[TTokenRole](token.Role)
	out.source = source
	out.tabWidth = tabWidth
	out.originalSpan = token.Span
	return out
}

func LexemeLineSpan[TObservation cmp.Ordered, TToken, TTokenRole comparable](
	lexeme Lexeme[TObservation, TToken, TTokenRole],
) (startLine, startColumn, endLine, endColumn int) {
	start := int(lexeme.originalSpan.Offset)
	end := start + int(lexeme.originalSpan.Length)
	sl, sc, el, ec, ok := LineSpanFromByteOffsets(start, end, lexeme.source, lexeme.tabWidth)
	if !ok {
		return 0, 0, 0, 0
	}
	return sl, sc, el, ec
}

func LexemeStartLineColumn[TObservation cmp.Ordered, TToken, TTokenRole comparable](
	lexeme Lexeme[TObservation, TToken, TTokenRole],
) (line, column int) {
	sl, sc, _, _ := LexemeLineSpan(lexeme)
	return sl, sc
}

func LexemeEndLineColumn[TObservation cmp.Ordered, TToken, TTokenRole comparable](
	lexeme Lexeme[TObservation, TToken, TTokenRole],
) (line, column int) {
	_, _, el, ec := LexemeLineSpan(lexeme)
	return el, ec
}

func lexemeConvertTokenKind[TToken comparable](kind lexarch.TokenKind) TToken {
	var zero TToken
	target := reflect.TypeOf(zero)
	if target == nil {
		panic("syntaxa: invalid token type conversion target")
	}

	val := reflect.ValueOf(uint32(kind))
	if !val.Type().ConvertibleTo(target) {
		panic(fmt.Sprintf("syntaxa: token kind uint32 is not convertible to %s", target.String()))
	}

	return val.Convert(target).Interface().(TToken)
}

func lexemeConvertTokenRole[TTokenRole comparable](role lexarch.TokenRole) TTokenRole {
	var zero TTokenRole
	target := reflect.TypeOf(zero)
	if target == nil {
		panic("syntaxa: invalid token role conversion target")
	}

	val := reflect.ValueOf(uint32(role))
	if !val.Type().ConvertibleTo(target) {
		panic(fmt.Sprintf("syntaxa: token role uint32 is not convertible to %s", target.String()))
	}

	return val.Convert(target).Interface().(TTokenRole)
}
