package syntaxa

import (
	"cmp"
	"fmt"
	"foundation/extensions"
	"lexarch"
	"strings"
	"structarch"
	"sync/atomic"
)

// =============================================================
// PARSING CURSOR
// =============================================================

/*
ParseCursor represents a snapshot of parser progress.

It is opaque by design and only meaningful to the RuleContext
implementation that created it.
*/
type ParseCursor struct {
	tokenIndex int
}

// =============================================================
// RULE CONTEXT
// =============================================================

/*
SelectRuleContext exposes pure, non-mutating lookahead operations.

Used exclusively for:
  - rule selection
  - branching
  - precedence decisions
  - predictive parsing

Must NEVER mutate parser or lexer state.
*/
type SelectRuleContext[TObservation cmp.Ordered, TToken, TTokenRole comparable] struct {

	/* Peek returns the lexeme at lookahead distance n (0 = current). Skips roles. */
	Peek func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole]

	/* PeekRaw returns the lexeme at lookahead distance n without skipping. */
	PeekRaw func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole]

	/* PeekRange returns upcoming lexemes without consuming input. */
	PeekRange func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole]

	/* Match reports whether current token matches any provided token. */
	Match func(tokens ...TToken) bool
}

/*
ExecRuleContext exposes transactional, mutating parsing operations.

Used exclusively once a grammar rule has been selected.

Transactional invariant:

  - Rules may consume freely while attempting to match
  - If a rule fails, cursor and lexer state are restored
  - Successful rules permanently commit consumption

Rules MUST NOT manually restore state on failure.
*/
type ExecRuleContext[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
] struct {
	SelectRuleContext[TObservation, TToken, TTokenRole]

	/* ============================================================
	   Consumption
	   ============================================================ */

	/*
		Consume consumes and returns the current lexeme.

		Skippable roles are automatically ignored.
	*/
	Consume func() lexarch.Lexeme[TObservation, TToken, TTokenRole]

	/*
		ConsumeRaw consumes and returns the current lexeme without
		skipping any token roles.
	*/
	ConsumeRaw func() lexarch.Lexeme[TObservation, TToken, TTokenRole]

	/*
		ConsumeRange returns up to `n` upcoming lexemes while
		consuming input.
	*/
	ConsumeRange func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole]

	/* ============================================================
	   Transaction control
	   ============================================================ */

	/*
		Save snapshots the current cursor state.
	*/
	Save func() ParseCursor

	/*
		Restore restores a previously saved cursor state.
	*/
	Restore func(ParseCursor)

	/* ============================================================
	   Error handling
	   ============================================================ */

	/*
		Report records a syntax error at a given source location.
	*/
	Report func(line, column int, description string)

	/*
		CreateErrorNode constructs a structured error AST node.
	*/
	CreateErrorNode func(message string) *SyntaxaASTNode[
		TObservation, TToken, TTokenRole, TNodeKind,
	]

	/* ============================================================
	   Lexer state control
	   ============================================================ */

	/*
		SetLexerState switches the active lexer ruleset/state.
	*/
	SetLexerState func(state TLexerState)

	/* ============================================================
	   AST construction
	   ============================================================ */

	/*
		Editor provides invariant-safe AST mutation and creation.
	*/
	Editor *ASTEditor[TObservation, TToken, TTokenRole, TNodeKind]

	/* ============================================================
	   Recovery management
	   ============================================================ */

	/*
		PushRecovery installs a new local synchronization token set.
	*/
	PushRecovery func(tokens ...TToken)

	/*
		PopRecovery removes the most recent synchronization set.
	*/
	PopRecovery func()

	/*
		CurrentRecovery returns the active synchronization tokens.
	*/
	CurrentRecovery func() []TToken

	/*
		PushSkipRoles installs a new local skippable role set.
	*/
	PushSkipRoles func(roles ...TTokenRole)

	/*
		PopSkipRoles removes the most recent skippable role set.
	*/
	PopSkipRoles func()

	/*
		CurrentSkips returns the active skippable role set.
	*/
	CurrentSkips func() []TTokenRole
}

func (ctx *ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) selectCTX() SelectRuleContext[TObservation, TToken, TTokenRole] {
	return SelectRuleContext[TObservation, TToken, TTokenRole]{
		Peek:      ctx.Peek,
		PeekRaw:   ctx.PeekRaw,
		PeekRange: ctx.PeekRange,
		Match:     ctx.Match,
	}
}

/*
BuildExecRuleContextFromSlice constructs a RuleContext over a linear
lexeme slice.

This is suitable for simple, fully-materialized token streams.

The cursor pointer is owned by the caller and mutated as parsing
progresses.

Use cases:
  - offline parsing
  - unit tests
  - small inputs fully resident in memory
*/
func BuildExecRuleContextFromSlice[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	lexemes []lexarch.Lexeme[TObservation, TToken, TTokenRole],
	errors *SyntaxErrors,
	cursor *int,
) ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	ctx := buildBaseContext[TObservation, TToken, TTokenRole, TLexerState](
		errors,
		parser.errorNodeKind,
	)

	// ---------------------------------------------------------
	// RAW ACCESS
	// ---------------------------------------------------------

	ctx.PeekRaw = func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		return lexemes[*cursor+n]
	}

	ctx.ConsumeRaw = func() lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		l := lexemes[*cursor]
		*cursor++
		return l
	}

	ctx.PeekRange = func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		return lexemes[*cursor : *cursor+n]
	}

	ctx.ConsumeRange = func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		l := lexemes[*cursor : *cursor+n]
		*cursor += n
		return l
	}

	// ---------------------------------------------------------
	// SKIP LOGIC
	// ---------------------------------------------------------

	isSkipped := func(role TTokenRole) bool {
		skips := ctx.CurrentSkips()
		for _, r := range skips {
			if r == role {
				return true
			}
		}
		return false
	}

	skipForward := func() {
		for {
			if *cursor >= len(lexemes) {
				return
			}
			if !isSkipped(lexemes[*cursor].Role) {
				return
			}
			*cursor++
		}
	}

	// ---------------------------------------------------------
	// LOGICAL ACCESS (AUTO-SKIPPING)
	// ---------------------------------------------------------

	ctx.Peek = func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		skipForward()
		return ctx.PeekRaw(n)
	}

	ctx.Consume = func() lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		skipForward()
		return ctx.ConsumeRaw()
	}

	ctx.Match = func(tokens ...TToken) bool {
		skipForward()

		for _, t := range tokens {
			if lexemes[*cursor].Token == t {
				*cursor++
				return true
			}
		}
		return false
	}

	// ---------------------------------------------------------
	// CURSOR CONTROL
	// ---------------------------------------------------------

	ctx.Save = func() ParseCursor {
		return ParseCursor{tokenIndex: *cursor}
	}

	ctx.Restore = func(c ParseCursor) {
		*cursor = c.tokenIndex
	}

	return ctx
}

