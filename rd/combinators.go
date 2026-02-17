package rd

import (
	"cmp"
	"fmt"

	"lexarch"
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
Result is a package-local alias for syntaxa.RuleResult.

Result carries both:
  - the produced AST node (may be nil for "no-node" rules)
  - whether the produced node is intended to be attached at the parser root
*/
type Result[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TNodeKind comparable,
] = syntaxa.RuleResult[TObs, TToken, TTokenRole, TNodeKind]

/*
Rule is a package-local alias for syntaxa.ParserRule.

A rule executes transactionally and returns:

	(result, true)  on success
	(_, false)      on failure (caller must treat as rollback)
*/
type Rule[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
] = syntaxa.ParserRule[TObs, TToken, TTokenRole, TLexerState, TNodeKind]

// =============================================================
// RESULT HELPERS
// =============================================================

/*
NoNode returns a successful result with no AST node and TopLevel=false.
Use for "structural-only" matches such as Tok/Expect/Guard.
*/
func NoNode[TObs cmp.Ordered, TToken, TTokenRole, TKind comparable]() Result[TObs, TToken, TTokenRole, TKind] {
	return Result[TObs, TToken, TTokenRole, TKind]{Node: nil, TopLevel: false}
}

/*
NodeResult wraps a node in a Result with TopLevel=false.
This is the default for all inner productions.
*/
func NodeResult[TObs cmp.Ordered, TToken, TTokenRole, TKind comparable](
	n *syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
) Result[TObs, TToken, TTokenRole, TKind] {
	return Result[TObs, TToken, TTokenRole, TKind]{Node: n, TopLevel: false}
}

// =============================================================
// TOP-LEVEL MARKER
// =============================================================

/*
TopLevel marks a rule's successful result as a parser-root production.

This is the explicit, grammar-owned way to indicate that the produced
node should be attached to the root by the engine.

Do NOT infer "top-level" from node.Parent()==nil; that is a heuristic and
will break as your grammar grows (deferred attachment, incremental builds,
multi-phase parses, etc.).
*/
func TopLevel[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	rule Rule[TObs, TToken, TTokenRole, TLexerState, TKind],
) Rule[TObs, TToken, TTokenRole, TLexerState, TKind] {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) (Result[TObs, TToken, TTokenRole, TKind], bool) {
		res, ok := rule(ctx)
		if !ok {
			return res, false
		}

		res.TopLevel = true
		return res, true
	}
}

// =============================================================
// CORE CONTROL-FLOW COMBINATORS (DO NOT CREATE NODES)
// =============================================================

/*
Choice attempts each rule in order and returns the first success.

Each rule attempt is isolated:
  - on failure, cursor is restored to the Choice entry point
  - on success, cursor remains advanced by the winning rule
*/
func Choice[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	rules ...Rule[TObs, TToken, TTokenRole, TLexerState, TKind],
) Rule[TObs, TToken, TTokenRole, TLexerState, TKind] {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) (Result[TObs, TToken, TTokenRole, TKind], bool) {

		for _, r := range rules {
			snap := ctx.Save()

			if res, ok := r(ctx); ok {
				return res, true
			}

			ctx.Restore(snap)
		}

		var zero Result[TObs, TToken, TTokenRole, TKind]
		return zero, false

	}
}

/*
Optional attempts a rule and always succeeds.

If rule matches:
  - returns its result

If rule fails:
  - restores cursor
  - returns (NoNode, true)
*/
func Optional[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	rule Rule[TObs, TToken, TTokenRole, TLexerState, TKind],
) Rule[TObs, TToken, TTokenRole, TLexerState, TKind] {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) (Result[TObs, TToken, TTokenRole, TKind], bool) {

		snap := ctx.Save()

		if res, ok := rule(ctx); ok {
			return res, true
		}

		ctx.Restore(snap)
		return NoNode[TObs, TToken, TTokenRole, TKind](), true
	}
}

/*
Guard runs pred without consuming input.

If pred returns true:
  - succeeds and returns (NoNode, true)

If pred returns false:
  - fails and returns (_, false)

Useful for predictive branching without allocating AST nodes.
*/
func Guard[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	pred func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) bool,
) Rule[TObs, TToken, TTokenRole, TLexerState, TKind] {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) (Result[TObs, TToken, TTokenRole, TKind], bool) {
		snap := ctx.Save()
		defer ctx.Restore(snap)

		if pred(ctx) {
			return NoNode[TObs, TToken, TTokenRole, TKind](), true
		}

		var zero Result[TObs, TToken, TTokenRole, TKind]
		return zero, false

	}
}

