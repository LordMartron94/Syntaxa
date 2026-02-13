package pratt

import (
	"cmp"
	"syntaxa"
)

type Ctx[TObs cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] = syntaxa.ExecRuleContext[TObs, TToken, TTokenRole, TLexerState, TNodeKind]

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
type PrefixParselet[TObs cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable] func(
	ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TNodeKind],
) *syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TNodeKind]

/*
InfixParselet parses infix operators.

The parselet must:
  - consume the operator token
  - parse the RHS using rbp
*/
type InfixParselet[TObs cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable] func(
	ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TNodeKind],
	left *syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TNodeKind],
	rbp int,
) *syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TNodeKind]

/*
PostfixParselet parses postfix operators.

Examples:
  - x++
  - function calls
  - indexing
  - member access
*/
type PostfixParselet[TObs cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable] func(
	ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TNodeKind],
	left *syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TNodeKind],
) *syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TNodeKind]

// =============================================================
// INTERNAL ENTRIES
// =============================================================

type infixEntry[TObs cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable] struct {
	lbp   int
	rbp   int
	assoc Associativity
	parse InfixParselet[TObs, TToken, TTokenRole, TNodeKind, TLexerState]
}

type postfixEntry[TObs cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable] struct {
	bp    int
	parse PostfixParselet[TObs, TToken, TTokenRole, TNodeKind, TLexerState]
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
type PrattParser[TObs cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable] struct {
	prefix  map[TToken]PrefixParselet[TObs, TToken, TTokenRole, TNodeKind, TLexerState]
	infix   map[TToken]infixEntry[TObs, TToken, TTokenRole, TNodeKind, TLexerState]
	postfix map[TToken]postfixEntry[TObs, TToken, TTokenRole, TNodeKind, TLexerState]
}

// =============================================================
// CONSTRUCTION
// =============================================================

func PrattParserCreate[TObs cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable]() *PrattParser[TObs, TToken, TTokenRole, TNodeKind, TLexerState] {
	return &PrattParser[TObs, TToken, TTokenRole, TNodeKind, TLexerState]{
		prefix:  make(map[TToken]PrefixParselet[TObs, TToken, TTokenRole, TNodeKind, TLexerState]),
		infix:   make(map[TToken]infixEntry[TObs, TToken, TTokenRole, TNodeKind, TLexerState]),
		postfix: make(map[TToken]postfixEntry[TObs, TToken, TTokenRole, TNodeKind, TLexerState]),
	}
}

// =============================================================
// REGISTRATION API
// =============================================================

/*
RegisterPrefix registers a prefix parselet.
*/
func (p *PrattParser[TObs, TToken, TTokenRole, TNodeKind, TLexerState]) RegisterPrefix(
	token TToken,
	fn PrefixParselet[TObs, TToken, TTokenRole, TNodeKind, TLexerState],
) {
	p.prefix[token] = fn
}

/*
RegisterInfix registers an infix operator with precedence
and associativity.
*/
func (p *PrattParser[TObs, TToken, TTokenRole, TNodeKind, TLexerState]) RegisterInfix(
	token TToken,
	precedence int,
	assoc Associativity,
	fn InfixParselet[TObs, TToken, TTokenRole, TNodeKind, TLexerState],
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

	p.infix[token] = infixEntry[TObs, TToken, TTokenRole, TNodeKind, TLexerState]{
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
func (p *PrattParser[TObs, TToken, TTokenRole, TNodeKind, TLexerState]) RegisterPostfix(
	token TToken,
	precedence int,
	fn PostfixParselet[TObs, TToken, TTokenRole, TNodeKind, TLexerState],
) {
	p.postfix[token] = postfixEntry[TObs, TToken, TTokenRole, TNodeKind, TLexerState]{
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
func (p *PrattParser[TObs, TToken, TTokenRole, TNodeKind, TLexerState]) ParseExpr(
	ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TNodeKind],
	minBP int,
) *syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TNodeKind] {

	lex := ctx.Peek(0)

	nud, ok := p.prefix[lex.Token]
	if !ok {
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