/*
BuildExecRuleContextFromLexerSession adapts a lexarch LexerSession
into a RuleContext.

This allows grammar rules to drive token consumption directly
from a stateful DFA-based lexer.

Use cases:
  - context-sensitive lexing
  - language modes (strings, comments, templates)
  - high-performance parsing
*/
func BuildExecRuleContextFromLexerSession[
	TObservation cmp.Ordered,
	TState comparable,
	TToken comparable,
	TTokenRole comparable,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TState],
	lexer *lexarch.Lexer[TObservation, TState, TToken, TTokenRole],
	session *lexarch.LexerSession[TObservation, TState],
	errors *SyntaxErrors,
) ExecRuleContext[TObservation, TToken, TTokenRole, TState, TNodeKind] {

	ctx := buildBaseContext[TObservation, TToken, TTokenRole, TState](
		errors,
		parser.errorNodeKind,
	)

	// ---------------------------------------------------------
	// RAW ACCESS
	// ---------------------------------------------------------

	ctx.PeekRaw = func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		lex, err := lexarch.LexerPeek(lexer, session, n)
		if err != nil {
			panic(err)
		}
		return lex
	}

	ctx.ConsumeRaw = func() lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		lex, err := lexarch.LexerConsume(lexer, session)
		if err != nil {
			panic(err)
		}
		return lex
	}

	ctx.PeekRange = func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		lex, err := lexarch.LexerPeekRange(lexer, session, n)
		if err != nil {
			panic(err)
		}
		return lex
	}

	ctx.ConsumeRange = func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		lex, err := lexarch.LexerConsumeRange(lexer, session, n)
		if err != nil {
			panic(err)
		}
		return lex
	}

	// ---------------------------------------------------------
	// SKIP LOGIC
	// ---------------------------------------------------------

	isSkipped := func(role TTokenRole) bool {
		skips := ctx.CurrentSkips()
		for _, r := range skips {
			if r == role {
				return true
			}
		}
		return false
	}

	skipForward := func() {
		for {
			lex := ctx.PeekRaw(0)

			if lex.Token == parser.eofToken {
				return
			}

			if !isSkipped(lex.Role) {
				return
			}

			ctx.ConsumeRaw()
		}
	}

	// ---------------------------------------------------------
	// LOGICAL ACCESS
	// ---------------------------------------------------------

	ctx.Peek = func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		skipForward()
		return ctx.PeekRaw(n)
	}

	ctx.Consume = func() lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		skipForward()
		return ctx.ConsumeRaw()
	}

	ctx.Match = func(tokens ...TToken) bool {
		skipForward()

		lex := ctx.PeekRaw(0)
		for _, t := range tokens {
			if lex.Token == t {
				ctx.ConsumeRaw()
				return true
			}
		}
		return false
	}

	// ---------------------------------------------------------
	// CURSOR CONTROL
	// ---------------------------------------------------------

	ctx.Save = func() ParseCursor {
		return ParseCursor{tokenIndex: session.Position()}
	}

	ctx.Restore = func(c ParseCursor) {
		session.SetPosition(c.tokenIndex)
	}

	ctx.SetLexerState = func(state TState) {
		lexarch.LexerSessionSetState(session, state)
	}

	return ctx
}