// =============================================================
// NODE-BUILDING COMBINATORS (ALWAYS REQUIRE A KIND)
// =============================================================

/*
SequenceAs composes multiple rules in order and wraps them in a node
of the provided kind.

All rules must succeed for SequenceAs to succeed.

On failure:
  - restores cursor to entry snapshot
  - returns (NoNode, false)

On success:
  - creates node(kind)
  - attaches each produced child node in order (nil nodes are ignored)
  - returns (node, TopLevel=false)
*/
func SequenceAs[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	kind TKind,
	rules ...Rule[TObs, TToken, TTokenRole, TLexerState, TKind],
) Rule[TObs, TToken, TTokenRole, TLexerState, TKind] {

	short := func(raw []TObs) string {
		const max = 40
		if len(raw) == 0 {
			return "∅"
		}
		s := fmt.Sprintf("%v", raw)
		if len(s) > max {
			return s[:max] + "…"
		}
		return s
	}

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) (Result[TObs, TToken, TTokenRole, TKind], bool) {

		snap := ctx.Save()
		node := ctx.Editor.NewNode(kind)

		fmt.Printf(
			"\n▶ SequenceAs %v BEGIN\n",
			kind,
		)

		for i, r := range rules {

			before := ctx.Save()

			rawBefore := ctx.PeekRaw(0)
			logBefore := ctx.Peek(0)

			res, ok := r(ctx)

			after := ctx.Save()
			rawAfter := ctx.PeekRaw(0)
			logAfter := ctx.Peek(0)

			moved := after.Index() - before.Index()
			consumed := moved != 0
			produced := res.Node != nil

			if !ok {
				fmt.Printf(
					"✖ rule #%d FAILED\n"+
						"    raw:     %v @ %d:%d\n"+
						"    logical: %v @ %d:%d\n"+
						"    text:    %s\n",
					i,
					rawBefore.Token,
					rawBefore.StartLine, rawBefore.StartColumn,
					logBefore.Token,
					logBefore.StartLine, logBefore.StartColumn,
					short(rawBefore.Raw),
				)

				ctx.Restore(snap)
				return res, false
			}

			fmt.Printf(
				"✔ rule #%d OK\n"+
					"    raw:     %v → %v\n"+
					"    logical: %v → %v\n"+
					"    moved:   %d tokens\n"+
					"    span:    %d:%d → %d:%d\n"+
					"    text:    %s\n"+
					"    node:    %v\n",
				i,
				rawBefore.Token, rawAfter.Token,
				logBefore.Token, logAfter.Token,
				moved,
				rawBefore.StartLine, rawBefore.StartColumn,
				rawAfter.EndLine, rawAfter.EndColumn,
				short(rawBefore.Raw),
				produced,
			)

			if !consumed {
				panic(
					fmt.Sprintf(
						"SequenceAs %v rule #%d succeeded without consuming input",
						kind,
						i,
					),
				)
			}

			if produced {
				ctx.Editor.AttachChild(node, res.Node)
			}
		}

		fmt.Printf("✔ SequenceAs %v COMPLETE\n\n", kind)

		return NodeResult(node), true
	}
}

/*
Group composes multiple rules in sequence but returns the first
non-nil node produced by the sequence (no wrapper node).

Rules may return nil nodes (e.g. Optional, Guard, Tok).
If all produced nodes are nil, Group returns (NoNode, true).

TopLevel:
  - Group never promotes TopLevel; it is an inner combinator.
  - If you want a top-level production, wrap the outermost rule in TopLevel(...).
*/
func Group[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	rules ...Rule[TObs, TToken, TTokenRole, TLexerState, TKind],
) Rule[TObs, TToken, TTokenRole, TLexerState, TKind] {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) (Result[TObs, TToken, TTokenRole, TKind], bool) {

		snap := ctx.Save()

		var first *syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]

		for _, r := range rules {
			res, ok := r(ctx)
			if !ok {
				ctx.Restore(snap)
				return res, false
			}

			if first == nil && res.Node != nil {
				first = res.Node
			}
		}

		if first == nil {
			return NoNode[TObs, TToken, TTokenRole, TKind](), true
		}
		return NodeResult(first), true
	}
}

