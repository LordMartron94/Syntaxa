package rd

import (
	"cmp"
	"syntaxa"
)

// =============================================================
// LOCAL TYPE ALIASES (PREVENT GENERIC DRIFT)
// =============================================================

/*
Ctx is a package-local alias for syntaxa.ExecRuleContext.

Canonical generic order:

	TObs        — observation type (rune, byte, etc.)
	TToken      — token enum/type
	TTokenRole  — lexer role/category
	TLexerState — lexer internal state
	TNodeKind   — AST node kind
*/
type Ctx[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
] = syntaxa.ExecRuleContext[TObs, TToken, TTokenRole, TLexerState, TNodeKind]

/*
Rule is a package-local alias for syntaxa.ParserRule.

A rule executes transactionally and returns:

	(node, true)  on success
	(nil, false)  on failure
*/
type Rule[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
] = syntaxa.ParserRule[TObs, TToken, TTokenRole, TLexerState, TNodeKind]

// =============================================================
// INTERNAL HELPERS
// =============================================================

/*
newAnonymousNode creates a structural container node with
the zero value of TNodeKind.

Used by combinators such as Sequence, Many, Many1 which exist
purely for structure and are typically wrapped by higher-level
grammar rules that assign semantic kinds.
*/
func newAnonymousNode[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TNodeKind]) *syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TNodeKind] {
	var zeroKind TNodeKind
	return ctx.Editor.NewNode(zeroKind)
}

// =============================================================
// COMBINATORS
// =============================================================

/*
Sequence composes multiple rules in order.

All rules must succeed for Sequence to succeed.

On failure:
  - all consumption is rolled back
  - no AST nodes are produced

On success:
  - a new container node is created
  - all child nodes are attached in order

Typical use:

	Sequence(term, Tok(Plus), term)
*/
func Sequence[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	rules ...Rule[TObs, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObs, TToken, TTokenRole, TLexerState, TNodeKind] {
	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TNodeKind]) (*syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TNodeKind], bool) {
		snap := ctx.Save()
		node := newAnonymousNode(ctx)

		for _, rule := range rules {

			child, ok := rule(ctx)
			if !ok {
				ctx.Restore(snap)
				return nil, false
			}

			ctx.Editor.AttachChild(node, child)
		}

		return node, true
	}
}

/*
Choice attempts each rule in order and returns the first success.

Each rule executes transactionally.

If all rules fail, Choice fails without consuming input.

Typical use:

	Choice(ifStmt, whileStmt, exprStmt)
*/
func Choice[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
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
Optional attempts a rule and always succeeds.

If the rule matches:
  - its node is returned

If it fails:
  - no input is consumed
  - (nil, true) is returned

Typical use:

	Sequence(typeName, Optional(initializer))
*/
func Optional[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
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

All successful matches are attached as children of a new container node.

Typical use:

	Many(statement)
*/
func Many[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	rule Rule[TObs, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObs, TToken, TTokenRole, TLexerState, TNodeKind] {
	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TNodeKind]) (*syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TNodeKind], bool) {

		node := newAnonymousNode(ctx)

		for {

			snap := ctx.Save()

			child, ok := rule(ctx)
			if !ok {
				ctx.Restore(snap)
				break
			}

			ctx.Editor.AttachChild(node, child)
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
func Many1[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	rule Rule[TObs, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObs, TToken, TTokenRole, TLexerState, TNodeKind] {
	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TNodeKind]) (*syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TNodeKind], bool) {

		node := newAnonymousNode(ctx)

		first, ok := rule(ctx)
		if !ok {
			return nil, false
		}

		ctx.Editor.AttachChild(node, first)

		for {

			snap := ctx.Save()

			child, ok := rule(ctx)
			if !ok {
				ctx.Restore(snap)
				break
			}

			ctx.Editor.AttachChild(node, child)
		}

		return node, true
	}
}

/*
TokenMatch matches a single token and produces a leaf AST node.

If the current token does not match, TokenMatch fails without consuming.

The resulting node:
  - has the specified node kind
  - contains the consumed token
  - has no children

Typical use:

	TokenMatch(Identifier, IdentNode)
*/
func TokenMatch[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	token TToken,
	kind TNodeKind,
) Rule[TObs, TToken, TTokenRole, TLexerState, TNodeKind] {
	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TNodeKind]) (*syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TNodeKind], bool) {
		lex := ctx.Peek(0)
		if lex.Token != token {
			return nil, false
		}

		ctx.Consume()

		node := ctx.Editor.NewNode(kind)
		ctx.Editor.AddToken(node, lex)

		return node, true
	}
}