/*
BuildExecRuleContextFromStreamingSession adapts a streaming lexer
into a RuleContext.

This enables online parsing over large or unbounded inputs.

Use cases:
  - network protocols
  - large files
  - incremental feeds
*/
func BuildExecRuleContextFromStreamingSession[
	TObservation cmp.Ordered,
	TState comparable,
	TToken comparable,
	TTokenRole comparable,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TState],
	lexer *lexarch.Lexer[TObservation, TState, TToken, TTokenRole],
	session *lexarch.StreamingLexerSession[TObservation, TState],
	errors *SyntaxErrors,
) ExecRuleContext[TObservation, TToken, TTokenRole, TState, TNodeKind] {

	ctx := buildBaseContext[TObservation, TToken, TTokenRole, TState](
		errors,
		parser.errorNodeKind,
	)

	// ---------------------------------------------------------
	// RAW ACCESS
	// ---------------------------------------------------------

	ctx.PeekRaw = func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		lex, err := lexarch.LexerPeekStreaming(lexer, session, n)
		if err != nil {
			panic(err)
		}
		return lex
	}

	ctx.ConsumeRaw = func() lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		lex, err := lexarch.LexerConsumeStreaming(lexer, session)
		if err != nil {
			panic(err)
		}
		return lex
	}

	ctx.PeekRange = func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		lex, err := lexarch.LexerPeekRangeStreaming(lexer, session, n)
		if err != nil {
			panic(err)
		}
		return lex
	}

	ctx.ConsumeRange = func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		lex, err := lexarch.LexerConsumeRangeStreaming(lexer, session, n)
		if err != nil {
			panic(err)
		}
		return lex
	}

	// ---------------------------------------------------------
	// SKIP LOGIC
	// ---------------------------------------------------------

	isSkipped := func(role TTokenRole) bool {
		skips := ctx.CurrentSkips()
		for _, r := range skips {
			if r == role {
				return true
			}
		}
		return false
	}

	skipForward := func() {
		for {
			lex := ctx.PeekRaw(0)

			if lex.Token == parser.eofToken {
				return
			}

			if !isSkipped(lex.Role) {
				return
			}

			ctx.ConsumeRaw()
		}
	}

	// ---------------------------------------------------------
	// LOGICAL ACCESS (AUTO-SKIPPING)
	// ---------------------------------------------------------

	ctx.Peek = func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		skipForward()
		return ctx.PeekRaw(n)
	}

	ctx.Consume = func() lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		skipForward()
		return ctx.ConsumeRaw()
	}

	ctx.Match = func(tokens ...TToken) bool {
		skipForward()

		lex := ctx.PeekRaw(0)
		for _, t := range tokens {
			if lex.Token == t {
				ctx.ConsumeRaw()
				return true
			}
		}
		return false
	}

	// ---------------------------------------------------------
	// CURSOR CONTROL
	// ---------------------------------------------------------

	ctx.Save = func() ParseCursor {
		return ParseCursor{tokenIndex: session.AbsPosition()}
	}

	ctx.Restore = func(c ParseCursor) {
		session.RestoreAbsolute(c.tokenIndex)
	}

	ctx.SetLexerState = func(state TState) {
		lexarch.StreamingLexerSessionSetState(session, state)
	}

	return ctx
}

// =============================================================
// AST NODE
// =============================================================

/*
SyntaxaASTNode represents a generic abstract syntax tree node.

It is a purely structural intermediate representation (IR).
All semantic meaning is defined externally via node kinds and
attached metadata.

The node is designed to support:
  - precise source mapping
  - incremental parsing and diffing
  - semantic annotation passes
  - format-preserving transformations
  - efficient tree navigation
*/
type SyntaxaASTNode[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind comparable] struct {

	/* Stable unique identifier for this node instance. */
	id uint64

	/* Client-defined syntactic category of the node. */
	kind TNodeKind

	// ---------------------------------------------------------
	// SOURCE MAPPING
	// ---------------------------------------------------------

	start       int
	end         int
	startLine   int
	startColumn int
	endLine     int
	endColumn   int

	// ---------------------------------------------------------
	// STRUCTURAL RELATIONSHIPS
	// ---------------------------------------------------------

	parent   *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind]
	children []*SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind]
	slots    map[string]*SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind]

	// ---------------------------------------------------------
	// TOKEN PRESERVATION
	// ---------------------------------------------------------

	tokens []lexarch.Lexeme[TObservation, TToken, TTokenRole]

	// ---------------------------------------------------------
	// METADATA
	// ---------------------------------------------------------

	attributes map[string]any

	// ---------------------------------------------------------
	// INCREMENTAL SYSTEMS
	// ---------------------------------------------------------

	revision uint64
}

func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) ID() uint64 {
	return n.id
}

func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) Kind() TKind {
	return n.kind
}

func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) Parent() *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind] {
	return n.parent
}

/* Children returns a defensive copy of child nodes. */
func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) Children() []*SyntaxaASTNode[TObs, TToken, TTokenRole, TKind] {
	out := make([]*SyntaxaASTNode[TObs, TToken, TTokenRole, TKind], len(n.children))
	copy(out, n.children)
	return out
}

/* Slot retrieves a node assigned to a named slot or nil. */
func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) Slot(name string) *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind] {
	if n.slots == nil {
		return nil
	}
	return n.slots[name]
}

/* SlotNames returns all defined slot keys. */
func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) SlotNames() []string {
	if n.slots == nil {
		return nil
	}
	out := make([]string, 0, len(n.slots))
	for k := range n.slots {
		out = append(out, k)
	}
	return out
}

/* Tokens returns a defensive copy of attached tokens. */
func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) Tokens() []lexarch.Lexeme[TObs, TToken, TTokenRole] {
	out := make([]lexarch.Lexeme[TObs, TToken, TTokenRole], len(n.tokens))
	copy(out, n.tokens)
	return out
}

func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) Span() (int, int) {
	return n.start, n.end
}

func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) LineSpan() (int, int, int, int) {
	return n.startLine, n.startColumn, n.endLine, n.endColumn
}

func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) Revision() uint64 {
	return n.revision
}

/* Attribute returns an attribute value and presence flag. */
func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) Attribute(key string) (any, bool) {
	if n.attributes == nil {
		return nil, false
	}
	v, ok := n.attributes[key]
	return v, ok
}

/* AttributeKeys returns all attribute keys. */
func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) AttributeKeys() []string {
	if n.attributes == nil {
		return nil
	}
	out := make([]string, 0, len(n.attributes))
	for k := range n.attributes {
		out = append(out, k)
	}
	return out
}

/*
Walk traverses the AST starting at this node using the selected strategy.

Traversal is:

  - cycle-safe
  - supports subtree pruning
  - supports early termination
  - read-only

It delegates internally to structarch.StructArchWalk.

Callback return values:

	skipSubtree:
	  when true, this node's children are not visited

	stopWalk:
	  when true, traversal stops immediately
*/
func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) Walk(
	strategy structarch.StructArchWalkStrategy,
	callback func(
		node *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
	) (skipSubtree, stopWalk bool),
) error {
	return structarch.StructArchWalk(
		structarch.WalkConfig[
			*SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
			uint64,
		]{
			Strategy: strategy,

			ID: func(n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) uint64 {
				return n.id
			},

			Children: func(n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) []*SyntaxaASTNode[TObs, TToken, TTokenRole, TKind] {
				return n.walkChildren()
			},

			Parent: func(n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind] {
				return n.parent
			},

			Callback: callback,
		},
		n,
	)
}