/*
ManyAs applies a rule zero or more times and wraps the results in node(kind).

It never fails.

All successful produced nodes are attached as children of node(kind).
Nil nodes are ignored.

Cycle safety:
  - If rule "succeeds" without consuming input, the loop terminates.
*/
func ManyAs[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	kind TKind,
	rule Rule[TObs, TToken, TTokenRole, TLexerState, TKind],
) Rule[TObs, TToken, TTokenRole, TLexerState, TKind] {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) (Result[TObs, TToken, TTokenRole, TKind], bool) {

		node := ctx.Editor.NewNode(kind)

		for {
			before := markProgress(ctx)

			snap := ctx.Save()
			res, ok := rule(ctx)
			if !ok {
				ctx.Restore(snap)
				break
			}
			if res.Node != nil {
				ctx.Editor.AttachChild(node, res.Node)
			}

			after := markProgress(ctx)
			if after == before {
				// Defensive: prevent infinite loop on "success without consumption".
				break
			}
		}

		return NodeResult(node), true
	}
}

/*
Many1As applies a rule one or more times and wraps the results in node(kind).

It fails if the rule does not match at least once.

Cycle safety:
  - If rule "succeeds" without consuming input, the loop terminates.
*/
func Many1As[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	kind TKind,
	rule Rule[TObs, TToken, TTokenRole, TLexerState, TKind],
) Rule[TObs, TToken, TTokenRole, TLexerState, TKind] {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) (Result[TObs, TToken, TTokenRole, TKind], bool) {

		node := ctx.Editor.NewNode(kind)

		firstRes, ok := rule(ctx)
		if !ok {
			return NoNode[TObs, TToken, TTokenRole, TKind](), false
		}
		if firstRes.Node != nil {
			ctx.Editor.AttachChild(node, firstRes.Node)
		}

		for {
			before := markProgress(ctx)

			snap := ctx.Save()
			res, ok := rule(ctx)
			if !ok {
				ctx.Restore(snap)
				break
			}
			if res.Node != nil {
				ctx.Editor.AttachChild(node, res.Node)
			}

			after := markProgress(ctx)
			if after == before {
				break
			}
		}

		return NodeResult(node), true
	}
}

/*
Wrap parses rule and wraps the produced node in a parent node(kind).

If rule succeeds but produces nil node, Wrap returns an empty node(kind).

TopLevel is always false; use TopLevel(Wrap(...)) if you want a root production.
*/
func Wrap[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	kind TKind,
	rule Rule[TObs, TToken, TTokenRole, TLexerState, TKind],
) Rule[TObs, TToken, TTokenRole, TLexerState, TKind] {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) (Result[TObs, TToken, TTokenRole, TKind], bool) {

		snap := ctx.Save()
		res, ok := rule(ctx)
		if !ok {
			ctx.Restore(snap)
			return res, false
		}

		node := ctx.Editor.NewNode(kind)
		if res.Node != nil {
			ctx.Editor.AttachChild(node, res.Node)
		}
		return NodeResult(node), true
	}
}

// =============================================================
// TOKEN COMBINATORS
// =============================================================

/*
TokenMatch matches a single token and produces a leaf AST node(kind).

If current token does not match, TokenMatch fails without consuming.

The resulting node:
  - has the specified node kind
  - contains the consumed token
*/
func TokenMatch[
	TObs cmp.Ordered,
	TToken comparable,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	token TToken,
	kind TKind,
) Rule[TObs, TToken, TTokenRole, TLexerState, TKind] {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) (Result[TObs, TToken, TTokenRole, TKind], bool) {

		lex := ctx.Peek(0)
		if lex.Token != token {
			var zero Result[TObs, TToken, TTokenRole, TKind]
			return zero, false
		}

		lex = ctx.Consume()

		node := ctx.Editor.NewNode(kind)
		ctx.Editor.AddToken(node, lex)

		return NodeResult(node), true
	}
}

/*
TokenExpect matches a single token and produces a leaf AST node(kind).

If current token does not match:
  - emits a syntax error
  - fails without consuming

The resulting node:
  - has the specified node kind
  - contains the consumed token
*/
func TokenExpect[
	TObs cmp.Ordered,
	TToken comparable,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	token TToken,
	kind TKind,
	message string,
) Rule[TObs, TToken, TTokenRole, TLexerState, TKind] {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) (Result[TObs, TToken, TTokenRole, TKind], bool) {

		lex := ctx.Peek(0)
		if lex.Token != token {

			if message == "" {
				message = fmt.Sprintf("expected token %v", token)
			}

			ctx.Report(
				lex.StartLine,
				lex.StartColumn,
				message,
			)

			var zero Result[TObs, TToken, TTokenRole, TKind]
			return zero, false
		}

		lex = ctx.Consume()

		node := ctx.Editor.NewNode(kind)
		ctx.Editor.AddToken(node, lex)

		return NodeResult(node), true
	}
}

