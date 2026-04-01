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
	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int
	TokenNumber int
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

	pos := lexarch.LexerByteSpanToPosition(token.Span, source, tabWidth)
	out.Start = start
	out.End = end
	out.StartLine = pos.StartLine
	out.StartColumn = pos.StartColumn
	out.EndLine = pos.EndLine
	out.EndColumn = pos.EndColumn
	out.TokenNumber = tokenNumber
	out.Raw = []byte(source[start:end])
	out.Token = lexemeConvertTokenKind[TToken](token.Kind)
	out.Role = lexemeConvertTokenRole[TTokenRole](token.Role)
	return out
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