func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) WalkPre(
	callback func(*SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) (skip, stop bool),
) error {
	return n.Walk(structarch.WALK_STRATEGY_PRE, callback)
}

func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) WalkPost(
	callback func(*SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) (skip, stop bool),
) error {
	return n.Walk(structarch.WALK_STRATEGY_POST, callback)
}

func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) WalkBreadth(
	callback func(*SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) (skip, stop bool),
) error {
	return n.Walk(structarch.WALK_STRATEGY_BREADTH, callback)
}

/*
FindFirst traverses the subtree and returns the first node
satisfying the predicate.

Traversal order: pre-order (top-down).

Returns nil if no match exists.
*/
func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) FindFirst(
	predicate func(*SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) bool,
) *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind] {

	var found *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]

	_ = n.WalkPre(func(cur *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) (bool, bool) {
		if predicate(cur) {
			found = cur
			return false, true
		}
		return false, false
	})

	return found
}

/*
FindAll returns all nodes in the subtree satisfying the predicate.

Traversal order: pre-order.
*/
func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) FindAll(
	predicate func(*SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) bool,
) []*SyntaxaASTNode[TObs, TToken, TTokenRole, TKind] {

	out := make([]*SyntaxaASTNode[TObs, TToken, TTokenRole, TKind], 0)

	_ = n.WalkPre(func(cur *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) (bool, bool) {
		if predicate(cur) {
			out = append(out, cur)
		}
		return false, false
	})

	return out
}

/*
FindFirstKind returns the first node of the given kind in the subtree.

Returns nil if absent.
*/
func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) FindFirstKind(
	kind TKind,
) *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind] {

	return n.FindFirst(func(cur *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) bool {
		return cur.kind == kind
	})
}

/*
FindAllKind returns all nodes of the given kind in the subtree.
*/
func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) FindAllKind(
	kind TKind,
) []*SyntaxaASTNode[TObs, TToken, TTokenRole, TKind] {

	return n.FindAll(func(cur *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) bool {
		return cur.kind == kind
	})
}

/*
Exists reports whether any node in the subtree satisfies the predicate.
*/
func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) Exists(
	predicate func(*SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) bool,
) bool {
	return n.FindFirst(predicate) != nil
}

/*
Count returns the number of nodes satisfying the predicate.
*/
func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) Count(
	predicate func(*SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) bool,
) int {

	count := 0

	_ = n.WalkPre(func(cur *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) (bool, bool) {
		if predicate(cur) {
			count++
		}
		return false, false
	})

	return count
}

func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) walkChildren() []*SyntaxaASTNode[TObs, TToken, TTokenRole, TKind] {

	total := len(n.children)

	if n.slots != nil {
		total += len(n.slots)
	}

	if total == 0 {
		return nil
	}

	out := make([]*SyntaxaASTNode[TObs, TToken, TTokenRole, TKind], 0, total)
	out = append(out, n.children...)

	for _, ch := range n.slots {
		if ch != nil {
			out = append(out, ch)
		}
	}

	return out
}

type ASTDebugFormatter[TObs cmp.Ordered, TToken, TTokenRole, TKind comparable] struct {

	/* REQUIRED */

	FormatKind func(TKind) string

	/* Optional render hooks */

	FormatToken     func(lexarch.Lexeme[TObs, TToken, TTokenRole]) string
	FormatAttribute func(key string, value any) string

	/* Coloring layer (nil = no color) */

	ColorKind      func(string) string
	ColorToken     func(string) string
	ColorAttribute func(string) string
	ColorSpan      func(string) string

	/* Position rendering */

	ShowByteSpan bool
	ShowLineSpan bool

	/* Structural extras */

	ShowTokens     bool
	ShowAttributes bool
	ShowNodeID     bool
	ShowRevision   bool

	/* Slot styling */

	SlotPrefix string // e.g. "@", "#", "slot:"
}