/*
Tok consumes the specified token and produces no AST node.

This is the "no AST node" version of TokenMatch.
Useful inside SequenceAs/Group to avoid creating punctuation nodes.
*/
func Tok[
	TObs cmp.Ordered,
	TToken comparable,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	token TToken,
) Rule[TObs, TToken, TTokenRole, TLexerState, TKind] {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) (Result[TObs, TToken, TTokenRole, TKind], bool) {

		lex := ctx.Peek(0)
		if lex.Token != token {
			var zero Result[TObs, TToken, TTokenRole, TKind]
			return zero, false
		}

		_ = ctx.Consume()
		return NoNode[TObs, TToken, TTokenRole, TKind](), true
	}
}

/*
Expect behaves like Tok, but on failure it emits a syntax error and fails.

This is not recovery; it is localized diagnostics. The core engine still
owns recovery strategy.
*/
func Expect[
	TObs cmp.Ordered,
	TToken comparable,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	token TToken,
	message string,
) Rule[TObs, TToken, TTokenRole, TLexerState, TKind] {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) (Result[TObs, TToken, TTokenRole, TKind], bool) {

		lex := ctx.Peek(0)
		if lex.Token != token {
			if message == "" {
				message = fmt.Sprintf("expected token %v", token)
			}
			ctx.Report(lex.StartLine, lex.StartColumn, message)
			var zero Result[TObs, TToken, TTokenRole, TKind]

			return zero, false
		}

		_ = ctx.Consume()
		return NoNode[TObs, TToken, TTokenRole, TKind](), true
	}
}

// =============================================================
// SLOT/LABEL COMBINATORS (STRUCTURED ATTACHMENT)
// =============================================================

/*
AsSlot parses rule and assigns the resulting node to parent slot.

If rule succeeds but produces nil node, the slot is not set.
*/
func AsSlot[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	slotName string,
	rule Rule[TObs, TToken, TTokenRole, TLexerState, TKind],
) func(
	ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind],
	parent *syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
) bool {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind], parent *syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) bool {

		snap := ctx.Save()
		res, ok := rule(ctx)
		if !ok {
			ctx.Restore(snap)
			return false
		}

		if res.Node != nil {
			ctx.Editor.SetSlot(parent, slotName, res.Node)
		}
		return true
	}
}

/*
SeqItem represents a single structured step inside SequenceSlotsAs.

Each item mutates the parent node by either:
  - attaching a positional child
  - assigning a named slot

Returning false indicates parse failure and triggers rollback.
*/
type SeqItem[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
] interface {
	apply(
		ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind],
		parent *syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
	) bool
}

type seqChild[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
] struct {
	r Rule[TObs, TToken, TTokenRole, TLexerState, TKind]
}

func (s seqChild[TObs, TToken, TTokenRole, TLexerState, TKind]) apply(
	ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind],
	parent *syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
) bool {

	snap := ctx.Save()
	res, ok := s.r(ctx)
	if !ok {
		ctx.Restore(snap)
		return false
	}
	if res.Node != nil {
		ctx.Editor.AttachChild(parent, res.Node)
	}
	return true
}

type seqSlot[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
] struct {
	name string
	r    Rule[TObs, TToken, TTokenRole, TLexerState, TKind]
}

func (s seqSlot[TObs, TToken, TTokenRole, TLexerState, TKind]) apply(
	ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind],
	parent *syntaxa.SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
) bool {

	snap := ctx.Save()
	res, ok := s.r(ctx)
	if !ok {
		ctx.Restore(snap)
		return false
	}
	if res.Node != nil {
		ctx.Editor.SetSlot(parent, s.name, res.Node)
	}
	return true
}

/*
Child marks a rule result as a positional child in SequenceSlotsAs.
*/
func Child[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	r Rule[TObs, TToken, TTokenRole, TLexerState, TKind],
) SeqItem[TObs, TToken, TTokenRole, TLexerState, TKind] {
	return seqChild[TObs, TToken, TTokenRole, TLexerState, TKind]{r: r}
}

/*
Slot marks a rule result as a named slot in SequenceSlotsAs.
*/
func Slot[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	name string,
	r Rule[TObs, TToken, TTokenRole, TLexerState, TKind],
) SeqItem[TObs, TToken, TTokenRole, TLexerState, TKind] {
	return seqSlot[TObs, TToken, TTokenRole, TLexerState, TKind]{name: name, r: r}
}

