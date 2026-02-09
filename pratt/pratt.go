package pratt

import (
	"cmp"
	"fmt"
	"syntaxa"
)

// =============================================================
// ASSOCIATIVITY
// =============================================================

/*
Associativity defines how operators of equal precedence bind.
*/
type Associativity uint8

const (
	Left Associativity = iota
	Right
	NonAssoc
)

// =============================================================
// PARSELET TYPES
// =============================================================

/*
PrefixParselet parses atomic and prefix expressions.

Examples:
  - literals
  - identifiers
  - prefix operators (-x, !x)
  - parenthesized expressions
*/
type PrefixParselet[TObs cmp.Ordered, TToken, TNodeKind, TLexerState comparable] func(
	ctx syntaxa.RuleContext[TObs, TToken, TLexerState, TNodeKind],
) *syntaxa.SyntaxaASTNode[TObs, TToken, TNodeKind]

/*
InfixParselet parses infix operators.

The parselet must:
  - consume the operator token
  - parse the RHS using rbp
*/
type InfixParselet[TObs cmp.Ordered, TToken, TNodeKind, TLexerState comparable] func(
	ctx syntaxa.RuleContext[TObs, TToken, TLexerState, TNodeKind],
	left *syntaxa.SyntaxaASTNode[TObs, TToken, TNodeKind],
	rbp int,
) *syntaxa.SyntaxaASTNode[TObs, TToken, TNodeKind]

/*
PostfixParselet parses postfix operators.

Examples:
  - x++
  - function calls
  - indexing
  - member access
*/
type PostfixParselet[TObs cmp.Ordered, TToken, TNodeKind, TLexerState comparable] func(
	ctx syntaxa.RuleContext[TObs, TToken, TLexerState, TNodeKind],
	left *syntaxa.SyntaxaASTNode[TObs, TToken, TNodeKind],
) *syntaxa.SyntaxaASTNode[TObs, TToken, TNodeKind]

// =============================================================
// INTERNAL ENTRIES
// =============================================================

type infixEntry[TObs cmp.Ordered, TToken, TNodeKind, TLexerState comparable] struct {
	lbp   int
	rbp   int
	assoc Associativity
	parse InfixParselet[TObs, TToken, TNodeKind, TLexerState]
}

type postfixEntry[TObs cmp.Ordered, TToken, TNodeKind, TLexerState comparable] struct {
	bp    int
	parse PostfixParselet[TObs, TToken, TNodeKind, TLexerState]
}

// =============================================================
// PRATT PARSER
// =============================================================

/*
PrattParser implements a full Pratt-style expression parser.

Supported:
  - prefix operators
  - infix operators (with associativity)
  - postfix operators

It is expression-only and integrates directly into
recursive descent grammars.
*/
type PrattParser[TObs cmp.Ordered, TToken, TNodeKind, TLexerState comparable] struct {
	prefix  map[TToken]PrefixParselet[TObs, TToken, TNodeKind, TLexerState]
	infix   map[TToken]infixEntry[TObs, TToken, TNodeKind, TLexerState]
	postfix map[TToken]postfixEntry[TObs, TToken, TNodeKind, TLexerState]
}

// =============================================================
// CONSTRUCTION
// =============================================================

func PrattParserCreate[TObs cmp.Ordered, TToken, TNodeKind, TLexerState comparable]() *PrattParser[TObs, TToken, TNodeKind, TLexerState] {
	return &PrattParser[TObs, TToken, TNodeKind, TLexerState]{
		prefix:  make(map[TToken]PrefixParselet[TObs, TToken, TNodeKind, TLexerState]),
		infix:   make(map[TToken]infixEntry[TObs, TToken, TNodeKind, TLexerState]),
		postfix: make(map[TToken]postfixEntry[TObs, TToken, TNodeKind, TLexerState]),
	}
}

// =============================================================
// REGISTRATION API
// =============================================================

/*
RegisterPrefix registers a prefix parselet.
*/
func (p *PrattParser[TObs, TToken, TNodeKind, TLexerState]) RegisterPrefix(
	token TToken,
	fn PrefixParselet[TObs, TToken, TNodeKind, TLexerState],
) {
	p.prefix[token] = fn
}

/*
RegisterInfix registers an infix operator with precedence
and associativity.
*/
func (p *PrattParser[TObs, TToken, TNodeKind, TLexerState]) RegisterInfix(
	token TToken,
	precedence int,
	assoc Associativity,
	fn InfixParselet[TObs, TToken, TNodeKind, TLexerState],
) {

	lbp := precedence
	rbp := precedence

	switch assoc {
	case Left:
		rbp = precedence + 1
	case Right:
		// rbp stays equal
	case NonAssoc:
		rbp = precedence + 1
	}

	p.infix[token] = infixEntry[TObs, TToken, TNodeKind, TLexerState]{
		lbp:   lbp,
		rbp:   rbp,
		assoc: assoc,
		parse: fn,
	}
}

/*
RegisterPostfix registers a postfix operator.

Postfix operators bind with a single binding power.
*/
func (p *PrattParser[TObs, TToken, TNodeKind, TLexerState]) RegisterPostfix(
	token TToken,
	precedence int,
	fn PostfixParselet[TObs, TToken, TNodeKind, TLexerState],
) {
	p.postfix[token] = postfixEntry[TObs, TToken, TNodeKind, TLexerState]{
		bp:    precedence,
		parse: fn,
	}
}

// =============================================================
// PARSING ENGINE
// =============================================================

/*
ParseExpr parses an expression with minimum binding power.

Correctly handles:
  - prefix
  - postfix
  - infix
  - associativity
*/
func (p *PrattParser[TObs, TToken, TNodeKind, TLexerState]) ParseExpr(
	ctx syntaxa.RuleContext[TObs, TToken, TLexerState, TNodeKind],
	minBP int,
) *syntaxa.SyntaxaASTNode[TObs, TToken, TNodeKind] {

	lex := ctx.Peek(0)
	nud := p.prefix[lex.Token]

	if nud == nil {
		ctx.Report(
			lex.StartLine,
			lex.StartColumn,
			fmt.Sprintf("unexpected token in expression: %v", lex.Token),
		)
		return nil
	}

	left := nud(ctx)

	for {
		look := ctx.Peek(0)

		// ---------- POSTFIX ----------

		if post, ok := p.postfix[look.Token]; ok {
			if post.bp < minBP {
				break
			}
			left = post.parse(ctx, left)
			continue
		}

		// ---------- INFIX ----------

		entry, ok := p.infix[look.Token]
		if !ok || entry.lbp < minBP {
			break
		}

		// Enforce non-associativity
		if entry.assoc == NonAssoc && entry.lbp == minBP {
			ctx.Report(
				look.StartLine,
				look.StartColumn,
				"non-associative operator used repeatedly",
			)
			return left
		}

		left = entry.parse(ctx, left, entry.rbp)
	}

	return left
}