/*
DebugDump returns a human-readable structural representation of the AST.

All semantic formatting is injected through ASTDebugFormatter to keep
Syntaxa independent of user enum meanings.

Output example:

Root
├─ Stmt
│  └─ BinaryExpr
│     ├─ CallExpr
│     │  └─ Ident(foo)
│     └─ BinaryExpr
│        ├─ Number(2)
│        └─ Number(3)
*/
func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) DebugDump(
	formatter ASTDebugFormatter[TObs, TToken, TTokenRole, TKind],
) string {

	var out strings.Builder

	applyColor := func(s string, f func(string) string) string {
		if f != nil {
			return f(s)
		}
		return s
	}

	formatLine := func(node *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) {

		kind := formatter.FormatKind(node.kind)
		kind = applyColor(kind, formatter.ColorKind)
		out.WriteString(kind)

		if formatter.ShowNodeID {
			out.WriteString(fmt.Sprintf(" #%d", node.id))
		}

		if formatter.ShowRevision {
			out.WriteString(fmt.Sprintf(" r%d", node.revision))
		}

		if formatter.ShowByteSpan {
			s, e := node.Span()
			txt := fmt.Sprintf("[%d:%d]", s, e)
			txt = applyColor(txt, formatter.ColorSpan)
			out.WriteString(" " + txt)
		}

		if formatter.ShowLineSpan {
			sl, sc, el, ec := node.LineSpan()
			txt := fmt.Sprintf("(%d:%d → %d:%d)", sl, sc, el, ec)
			txt = applyColor(txt, formatter.ColorSpan)
			out.WriteString(" " + txt)
		}

		if formatter.ShowTokens && formatter.FormatToken != nil && len(node.tokens) > 0 {
			out.WriteString(" {")
			for i, t := range node.tokens {
				if i > 0 {
					out.WriteString(", ")
				}
				txt := formatter.FormatToken(t)
				txt = applyColor(txt, formatter.ColorToken)
				out.WriteString(txt)
			}
			out.WriteString("}")
		}

		if formatter.ShowAttributes && formatter.FormatAttribute != nil && len(node.attributes) > 0 {
			out.WriteString(" <")
			first := true

			attributes := extensions.MapSortFunc(node.attributes, func(pairA, pairB extensions.KeyValuePair[string, any]) int {
				return cmp.Compare(pairA.Key, pairB.Key)
			})

			for _, attribute := range attributes {
				if !first {
					out.WriteString(", ")
				}
				first = false
				txt := formatter.FormatAttribute(attribute.Key, attribute.Value)
				txt = applyColor(txt, formatter.ColorAttribute)
				out.WriteString(txt)
			}
			out.WriteString(">")
		}

		out.WriteByte('\n')
	}

	// ------------------------------------------------------------
	// Prefix helpers
	// ------------------------------------------------------------

	writePrefix := func(prefix string, isLast bool, depth int) {
		// Root itself is printed without tree glyphs.
		if depth == 0 {
			return
		}
		if isLast {
			out.WriteString(prefix + "└─ ")
		} else {
			out.WriteString(prefix + "├─ ")
		}
	}

	nextPrefix := func(prefix string, isLast bool, depth int) string {
		// After the root level, we maintain the vertical guides.
		// For the immediate children of root (depth==1), prefix is "".
		if depth == 0 {
			return ""
		}
		if isLast {
			return prefix + "   "
		}
		return prefix + "│  "
	}

	// ------------------------------------------------------------
	// Unified child enumeration (children + slots)
	// ------------------------------------------------------------

	type edge struct {
		isSlot bool
		name   string
		node   *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]
	}

	collectEdges := func(cur *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) []edge {
		total := len(cur.children)
		if cur.slots != nil {
			total += len(cur.slots)
		}
		if total == 0 {
			return nil
		}

		edges := make([]edge, 0, total)

		for _, ch := range cur.children {
			edges = append(edges, edge{node: ch})
		}

		if cur.slots != nil {
			slots := extensions.MapSortFunc(cur.slots, func(pairA, pairB extensions.KeyValuePair[string, *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]]) int {
				return cmp.Compare(pairA.Key, pairB.Key)
			})

			for _, slot := range slots {
				if slot.Value == nil {
					continue
				}
				edges = append(edges, edge{isSlot: true, name: slot.Key, node: slot.Value})
			}
		}

		return edges
	}

	// ------------------------------------------------------------
	// Recursive walker
	// ------------------------------------------------------------

	var walk func(
		node *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
		prefix string,
		isLast bool,
		depth int,
	)

	walk = func(
		node *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
		prefix string,
		isLast bool,
		depth int,
	) {
		writePrefix(prefix, isLast, depth)
		formatLine(node)

		edges := collectEdges(node)
		if len(edges) == 0 {
			return
		}

		childPrefix := nextPrefix(prefix, isLast, depth)

		for i, e := range edges {
			last := i == len(edges)-1

			if !e.isSlot {
				walk(e.node, childPrefix, last, depth+1)
				continue
			}

			// Slot edge: render label + node line, then recurse into slot node's children.
			writePrefix(childPrefix, last, depth+1)

			label := formatter.SlotPrefix + e.name
			label = applyColor(label, formatter.ColorAttribute)
			out.WriteString(label + " → ")

			// Slot target printed on same line (no extra prefix)
			formatLine(e.node)

			// Recurse into the slot node's children with appropriate prefix.
			grand := collectEdges(e.node)
			if len(grand) == 0 {
				continue
			}

			grandPrefix := childPrefix
			if last {
				grandPrefix += "   "
			} else {
				grandPrefix += "│  "
			}

			for j, g := range grand {
				gLast := j == len(grand)-1
				if !g.isSlot {
					walk(g.node, grandPrefix, gLast, depth+2)
				} else {
					// nested slot-of-slot
					writePrefix(grandPrefix, gLast, depth+2)
					lbl := formatter.SlotPrefix + g.name
					lbl = applyColor(lbl, formatter.ColorAttribute)
					out.WriteString(lbl + " → ")
					formatLine(g.node)
				}
			}
		}
	}

	// ------------------------------------------------------------
	// Emit root + children
	// ------------------------------------------------------------

	formatLine(n)

	rootEdges := collectEdges(n)
	for i, e := range rootEdges {
		last := i == len(rootEdges)-1

		if !e.isSlot {
			// Root's children are depth==1 so they get connectors.
			walk(e.node, "", last, 1)
			continue
		}

		// Root slot
		writePrefix("", last, 1)

		label := formatter.SlotPrefix + e.name
		label = applyColor(label, formatter.ColorAttribute)
		out.WriteString(label + " → ")
		formatLine(e.node)
	}

	return out.String()
}

/*
ASTEditor is the exclusive authority for creating and mutating
AST structure.

All topology changes are invariant-checked and automatically
propagate span and incremental revision updates.

Once frozen, the AST becomes immutable.
*/
type ASTEditor[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind comparable] struct {
	nextID uint64
	root   *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind]
	frozen bool

	inUse atomic.Bool
}

func (e *ASTEditor[TObservation, TToken, TTokenRole, TNodeKind]) begin() {
	if e.inUse.Load() {
		panic("ASTEditor reused concurrently or across parses")
	}

	e.inUse.Store(true)
}