/*
SequenceSlotsAs composes multiple SeqItem entries in order and wraps
them in a node(kind). Items may attach as children or slots.

On failure:
  - restores cursor
  - returns (NoNode, false)

TopLevel is always false; use TopLevel(SequenceSlotsAs(...)) for root productions.
*/
func SequenceSlotsAs[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	kind TKind,
	items ...SeqItem[TObs, TToken, TTokenRole, TLexerState, TKind],
) Rule[TObs, TToken, TTokenRole, TLexerState, TKind] {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) (Result[TObs, TToken, TTokenRole, TKind], bool) {

		snap := ctx.Save()
		node := ctx.Editor.NewNode(kind)

		for _, it := range items {
			if ok := it.apply(ctx, node); !ok {
				ctx.Restore(snap)
				return NoNode[TObs, TToken, TTokenRole, TKind](), false
			}
		}

		return NodeResult[TObs, TToken, TTokenRole, TKind](node), true
	}
}

// =============================================================
// COMMON LIST PATTERNS
// =============================================================

/*
SepBy parses zero or more occurrences of elem separated by sep,
wrapping them into node(kind). Never fails.

Cycle safety:
  - If sep+elem "succeed" without consuming input, the loop terminates.

Notes:
  - sep and elem may be "no-node" rules (Tok/Expect/Guard), in which case
    only elem nodes (if any) are attached.
*/
func SepBy[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	kind TKind,
	elem Rule[TObs, TToken, TTokenRole, TLexerState, TKind],
	sep Rule[TObs, TToken, TTokenRole, TLexerState, TKind],
) Rule[TObs, TToken, TTokenRole, TLexerState, TKind] {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) (Result[TObs, TToken, TTokenRole, TKind], bool) {

		node := ctx.Editor.NewNode(kind)

		// First elem is optional.
		snap0 := ctx.Save()
		firstRes, ok := elem(ctx)
		if !ok {
			ctx.Restore(snap0)
			return NodeResult[TObs, TToken, TTokenRole, TKind](node), true
		}
		if firstRes.Node != nil {
			ctx.Editor.AttachChild(node, firstRes.Node)
		}

		for {

			before := markProgress(ctx)

			// sep must match
			snapSep := ctx.Save()
			_, okSep := sep(ctx)
			if !okSep {
				ctx.Restore(snapSep)
				break
			}

			// elem must match after sep, otherwise rollback sep too
			snapElem := ctx.Save()
			nextRes, okElem := elem(ctx)
			if !okElem {
				ctx.Restore(snapElem)
				ctx.Restore(snapSep)
				break
			}
			if nextRes.Node != nil {
				ctx.Editor.AttachChild(node, nextRes.Node)
			}

			after := markProgress(ctx)
			if after == before {
				// Defensive: prevent infinite loops if elem/sep succeed without consuming.
				break
			}
		}

		return NodeResult[TObs, TToken, TTokenRole, TKind](node), true
	}
}

/*
SepBy1 parses one or more occurrences of elem separated by sep,
wrapping them into node(kind). Fails if first elem fails.

Cycle safety:
  - If sep+elem "succeed" without consuming input, the loop terminates.
*/
func SepBy1[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	kind TKind,
	elem Rule[TObs, TToken, TTokenRole, TLexerState, TKind],
	sep Rule[TObs, TToken, TTokenRole, TLexerState, TKind],
) Rule[TObs, TToken, TTokenRole, TLexerState, TKind] {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) (Result[TObs, TToken, TTokenRole, TKind], bool) {

		node := ctx.Editor.NewNode(kind)

		firstRes, ok := elem(ctx)
		if !ok {
			return NoNode[TObs, TToken, TTokenRole, TKind](), false
		}
		if firstRes.Node != nil {
			ctx.Editor.AttachChild(node, firstRes.Node)
		}

		for {

			before := markProgress(ctx)

			snapSep := ctx.Save()
			_, okSep := sep(ctx)
			if !okSep {
				ctx.Restore(snapSep)
				break
			}

			snapElem := ctx.Save()
			nextRes, okElem := elem(ctx)
			if !okElem {
				ctx.Restore(snapElem)
				ctx.Restore(snapSep)
				break
			}
			if nextRes.Node != nil {
				ctx.Editor.AttachChild(node, nextRes.Node)
			}

			after := markProgress(ctx)
			if after == before {
				break
			}
		}

		return NodeResult(node), true
	}
}

