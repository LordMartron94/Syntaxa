package rd

import (
	"cmp"
	"syntaxa"
)

// =============================================================
// LOCAL TYPE ALIASES (PREVENT GENERIC DRIFT)
// =============================================================

/*
Ctx is a package-local alias for syntaxa.RuleContext.

It fixes the canonical generic ordering used by all recursive
descent combinators in this package and prevents accidental
permutation of type parameters across files.

Canonical order (must match syntaxa.RuleContext):

	TObs        — observation type (e.g. rune, byte)
	TToken      — token type
	TTokenRole  — lexer-assigned token role/category
	TLexerState — lexer internal state snapshot
	TNodeKind   — AST node kind (stored in node.NodeKind)
*/
type Ctx[TObs cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] = syntaxa.RuleContext[TObs, TToken, TTokenRole, TLexerState, TNodeKind]

/*
Rule is a package-local alias for syntaxa.ParserRule.

All recursive descent combinators operate exclusively on this alias
to ensure consistent generic ordering and long-term API stability.

A Rule consumes from the provided context transactionally (via the
context’s Save/Restore discipline) and returns:

	(node, true)  — on success
	(nil, false)  — on failure
*/
type Rule[TObs cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] = syntaxa.ParserRule[TObs, TToken, TTokenRole, TLexerState, TNodeKind]

// =============================================================
// COMBINATORS
// =============================================================

/*
Sequence composes multiple rules in sequence.

All rules must succeed in order for Sequence to succeed.

On failure:
  - all consumption is rolled back
  - no partial AST nodes are returned

On success:
  - a new AST node is created
  - all child nodes are attached in order

Typical use:

	Sequence(term, Tok(Plus), term)
*/
func Sequence[TObs cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable](
	rules ...Rule[TObs, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObs, TToken, TTokenRole, TLexerState, TNodeKind] {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TNodeKind]) (*syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TNodeKind], bool) {

		snap := ctx.Save()
		node := ctx.CreateASTNode()

		for _, rule := range rules {
			child, ok := rule(ctx)
			if !ok {
				ctx.Restore(snap)
				return nil, false
			}
			child.Parent = node
			node.Children = append(node.Children, child)
		}

		return node, true
	}
}

/*
Choice tries rules in order and returns the first successful match.

Each rule is attempted transactionally.
If all rules fail, Choice fails without consuming input.

Typical use:

	Choice(ifStmt, whileStmt, exprStmt)
*/
func Choice[TObs cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable](
	rules ...Rule[TObs, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObs, TToken, TTokenRole, TLexerState, TNodeKind] {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TNodeKind]) (*syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TNodeKind], bool) {

		for _, rule := range rules {
			snap := ctx.Save()
			if node, ok := rule(ctx); ok {
				return node, true
			}
			ctx.Restore(snap)
		}

		return nil, false
	}
}

/*
Optional attempts a rule and succeeds regardless.

If the rule matches:
  - its node is returned

If it does not:
  - no input is consumed
  - (nil, true) is returned

This allows optional grammar elements without backtracking noise.

Typical use:

	Sequence(typeName, Optional(initializer))
*/
func Optional[TObs cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable](
	rule Rule[TObs, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObs, TToken, TTokenRole, TLexerState, TNodeKind] {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TNodeKind]) (*syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TNodeKind], bool) {

		snap := ctx.Save()
		if node, ok := rule(ctx); ok {
			return node, true
		}

		ctx.Restore(snap)
		return nil, true
	}
}

/*
Many applies a rule zero or more times.

It never fails.
All successful matches are collected as children of a new node.

Typical use:

	Many(statement)
*/
func Many[TObs cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable](
	rule Rule[TObs, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObs, TToken, TTokenRole, TLexerState, TNodeKind] {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TNodeKind]) (*syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TNodeKind], bool) {

		node := ctx.CreateASTNode()

		for {
			snap := ctx.Save()
			child, ok := rule(ctx)
			if !ok {
				ctx.Restore(snap)
				break
			}
			child.Parent = node
			node.Children = append(node.Children, child)
		}

		return node, true
	}
}

/*
Many1 applies a rule one or more times.

It fails if the rule does not match at least once.

Typical use:

	Many1(parameter)
*/
func Many1[TObs cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable](
	rule Rule[TObs, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObs, TToken, TTokenRole, TLexerState, TNodeKind] {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TNodeKind]) (*syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TNodeKind], bool) {

		node := ctx.CreateASTNode()

		first, ok := rule(ctx)
		if !ok {
			return nil, false
		}

		first.Parent = node
		node.Children = append(node.Children, first)

		for {
			snap := ctx.Save()
			child, ok := rule(ctx)
			if !ok {
				ctx.Restore(snap)
				break
			}
			child.Parent = node
			node.Children = append(node.Children, child)
		}

		return node, true
	}
}

/*
TokenMatch matches a single token and produces a leaf AST node.

If the current token does not match, TokenMatch fails without consuming.

The resulting node:
  - contains the consumed token
  - has no children

Typical use:

	TokenMatch(Identifier, IdentNode)
*/
func TokenMatch[TObs cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable](
	token TToken,
	kind TNodeKind,
) Rule[TObs, TToken, TTokenRole, TLexerState, TNodeKind] {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TNodeKind]) (*syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TNodeKind], bool) {

		lex := ctx.Peek(0)
		if lex.Token != token {
			return nil, false
		}

		ctx.Consume()

		node := ctx.CreateASTNode()
		node.NodeKind = kind
		node.Tokens = append(node.Tokens, lex)

		return node, true
	}
}