/*
NewNode creates a detached AST node of the specified kind.
*/
func (e *ASTEditor[TObs, TToken, TTokenRole, TKind]) NewNode(kind TKind) *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind] {
	e.ensureMutable()

	e.nextID++

	return &SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]{
		id:   e.nextID,
		kind: kind,
	}
}

func (e *ASTEditor[TObs, TToken, TTokenRole, TKind]) AttachChild(parent, child *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) {
	e.ensureMutable()

	if child.parent != nil {
		panic("AST invariant: node already has parent")
	}

	for p := parent; p != nil; p = p.parent {
		if p == child {
			panic("AST invariant: cycle detected")
		}
	}

	child.parent = parent
	parent.children = append(parent.children, child)

	e.markDirtyCascade(parent)
	e.recomputeSpanUp(parent)
}

func (e *ASTEditor[TObs, TToken, TTokenRole, TKind]) SetSlot(parent *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind], name string, child *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) {
	e.ensureMutable()

	if child.parent != nil {
		panic("AST invariant: node already has parent")
	}

	if parent.slots == nil {
		parent.slots = make(map[string]*SyntaxaASTNode[TObs, TToken, TTokenRole, TKind])
	}

	if old := parent.slots[name]; old != nil {
		old.parent = nil
	}

	parent.slots[name] = child
	child.parent = parent

	e.markDirtyCascade(parent)
	e.recomputeSpanUp(parent)
}

func (e *ASTEditor[TObs, TToken, TTokenRole, TKind]) Replace(oldNode, newNode *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) {
	e.ensureMutable()

	parent := oldNode.parent
	if parent == nil {
		panic("cannot replace root")
	}

	if newNode.parent != nil {
		panic("replacement already has parent")
	}

	for i, ch := range parent.children {
		if ch == oldNode {
			parent.children[i] = newNode
			newNode.parent = parent
			oldNode.parent = nil
			e.markDirtyCascade(parent)
			e.recomputeSpanUp(parent)
			return
		}
	}

	for k, v := range parent.slots {
		if v == oldNode {
			parent.slots[k] = newNode
			newNode.parent = parent
			oldNode.parent = nil
			e.markDirtyCascade(parent)
			e.recomputeSpanUp(parent)
			return
		}
	}

	panic("node not owned by parent")
}

func (e *ASTEditor[TObs, TToken, TTokenRole, TKind]) Detach(node *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) {
	e.ensureMutable()

	parent := node.parent
	if parent == nil {
		return
	}

	for i, ch := range parent.children {
		if ch == node {
			parent.children = append(parent.children[:i], parent.children[i+1:]...)
			node.parent = nil
			e.markDirtyCascade(parent)
			return
		}
	}

	for k, v := range parent.slots {
		if v == node {
			delete(parent.slots, k)
			node.parent = nil
			e.markDirtyCascade(parent)
			return
		}
	}

	e.recomputeSpanUp(parent)
}

func (e *ASTEditor[TObs, TToken, TTokenRole, TKind]) SetAttribute(node *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind], key string, value any) {
	e.ensureMutable()

	if node.attributes == nil {
		node.attributes = make(map[string]any)
	}
	node.attributes[key] = value
	e.markDirtyCascade(node)
}

func (e *ASTEditor[TObs, TToken, TTokenRole, TKind]) DeleteAttribute(node *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind], key string) {
	e.ensureMutable()

	if node.attributes == nil {
		return
	}
	delete(node.attributes, key)
	e.markDirtyCascade(node)
}

func (e *ASTEditor[TObs, TToken, TTokenRole, TKind]) AddToken(
	node *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
	tok lexarch.Lexeme[TObs, TToken, TTokenRole],
) {
	e.ensureMutable()

	node.tokens = append(node.tokens, tok)

	// ─────────────────────────────────────────────
	// Establish or widen span from token
	// ─────────────────────────────────────────────

	if len(node.tokens) == 1 {
		node.start = tok.Start
		node.end = tok.End

		node.startLine = tok.StartLine
		node.startColumn = tok.StartColumn
		node.endLine = tok.EndLine
		node.endColumn = tok.EndColumn
	} else {
		if tok.Start < node.start {
			node.start = tok.Start
			node.startLine = tok.StartLine
			node.startColumn = tok.StartColumn
		}

		if tok.End > node.end {
			node.end = tok.End
			node.endLine = tok.EndLine
			node.endColumn = tok.EndColumn
		}
	}

	// ─────────────────────────────────────────────
	// Propagate structural invariants upward
	// ─────────────────────────────────────────────

	e.markDirtyCascade(node)
	e.recomputeSpanUp(node)
}

func (e *ASTEditor[TObs, TToken, TTokenRole, TKind]) SetTokens(node *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind], toks []lexarch.Lexeme[TObs, TToken, TTokenRole]) {
	e.ensureMutable()

	node.tokens = make([]lexarch.Lexeme[TObs, TToken, TTokenRole], len(toks))
	copy(node.tokens, toks)

	e.markDirtyCascade(node)
}

func (e *ASTEditor[TObs, TToken, TTokenRole, TKind]) SetSpan(
	node *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
	start, end int,
) {
	e.ensureMutable()

	node.start = start
	node.end = end

	e.markDirtyCascade(node)
}

func (e *ASTEditor[TObs, TToken, TTokenRole, TKind]) SetLineSpan(
	node *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
	sl, sc, el, ec int,
) {
	e.ensureMutable()

	node.startLine = sl
	node.startColumn = sc
	node.endLine = el
	node.endColumn = ec

	e.markDirtyCascade(node)
}

func (e *ASTEditor[TObs, TToken, TTokenRole, TKind]) markDirtyCascade(n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) {
	for cur := n; cur != nil; cur = cur.parent {
		cur.revision++
	}
}