/* ============================================================
   CONTEXT SCOPING COMBINATORS
   ============================================================ */

/*
WithCtx wraps a rule in a scoped ExecRuleContext mutation.

Lifecycle:

 1. A transactional snapshot is taken via ctx.Save()
 2. enter(ctx) is executed to mutate parsing context
 3. rule(ctx) is executed normally
 4. exit(ctx) is always executed (deferred)
 5. If rule fails, cursor state is restored automatically

Invariants:

  - enter/exit must be symmetrical (stack discipline)
  - rules MUST NOT manually Restore() on failure
  - scoped mutations are automatically rolled back on failure

Typical uses:

  - temporary skip role sets
  - recovery synchronization scopes
  - lexer state switching
  - diagnostics scopes
  - grammar-local parsing modes

This is the foundational primitive for all context-aware grammar control.

Never mutate ExecRuleContext globally inside grammar rules.
Always use scoped combinators instead.

Failure semantics:

  - if rule fails: state is fully rolled back
  - if rule succeeds: consumption is committed, scope exits cleanly
*/
func WithCtx[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	enter func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]),
	exit func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]),
	rule Rule[TObs, TToken, TTokenRole, TLexerState, TKind],
) Rule[TObs, TToken, TTokenRole, TLexerState, TKind] {

	return func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) (Result[TObs, TToken, TTokenRole, TKind], bool) {

		snap := ctx.Save()

		enter(ctx)
		defer exit(ctx)

		res, ok := rule(ctx)
		if !ok {
			ctx.Restore(snap)
			return res, false
		}

		return res, true
	}
}

/*
WithSkip temporarily installs a local skippable token-role set
for the duration of rule execution.

All Consume() calls inside the scope automatically ignore these roles.

Typical uses:

  - ignoring whitespace
  - ignoring comments
  - grammar-local trivia suppression

The previous skip set is restored automatically on exit or failure.

Never call PushSkipRoles manually inside grammar rules.
Use this scoped helper instead.
*/
func WithSkip[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	r Rule[TObs, TToken, TTokenRole, TLexerState, TKind],
	roles ...TTokenRole,
) Rule[TObs, TToken, TTokenRole, TLexerState, TKind] {

	return WithCtx(
		func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) {
			ctx.PushSkipRoles(roles...)
		},
		func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) {
			ctx.PopSkipRoles()
		},
		r,
	)
}

/*
WithRecovery temporarily installs a local recovery synchronization token set.

Used to define grammar-specific error resynchronization points.

On syntax error, the parser may skip input until one of these tokens
is encountered.

The previous recovery set is restored automatically on exit or failure.

Typical uses:

  - statement boundaries
  - block terminators
  - delimiter recovery

Always scope recovery behavior — never mutate globally.
*/
func WithRecovery[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	r Rule[TObs, TToken, TTokenRole, TLexerState, TKind],
	tokens ...TToken,
) Rule[TObs, TToken, TTokenRole, TLexerState, TKind] {

	return WithCtx(
		func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) {
			ctx.PushRecovery(tokens...)
		},
		func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) {
			ctx.PopRecovery()
		},
		r,
	)
}

/*
WithLexerState temporarily switches the active lexer ruleset.

Useful for:

  - string interpolation modes
  - template languages
  - indentation-sensitive parsing
  - embedded DSLs

The previous lexer state is restored automatically on exit or failure.

Lexer state transitions are fully transactional.
*/
func WithLexerState[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	state TLexerState,
	r Rule[TObs, TToken, TTokenRole, TLexerState, TKind],
) Rule[TObs, TToken, TTokenRole, TLexerState, TKind] {

	return WithCtx(
		func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) {
			ctx.SetLexerState(state)
		},
		func(ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) {
			// state rollback handled by transaction snapshot
		},
		r,
	)
}

// =============================================================
// SMALL UTILITIES
// =============================================================

/*
Lexeme returns the current logical lexeme (Peek(0)).
Convenience for client code.
*/
func Lexeme[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) lexarch.Lexeme[TObs, TToken, TTokenRole] {
	return ctx.Peek(0)
}

// =============================================================
// INTERNAL PROGRESS MARKING
// =============================================================

type progressMark struct {
	start int
	end   int
	token any
	role  any
}

func markProgress[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](ctx Ctx[TObs, TToken, TTokenRole, TLexerState, TKind]) progressMark {

	lex := ctx.PeekRaw(0)

	return progressMark{
		start: lex.Start,
		end:   lex.End,
		token: any(lex.Token),
		role:  any(lex.Role),
	}
}