func (e *ASTEditor[TObs, TToken, TTokenRole, TKind]) recomputeSpanUp(n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) {
	for cur := n; cur != nil; cur = cur.parent {
		e.recomputeSpan(cur)
	}
}

func (e *ASTEditor[TObs, TToken, TTokenRole, TKind]) recomputeSpan(
	n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
) {
	first := true

	var (
		minStart int
		maxEnd   int

		startLine   int
		startColumn int
		endLine     int
		endColumn   int
	)

	applyNode := func(ch *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) {
		if ch == nil {
			return
		}

		if first {
			first = false

			minStart = ch.start
			maxEnd = ch.end

			startLine = ch.startLine
			startColumn = ch.startColumn
			endLine = ch.endLine
			endColumn = ch.endColumn
			return
		}

		// ---- byte span ----

		if ch.start < minStart {
			minStart = ch.start
		}
		if ch.end > maxEnd {
			maxEnd = ch.end
		}

		// ---- start position ----

		if ch.startLine < startLine ||
			(ch.startLine == startLine && ch.startColumn < startColumn) {

			startLine = ch.startLine
			startColumn = ch.startColumn
		}

		// ---- end position ----

		if ch.endLine > endLine ||
			(ch.endLine == endLine && ch.endColumn > endColumn) {

			endLine = ch.endLine
			endColumn = ch.endColumn
		}
	}

	applyToken := func(tok lexarch.Lexeme[TObs, TToken, TTokenRole]) {
		if first {
			first = false

			minStart = tok.Start
			maxEnd = tok.End

			startLine = tok.StartLine
			startColumn = tok.StartColumn
			endLine = tok.EndLine
			endColumn = tok.EndColumn
			return
		}

		// ---- byte span ----

		if tok.Start < minStart {
			minStart = tok.Start
		}
		if tok.End > maxEnd {
			maxEnd = tok.End
		}

		// ---- start position ----

		if tok.StartLine < startLine ||
			(tok.StartLine == startLine && tok.StartColumn < startColumn) {

			startLine = tok.StartLine
			startColumn = tok.StartColumn
		}

		// ---- end position ----

		if tok.EndLine > endLine ||
			(tok.EndLine == endLine && tok.EndColumn > endColumn) {

			endLine = tok.EndLine
			endColumn = tok.EndColumn
		}
	}

	// ─────────────────────────────────────
	// Union all span contributors
	// ─────────────────────────────────────

	for _, tok := range n.tokens {
		applyToken(tok)
	}

	for _, ch := range n.children {
		applyNode(ch)
	}

	for _, ch := range n.slots {
		applyNode(ch)
	}

	if first {
		return // no span contributors
	}

	n.start = minStart
	n.end = maxEnd

	n.startLine = startLine
	n.startColumn = startColumn
	n.endLine = endLine
	n.endColumn = endColumn
}

func (e *ASTEditor[TObs, TToken, TTokenRole, TKind]) Freeze() {
	e.frozen = true
}

func (e *ASTEditor[TObs, TToken, TTokenRole, TKind]) ensureMutable() {
	if e.frozen {
		panic("AST is frozen and immutable")
	}
}

// =============================================================
// RULES
// =============================================================

/* RuleResult encapsulates the return value of a parser rule. */
type RuleResult[TObs cmp.Ordered, TToken, TTokenRole, TKind comparable] struct {
	Node     *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]
	TopLevel bool
}

/*
ParserRule attempts to parse input at the current cursor position.

Contract:
  - On success: returns (node, true) and commits consumption
  - On failure: returns (nil, false); consumption is rolled back
*/
type ParserRule[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
] func(
	ctx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) (RuleResult[TObservation, TToken, TTokenRole, TNodeKind], bool)

/*
RuleSelector is client-owned logic that determines which rule
should be attempted at the current position.

Returning nil indicates that no rule applies and triggers recovery.
*/
type RuleSelector[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
] func(
	ctx SelectRuleContext[TObservation, TToken, TTokenRole],
) ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]

// =============================================================
// ERRORS
// =============================================================

/*
SyntaxError represents a single syntax error.
*/
type SyntaxError struct {
	Message string
	Line    int
	Column  int
}

func (e SyntaxError) Format() string {
	return fmt.Sprintf("syntax error at %d:%d: %s", e.Line, e.Column, e.Message)
}

/*
SyntaxErrors aggregates syntax errors produced during parsing.
*/
type SyntaxErrors struct {
	Errors []SyntaxError
}

func (s *SyntaxErrors) HasErrors() bool {
	return len(s.Errors) > 0
}

// =============================================================
// PARSER
// =============================================================

/*
SyntaxaParser is a grammar-agnostic parsing engine.

It imposes no parsing paradigm (LL, LR, Pratt, PEG, etc.).

Responsibilities:
  - transactional rule execution
  - centralized error recovery
  - AST assembly
*/
type SyntaxaParser[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable] struct {
	selectRule RuleSelector[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]

	eofToken TToken

	rootNodeKind  TNodeKind
	errorNodeKind TNodeKind

	freezeAfterParse bool
}

/*
SyntaxaParserCreate constructs a new parser instance.
*/
func SyntaxaParserCreate[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable](
	selectRule RuleSelector[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	eofToken TToken,
	rootNodeKind, errorNodeKind TNodeKind,
	freezeAfterParse bool,
) *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState] {
	return &SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]{
		selectRule:       selectRule,
		eofToken:         eofToken,
		rootNodeKind:     rootNodeKind,
		errorNodeKind:    errorNodeKind,
		freezeAfterParse: freezeAfterParse,
	}
}

/*
SyntaxaParserParseASTSimple constructs an AST from a linear
sequence of lexemes.

This is intended for non-streaming scenarios.

Advanced integrations should build custom RuleContexts and call
SyntaxaParserParseWithContext directly.
*/
func SyntaxaParserParseASTSimple[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	lexemes []lexarch.Lexeme[TObservation, TToken, TTokenRole],
) (*SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind], *SyntaxErrors) {
	root := &SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind]{
		kind:     parser.rootNodeKind,
		children: make([]*SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind], 0),
	}

	errors := &SyntaxErrors{Errors: make([]SyntaxError, 0)}

	cursor := 0

	ctx := BuildExecRuleContextFromSlice(
		parser,
		lexemes,
		errors,
		&cursor,
	)

	parseWithContext(
		parser,
		ctx,
		root,
	)

	return root, errors
}

/*
SyntaxaParserParseWithContext drives parsing using a fully
configured RuleContext.

This is the advanced entry point for custom lexer integrations,
streaming scenarios, incremental systems, and complex pipelines.

The caller is responsible for constructing a valid RuleContext
and providing an initialized root node.

Typical use cases:
  - parsing directly from lexer sessions
  - online/streaming parsing
  - multi-stage lexing pipelines
  - editor-driven incremental parsing
  - custom recovery strategies

This function guarantees:
  - transactional rule execution
  - centralized error recovery
  - consistent AST assembly
*/
func SyntaxaParserParseWithContext[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	ctx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	root *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
) {
	parseWithContext(parser, ctx, root)
}

// =============================================================
// CONTEXT BUILDERS (SHARED CORE)
// =============================================================

func buildBaseContext[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	errors *SyntaxErrors,
	errorNodeKind TNodeKind,
) ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	recoveryStack := make([][]TToken, 0)
	skipTokensStack := make([][]TTokenRole, 0)

	editor := &ASTEditor[TObservation, TToken, TTokenRole, TNodeKind]{}
	editor.begin()

	return ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{

		// ====================================================
		// Error reporting
		// ====================================================

		Report: func(line, column int, description string) {
			errors.Errors = append(errors.Errors, SyntaxError{
				Message: description,
				Line:    line,
				Column:  column,
			})
		},

		// ====================================================
		// AST system
		// ====================================================

		Editor: editor,

		CreateErrorNode: func(message string) *SyntaxaASTNode[
			TObservation, TToken, TTokenRole, TNodeKind,
		] {
			n := editor.NewNode(errorNodeKind)
			editor.SetAttribute(n, "error", message)
			return n
		},

		// ====================================================
		// Recovery stack
		// ====================================================

		PushRecovery: func(tokens ...TToken) {
			cp := make([]TToken, len(tokens))
			copy(cp, tokens)
			recoveryStack = append(recoveryStack, cp)
		},

		PopRecovery: func() {
			if len(recoveryStack) > 0 {
				recoveryStack = recoveryStack[:len(recoveryStack)-1]
			}
		},

		CurrentRecovery: func() []TToken {
			if len(recoveryStack) == 0 {
				return nil
			}
			return recoveryStack[len(recoveryStack)-1]
		},

		// ====================================================
		// Skip-role stack
		// ====================================================

		PushSkipRoles: func(roles ...TTokenRole) {
			cp := make([]TTokenRole, len(roles))
			copy(cp, roles)
			skipTokensStack = append(skipTokensStack, cp)
		},

		PopSkipRoles: func() {
			if len(skipTokensStack) > 0 {
				skipTokensStack = skipTokensStack[:len(skipTokensStack)-1]
			}
		},

		CurrentSkips: func() []TTokenRole {
			if len(skipTokensStack) == 0 {
				return nil
			}
			return skipTokensStack[len(skipTokensStack)-1]
		},
	}
}

func recoverWithContext[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable](
	ctx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	current lexarch.Lexeme[TObservation, TToken, TTokenRole],
	eofToken TToken,
) bool {

	sync := ctx.CurrentRecovery()
	if len(sync) == 0 {
		ctx.Consume()
		return current.Token != eofToken
	}

	for {
		if current.Token == eofToken {
			return false
		}

		for _, t := range sync {
			if current.Token == t {
				return true
			}
		}

		ctx.Consume()
		current = ctx.Peek(0)
	}
}

func parseWithContext[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	execCtx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	root *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
) {
	editor := execCtx.Editor

	if parser.freezeAfterParse {
		defer editor.Freeze()
	}

	lastCursor := -1

	for {
		current := execCtx.Peek(0)

		if current.Token == parser.eofToken {
			return
		}

		start := execCtx.Save().tokenIndex

		rule := parser.selectRule(execCtx.selectCTX())

		// ====================================================
		// No rule matched → structured error node
		// ====================================================

		if rule == nil {
			execCtx.Report(
				current.StartLine,
				current.StartColumn,
				fmt.Sprintf("unexpected token %v", current.Token),
			)

			errNode := execCtx.CreateErrorNode("unexpected token")
			errNode.tokens = append(errNode.tokens, current)

			execCtx.Editor.AttachChild(root, errNode)

			if !recoverWithContext(execCtx, current, parser.eofToken) {
				return
			}

			if start == lastCursor {
				execCtx.Consume()
			}

			lastCursor = start
			continue
		}

		// ====================================================
		// Try rule transactionally
		// ====================================================

		snapshot := execCtx.Save()

		result, ok := rule(execCtx)

		if !ok {
			execCtx.Restore(snapshot)

			if !recoverWithContext(execCtx, current, parser.eofToken) {
				return
			}

			if snapshot.tokenIndex == lastCursor {
				execCtx.Consume()
			}

			lastCursor = snapshot.tokenIndex
			continue
		}

		// ====================================================
		// Successful production
		// ====================================================

		if result.TopLevel {
			editor.AttachChild(root, result.Node)
		}

		lastCursor = -1
	}
}
