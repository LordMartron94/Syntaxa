package syntaxa

import (
	"cmp"
	"fmt"
	"lexarch"
	"structarch"
	"sync/atomic"
)

// =============================================================
// PARSING CURSOR
// =============================================================

/*
ParserSnapshot represents a snapshot of parser progress.

It is opaque by design and only meaningful to the RuleContext
implementation that created it.
*/
type ParserSnapshot struct {
	tokenIndex int
	aux        any
}

func (p *ParserSnapshot) Index() int {
	return p.tokenIndex
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

	/* PeekRangeRaw returns upcoming lexemes without consuming input. Skips roles. */
	PeekRangeRaw func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole]

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

	/* ConsumeIf consumes current token iff it matches any provided token. */
	ConsumeIf func(tokens ...TToken) bool

	/* Expect consumes token or reports+fails (or reports+returns false). */
	Expect func(token TToken, message string) bool

	/*
		ConsumeRaw consumes and returns the current lexeme without
		skipping any token roles.
	*/
	ConsumeRaw func() lexarch.Lexeme[TObservation, TToken, TTokenRole]

	/*
		ConsumeRange returns up to `n` upcoming lexemes while
		consuming input.

		Skips roles.
	*/
	ConsumeRange func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole]

	/*
		ConsumeRangeRaw returns up to `n` upcoming lexemes while
		consuming input.
	*/
	ConsumeRangeRaw func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole]

	/*
	   Try executes fn transactionally.

	   On failure:
	     - cursor is restored automatically
	     - returns false

	   On success:
	     - state is committed
	     - returns true
	*/
	Try func(fn func() bool) bool

	/* Optional attempts fn once, never fails */
	Optional func(fn func() bool) bool

	/* ZeroOrMore repeats while fn succeeds */
	ZeroOrMore func(fn func() bool)

	/* OneOrMore repeats at least once */
	OneOrMore func(fn func() bool) bool

	/*
		ExpectOneOf consumes one of provided tokens or reports error.
	*/
	ExpectOneOf func(tokens []TToken, message string) bool

	/* ============================================================
	   Transaction control
	   ============================================================ */

	/*
		save snapshots the current cursor state.
	*/
	save func() ParserSnapshot

	/*
		restore restores a previously saved cursor state.
	*/
	restore func(ParserSnapshot)

	/* ============================================================
	   Error handling
	   ============================================================ */

	/*
		Report records a syntax error at a given source location.
	*/
	Report func(line, column int, description string)

	reportLexerError func(line, column int, description string)

	getErrors func() *SyntaxErrors[TObservation]

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
		currentRecovery returns the active synchronization tokens.
	*/
	currentRecovery func() tokenSet[TToken]

	/*
		PushSkipRoles installs a new local skippable role set.
	*/
	PushSkipRoles func(roles ...TTokenRole)

	/*
		PopSkipRoles removes the most recent skippable role set.
	*/
	PopSkipRoles func()

	currentSkips func() tokenSet[TTokenRole]

	LastLexingError func() *lexarch.LexingError[TObservation, TToken]
}

func (ctx *ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) selectCTX() SelectRuleContext[TObservation, TToken, TTokenRole] {
	return SelectRuleContext[TObservation, TToken, TTokenRole]{
		Peek:         ctx.Peek,
		PeekRaw:      ctx.PeekRaw,
		PeekRange:    ctx.PeekRange,
		PeekRangeRaw: ctx.PeekRangeRaw,
		Match:        ctx.Match,
	}
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

	if child == nil {
		panic("AST invariant: nil node not allowed")
	}

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

type span struct {
	start, end int
	sl, sc     int
	el, ec     int
}

func merge(a, b span) span {
	// byte span
	if b.start < a.start {
		a.start = b.start
	}
	if b.end > a.end {
		a.end = b.end
	}

	// start position
	if b.sl < a.sl || (b.sl == a.sl && b.sc < a.sc) {
		a.sl = b.sl
		a.sc = b.sc
	}

	// end position
	if b.el > a.el || (b.el == a.el && b.ec > a.ec) {
		a.el = b.el
		a.ec = b.ec
	}

	return a
}

func spanFromNode[TObs cmp.Ordered, TToken, TTokenRole, TKind comparable](
	n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
) span {
	return span{
		start: n.start,
		end:   n.end,
		sl:    n.startLine,
		sc:    n.startColumn,
		el:    n.endLine,
		ec:    n.endColumn,
	}
}

func spanFromToken[TObs cmp.Ordered, TToken, TTokenRole comparable](
	t lexarch.Lexeme[TObs, TToken, TTokenRole],
) span {
	return span{
		start: t.Start,
		end:   t.End,
		sl:    t.StartLine,
		sc:    t.StartColumn,
		el:    t.EndLine,
		ec:    t.EndColumn,
	}
}

func (e *ASTEditor[TObs, TToken, TTokenRole, TKind]) recomputeSpan(
	n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
) {
	var (
		acc   span
		first = true
	)

	apply := func(s span) {
		if first {
			acc = s
			first = false
			return
		}
		acc = merge(acc, s)
	}

	for _, tok := range n.tokens {
		apply(spanFromToken(tok))
	}

	for _, ch := range n.children {
		if ch != nil {
			apply(spanFromNode(ch))
		}
	}

	for _, ch := range n.slots {
		if ch != nil {
			apply(spanFromNode(ch))
		}
	}

	if first {
		return // no contributors
	}

	n.start = acc.start
	n.end = acc.end
	n.startLine = acc.sl
	n.startColumn = acc.sc
	n.endLine = acc.el
	n.endColumn = acc.ec
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
type SyntaxError[TObservation cmp.Ordered] struct {
	ProducedByLexer bool

	Message string

	Line   int
	Column int

	AbsolutePosition int
	TokenNumber      int

	Expected [][]TObservation
	Found    *TObservation
}

/*
SyntaxErrors aggregates syntax errors produced during parsing.
*/
type SyntaxErrors[TObservation cmp.Ordered] struct {
	Errors []SyntaxError[TObservation]

	stack []errorFrame[TObservation]
}

type errorFrame[TObservation cmp.Ordered] struct {
	best                 *SyntaxError[TObservation]
	bestAbsolutePosition int
}

func SyntaxErrorsCreate[TObservation cmp.Ordered]() *SyntaxErrors[TObservation] {
	return &SyntaxErrors[TObservation]{
		Errors: make([]SyntaxError[TObservation], 0),
		stack:  make([]errorFrame[TObservation], 0),
	}
}

func (s *SyntaxErrors[_]) HasErrors() bool {
	return len(s.Errors) > 0
}

func (s *SyntaxErrors[TObservation]) PushFrame() {
	s.stack = append(s.stack, errorFrame[TObservation]{})
}

func (s *SyntaxErrors[TObservation]) PopFrame(commit bool) {
	if len(s.stack) == 0 {
		panic("SyntaxErrors: PopFrame without PushFrame")
	}

	top := s.stack[len(s.stack)-1]
	s.stack = s.stack[:len(s.stack)-1]

	if commit {
		if top.best != nil {
			s.commitCandidate(*top.best)
		}
		return
	}

	if top.best != nil {
		s.commitCandidate(*top.best)
	}
}

func (s *SyntaxErrors[TObservation]) commitCandidate(err SyntaxError[TObservation]) {
	if len(s.stack) > 0 {
		f := &s.stack[len(s.stack)-1]

		if betterError(f, err) {
			f.best = &err
			f.bestAbsolutePosition = err.AbsolutePosition
		}
		return
	}

	s.Errors = append(s.Errors, err)
}

func (s *SyntaxErrors[TObservation]) report(err SyntaxError[TObservation]) {
	if len(s.stack) == 0 {
		s.Errors = append(s.Errors, err)
		return
	}

	f := &s.stack[len(s.stack)-1]

	if betterError(f, err) {
		f.best = &err
		f.bestAbsolutePosition = err.AbsolutePosition
	}
}

func betterError[TObservation cmp.Ordered](
	cur *errorFrame[TObservation],
	err SyntaxError[TObservation],
) bool {
	if cur.best == nil {
		return true
	}

	if err.AbsolutePosition > cur.bestAbsolutePosition {
		return true
	}

	if err.AbsolutePosition == cur.bestAbsolutePosition &&
		cur.best.ProducedByLexer &&
		!err.ProducedByLexer {
		return true
	}

	return false
}

// =============================================================
// PARSER
// =============================================================

type ParseTraceEvent[TToken any] struct {
	Cursor        int
	RawToken      TToken
	LogicalToken  TToken
	RuleSelected  bool
	RuleSucceeded bool
	Consumed      bool
	TopLevel      bool
	NodeReturned  bool
	RootRevision  uint64
}

type ParseTrace[TToken any] struct {
	Events []ParseTraceEvent[TToken]
}

type ParseResult[TObs cmp.Ordered, TToken, TTokenRole, TKind comparable] struct {
	Root   *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]
	Errors *SyntaxErrors[TObs]
	Trace  *ParseTrace[TToken]
}

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

	defaultSkipRoles []TTokenRole

	tokenFormatter       func(token TToken) string
	observationFormatter lexarch.ObservationFormatter[TObservation]

	eofToken TToken

	rootNodeKind  TNodeKind
	errorNodeKind TNodeKind

	freezeAfterParse bool
	debugTrace       bool
	lastTrace        *ParseTrace[TToken]
}

/*
SyntaxaParserCreate constructs a new parser instance.
*/
func SyntaxaParserCreate[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable](
	selectRule RuleSelector[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	tokenFormatter func(token TToken) string,
	observationFormatter lexarch.ObservationFormatter[TObservation],
	eofToken TToken,
	rootNodeKind, errorNodeKind TNodeKind,
	freezeAfterParse bool,
) *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState] {
	return &SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]{
		selectRule:           selectRule,
		tokenFormatter:       tokenFormatter,
		observationFormatter: observationFormatter,
		eofToken:             eofToken,
		rootNodeKind:         rootNodeKind,
		errorNodeKind:        errorNodeKind,
		freezeAfterParse:     freezeAfterParse,
		defaultSkipRoles:     nil,
	}
}

func (p *SyntaxaParser[_, _, TTokenRole, _, _]) SetDefaultSkips(roles ...TTokenRole) {
	p.defaultSkipRoles = append([]TTokenRole(nil), roles...)
}

func (p *SyntaxaParser[_, _, TTokenRole, _, _]) GetDefaultSkips() []TTokenRole {
	return p.defaultSkipRoles
}

func (p *SyntaxaParser[_, _, _, _, _]) EnableTrace(enable bool) {
	p.debugTrace = enable
}

func (p *SyntaxaParser[_, _, TTokenRole, _, _]) TraceEnabled() bool {
	return p.debugTrace
}

func (p *SyntaxaParser[_, TToken, _, _, _]) LastTrace() *ParseTrace[TToken] {
	if p.lastTrace == nil {
		return nil
	}
	return p.lastTrace
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
func SyntaxaParserParseWithContext[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TNodeKind,
	TLexerState comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	ctx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	root *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
) (*ParseTrace[TToken], error) {
	return parseWithContext(parser, ctx, root)
}

// =============================================================
// RAW SOURCE CORE
// =============================================================

type rawSource[TObs cmp.Ordered, TToken, TTokenRole comparable] struct {
	peek    func(int) lexarch.Lexeme[TObs, TToken, TTokenRole]
	consume func() lexarch.Lexeme[TObs, TToken, TTokenRole]
}

func attachRawSource[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	ctx *ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	src rawSource[TObservation, TToken, TTokenRole],
) {
	// ------------------------------------------------------------
	// Primitive access
	// ------------------------------------------------------------

	ctx.PeekRaw = src.peek
	ctx.ConsumeRaw = src.consume

	// ------------------------------------------------------------
	// Derived range helpers
	// ------------------------------------------------------------

	ctx.PeekRangeRaw = func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		if n <= 0 {
			return nil
		}

		out := make([]lexarch.Lexeme[TObservation, TToken, TTokenRole], 0, n)

		for i := 0; i < n; i++ {
			out = append(out, src.peek(i))
		}

		return out
	}

	ctx.ConsumeRangeRaw = func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		if n <= 0 {
			return nil
		}

		out := make([]lexarch.Lexeme[TObservation, TToken, TTokenRole], 0, n)

		for i := 0; i < n; i++ {
			out = append(out, src.consume())
		}

		return out
	}
}

func BuildExecRuleContextFromSlice[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	lexemes []lexarch.Lexeme[TObservation, TToken, TTokenRole],
	errors *SyntaxErrors[TObservation],
	cursor *int,
) ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	ctx := buildBaseContext(parser, errors)

	eofLex := lexarch.Lexeme[TObservation, TToken, TTokenRole]{
		Token: parser.eofToken,
	}

	attachRawSource(&ctx, rawSource[TObservation, TToken, TTokenRole]{
		peek: func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
			idx := *cursor + n
			if idx >= len(lexemes) {
				return eofLex
			}
			return lexemes[idx]
		},
		consume: func() lexarch.Lexeme[TObservation, TToken, TTokenRole] {
			if *cursor >= len(lexemes) {
				return eofLex
			}

			l := lexemes[*cursor]
			*cursor++
			return l
		},
	})

	ctx.save = func() ParserSnapshot {
		return ParserSnapshot{tokenIndex: *cursor}
	}

	ctx.restore = func(c ParserSnapshot) {
		*cursor = c.tokenIndex
	}

	ctx.LastLexingError = func() *lexarch.LexingError[TObservation, TToken] {
		return nil
	}

	finalizeExecContext(&ctx, parser.eofToken, parser.defaultSkipRoles)
	return ctx
}

func BuildExecRuleContextFromLexerSession[
	TObservation cmp.Ordered,
	TState comparable,
	TToken comparable,
	TTokenRole comparable,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TState],
	lexer *lexarch.Lexer[TObservation, TState, TToken, TTokenRole],
	session *lexarch.LexerSession[TObservation, TState, TToken],
	errors *SyntaxErrors[TObservation],
) ExecRuleContext[TObservation, TToken, TTokenRole, TState, TNodeKind] {
	ctx := buildBaseContext(parser, errors)

	attachRawSource(&ctx, rawSource[TObservation, TToken, TTokenRole]{
		peek: func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
			return lexarch.LexerPeek(lexer, session, n)
		},
		consume: func() lexarch.Lexeme[TObservation, TToken, TTokenRole] {
			return lexarch.LexerConsume(lexer, session)
		},
	})

	ctx.save = func() ParserSnapshot {
		snap := session.Snapshot()
		return ParserSnapshot{
			tokenIndex: snap.Position,
			aux:        snap,
		}
	}

	ctx.restore = func(c ParserSnapshot) {
		snap, ok := c.aux.(lexarch.LexerSessionSnapshot[TState])
		if !ok {
			panic("ParserSnapshot.aux: invalid lexer snapshot")
		}
		session.RestoreSnapshot(snap)
	}

	ctx.SetLexerState = func(state TState) {
		lexarch.LexerSessionSetState(session, state)
	}

	ctx.LastLexingError = func() *lexarch.LexingError[TObservation, TToken] {
		return session.GetLastError()
	}

	finalizeExecContext(&ctx, parser.eofToken, parser.defaultSkipRoles)
	return ctx
}

func BuildExecRuleContextFromStreamingSession[
	TObservation cmp.Ordered,
	TState comparable,
	TToken comparable,
	TTokenRole comparable,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TState],
	lexer *lexarch.Lexer[TObservation, TState, TToken, TTokenRole],
	session *lexarch.StreamingLexerSession[TObservation, TState, TToken],
	errors *SyntaxErrors[TObservation],
) ExecRuleContext[TObservation, TToken, TTokenRole, TState, TNodeKind] {
	ctx := buildBaseContext(parser, errors)

	attachRawSource(&ctx, rawSource[TObservation, TToken, TTokenRole]{
		peek: func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
			return lexarch.LexerPeekStreaming(lexer, session, n)
		},
		consume: func() lexarch.Lexeme[TObservation, TToken, TTokenRole] {
			return lexarch.LexerConsumeStreaming(lexer, session)
		},
	})

	ctx.save = func() ParserSnapshot {
		snap := session.Snapshot()
		return ParserSnapshot{
			tokenIndex: snap.AbsPos,
			aux:        snap,
		}
	}

	ctx.restore = func(c ParserSnapshot) {
		snap, ok := c.aux.(lexarch.StreamingLexerSessionSnapshot[TObservation, TState])
		if !ok {
			panic("ParserSnapshot.aux: invalid streaming snapshot")
		}
		session.RestoreSnapshot(snap)
	}

	ctx.SetLexerState = func(state TState) {
		lexarch.StreamingLexerSessionSetState(session, state)
	}

	ctx.LastLexingError = func() *lexarch.LexingError[TObservation, TToken] {
		return session.GetLastError()
	}

	finalizeExecContext(&ctx, parser.eofToken, parser.defaultSkipRoles)
	return ctx
}

type tokenSet[TToken comparable] map[TToken]struct{}

func buildBaseContext[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	errors *SyntaxErrors[TObservation],
) ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	recoveryStack := make([]tokenSet[TToken], 0)
	skipTokensStack := make([]tokenSet[TTokenRole], 0)

	editor := &ASTEditor[TObservation, TToken, TTokenRole, TNodeKind]{}
	editor.begin()

	return ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{

		// ====================================================
		// Error reporting
		// ====================================================

		Report: func(line, column int, description string) {
			errors.report(SyntaxError[TObservation]{
				Message: description,
				Line:    line,
				Column:  column,
			})
		},

		reportLexerError: func(line, column int, description string) {
			errors.report(SyntaxError[TObservation]{
				Message:         description,
				Line:            line,
				Column:          column,
				ProducedByLexer: true,
			})
		},

		getErrors: func() *SyntaxErrors[TObservation] {
			return errors
		},

		// ====================================================
		// AST system
		// ====================================================

		Editor: editor,

		CreateErrorNode: func(message string) *SyntaxaASTNode[
			TObservation, TToken, TTokenRole, TNodeKind,
		] {
			n := editor.NewNode(parser.errorNodeKind)
			editor.SetAttribute(n, "error", message)
			return n
		},

		// ====================================================
		// Recovery stack
		// ====================================================

		PushRecovery: func(tokens ...TToken) {
			set := make(tokenSet[TToken])
			for _, r := range tokens {
				set[r] = struct{}{}
			}
			recoveryStack = append(recoveryStack, set)
		},

		PopRecovery: func() {
			if len(recoveryStack) > 0 {
				recoveryStack = recoveryStack[:len(recoveryStack)-1]
			}
		},

		currentRecovery: func() tokenSet[TToken] {
			if len(recoveryStack) == 0 {
				return nil
			}
			return recoveryStack[len(recoveryStack)-1]
		},

		// ====================================================
		// Skip-role stack
		// ====================================================

		PushSkipRoles: func(roles ...TTokenRole) {
			set := make(tokenSet[TTokenRole])
			for _, r := range roles {
				set[r] = struct{}{}
			}
			skipTokensStack = append(skipTokensStack, set)
		},

		PopSkipRoles: func() {
			if len(skipTokensStack) > 0 {
				skipTokensStack = skipTokensStack[:len(skipTokensStack)-1]
			}
		},

		currentSkips: func() tokenSet[TTokenRole] {
			if len(skipTokensStack) == 0 {
				return nil
			}
			return skipTokensStack[len(skipTokensStack)-1]
		},
	}
}

func finalizeExecContext[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	ctx *ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	eofToken TToken,
	defaultSkipRoles []TTokenRole,
) {

	isEOF := func(l lexarch.Lexeme[TObservation, TToken, TTokenRole]) bool {
		return l.Token == eofToken
	}

	isSkipped := func(role TTokenRole) bool {
		_, skipped := ctx.currentSkips()[role]
		return skipped
	}

	// ----------------------------------------------------
	// Pure skip-aware peek (NON MUTATING)
	// ----------------------------------------------------

	ctx.Peek = func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		if n < 0 {
			panic("Peek: n must be >= 0")
		}

		seen := 0
		i := 0

		for {
			cur := ctx.PeekRaw(i)

			if isEOF(cur) {
				return cur
			}

			if !isSkipped(cur.Role) {
				if seen == n {
					return cur
				}
				seen++
			}

			i++
		}
	}

	ctx.PeekRange = func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		if n <= 0 {
			return nil
		}

		out := make([]lexarch.Lexeme[TObservation, TToken, TTokenRole], 0, n)

		i := 0
		for len(out) < n {
			cur := ctx.PeekRaw(i)

			if isEOF(cur) {
				break
			}

			if !isSkipped(cur.Role) {
				out = append(out, cur)
			}

			i++
		}

		return out
	}

	// ----------------------------------------------------
	// Actual consumption (ONLY place that mutates)
	// ----------------------------------------------------

	skipForward := func() {
		for {
			cur := ctx.PeekRaw(0)

			if isEOF(cur) || !isSkipped(cur.Role) {
				return
			}

			ctx.ConsumeRaw()
		}
	}

	ctx.Consume = func() lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		skipForward()
		return ctx.ConsumeRaw()
	}

	ctx.ConsumeRange = func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		if n <= 0 {
			return nil
		}

		out := make([]lexarch.Lexeme[TObservation, TToken, TTokenRole], 0, n)

		for len(out) < n {
			cur := ctx.PeekRaw(0)

			if isEOF(cur) {
				break
			}

			if !isSkipped(cur.Role) {
				out = append(out, cur)
			}

			ctx.ConsumeRaw()
		}

		return out
	}

	// ----------------------------------------------------
	// Helpers
	// ----------------------------------------------------

	ctx.ConsumeIf = func(tokens ...TToken) bool {
		skipForward()
		cur := ctx.PeekRaw(0)

		for _, t := range tokens {
			if cur.Token == t {
				ctx.ConsumeRaw()
				return true
			}
		}
		return false
	}

	ctx.Expect = func(token TToken, message string) bool {
		skipForward()
		cur := ctx.PeekRaw(0)

		if cur.Token == token {
			ctx.ConsumeRaw()
			return true
		}

		ctx.Report(cur.StartLine, cur.StartColumn, message)
		return false
	}

	ctx.Match = func(tokens ...TToken) bool {
		cur := ctx.Peek(0)
		for _, t := range tokens {
			if cur.Token == t {
				return true
			}
		}
		return false
	}

	// ----------------------------------------------------
	// Transactional helpers
	// ----------------------------------------------------

	ctx.Try = func(fn func() bool) bool {
		errors := ctx.getErrors()
		snap := ctx.save()

		errors.PushFrame()
		ok := fn()
		errors.PopFrame(ok)

		if ok {
			return true
		}

		ctx.restore(snap)
		return false
	}

	ctx.Optional = func(fn func() bool) bool {
		ctx.Try(fn)
		return true
	}

	ctx.ZeroOrMore = func(fn func() bool) {
		for {
			snap := ctx.save()
			if !fn() {
				ctx.restore(snap)
				return
			}
			if ctx.save().tokenIndex == snap.tokenIndex {
				panic("ZeroOrMore: rule succeeded without consuming input")
			}
		}
	}

	ctx.OneOrMore = func(fn func() bool) bool {
		if !ctx.Try(fn) {
			return false
		}
		for ctx.Try(fn) {
		}
		return true
	}

	ctx.ExpectOneOf = func(tokens []TToken, message string) bool {
		skipForward()
		cur := ctx.PeekRaw(0)

		for _, t := range tokens {
			if cur.Token == t {
				ctx.ConsumeRaw()
				return true
			}
		}

		ctx.Report(cur.StartLine, cur.StartColumn, message)
		return false
	}

	if len(defaultSkipRoles) > 0 {
		ctx.PushSkipRoles(defaultSkipRoles...)
	}
}

func recoverWithContext[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable](
	ctx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	current lexarch.Lexeme[TObservation, TToken, TTokenRole],
	eofToken TToken,
) bool {

	sync := ctx.currentRecovery()
	if len(sync) == 0 {
		ctx.Consume()
		return current.Token != eofToken
	}

	for {
		if current.Token == eofToken {
			return false
		}

		_, synced := sync[current.Token]
		if synced {
			return true
		}

		ctx.Consume()
		current = ctx.PeekRaw(0)
	}
}

type parseLoopState struct {
	lastCursor   int
	sawNonEOFRaw bool
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
) (*ParseTrace[TToken], error) {

	editor := execCtx.Editor
	if parser.freezeAfterParse {
		defer editor.Freeze()
	}

	trace := initTrace[TToken](parser.debugTrace)

	state := parseLoopState{}

	for {
		step, done, err := parseIteration(parser, execCtx, root, trace, &state)
		if err != nil {
			return trace, err
		}
		if done {
			break
		}
		state.lastCursor = step
	}

	errors := execCtx.getErrors()

	if err := validateFinalAST(root, errors, state.sawNonEOFRaw); err != nil {
		return trace, err
	}

	return trace, nil
}

func initTrace[TToken any](enabled bool) *ParseTrace[TToken] {
	if !enabled {
		return nil
	}
	return &ParseTrace[TToken]{}
}

func parseIteration[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	execCtx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	root *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
	trace *ParseTrace[TToken],
	state *parseLoopState,
) (cursor int, done bool, err error) {

	rawCurrent, current := peekTokens(execCtx)

	if rawCurrent.Token != parser.eofToken {
		state.sawNonEOFRaw = true
	}

	if handled, done := handleLexingError(execCtx); handled {
		return -1, done, nil
	}

	if current.Token == parser.eofToken {
		return -1, true, nil
	}

	startSnap := execCtx.save()
	startPos := startSnap.tokenIndex

	rule := parser.selectRule(execCtx.selectCTX())

	event := createTraceEvent[TObservation, TToken, TTokenRole, TNodeKind](startPos, rawCurrent, current, rule != nil, root.revision)

	if rule == nil {
		handleNoRule(parser, execCtx, root, trace, event, current, state)
		return startPos, false, nil
	}

	result, endPos, ok := executeRule(execCtx, rule)

	updateTrace(&event, result, ok, startPos, endPos)
	appendTrace(trace, event)

	if !ok {
		execCtx.restore(startSnap)
		handleRecovery(execCtx, current, parser.eofToken, startPos, state)
		return startPos, false, nil
	}

	if err := validateRuleSuccess[TObservation, TToken, TTokenRole, TLexerState](result, startPos, endPos, current); err != nil {
		return -1, true, err
	}

	attachIfTopLevel(execCtx.Editor, root, result)

	state.lastCursor = -1
	return endPos, false, nil
}

func peekTokens[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole comparable,
	TLexerState,
	TNodeKind comparable,
](
	execCtx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) (
	raw lexarch.Lexeme[TObservation, TToken, TTokenRole],
	logical lexarch.Lexeme[TObservation, TToken, TTokenRole],
) {
	raw = execCtx.PeekRaw(0)
	logical = execCtx.Peek(0)
	return
}

func handleLexingError[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole comparable,
	TLexerState,
	TNodeKind comparable,
](
	execCtx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) (handled bool, done bool) {

	if lexErr := execCtx.LastLexingError(); lexErr != nil {
		execCtx.reportLexerError(
			lexErr.StartLine,
			lexErr.StartColumn,
			lexErr.Error(),
		)
		return true, true
	}

	return false, false
}

func executeRule[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole comparable,
	TLexerState,
	TNodeKind comparable,
](
	execCtx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	rule ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) (
	result RuleResult[TObservation, TToken, TTokenRole, TNodeKind],
	endPos int,
	ok bool,
) {
	result, ok = rule(execCtx)
	endPos = execCtx.save().tokenIndex
	return
}

func validateRuleSuccess[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole comparable,
	TLexerState,
	TNodeKind comparable,
](
	result RuleResult[TObservation, TToken, TTokenRole, TNodeKind],
	startPos int,
	endPos int,
	current lexarch.Lexeme[TObservation, TToken, TTokenRole],
) error {

	if endPos == startPos {
		return fmt.Errorf(
			"parser invariant violated: rule succeeded without consuming input at cursor %d (token=%v)",
			startPos,
			current.Token,
		)
	}

	if result.TopLevel && result.Node == nil {
		return fmt.Errorf(
			"parser invariant violated: TopLevel rule returned nil node at cursor %d",
			startPos,
		)
	}

	return nil
}

func attachIfTopLevel[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole comparable,
	TNodeKind comparable,
](
	editor *ASTEditor[TObservation, TToken, TTokenRole, TNodeKind],
	root *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
	result RuleResult[TObservation, TToken, TTokenRole, TNodeKind],
) {
	if result.TopLevel {
		editor.AttachChild(root, result.Node)
	}
}

func validateFinalAST[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole comparable,
	TNodeKind comparable,
](
	root *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
	errors *SyntaxErrors[TObservation],
	sawNonEOFRaw bool,
) error {

	if sawNonEOFRaw && len(root.children) == 0 && !errors.HasErrors() {
		return fmt.Errorf(
			"parser produced empty AST despite non-empty input",
		)
	}
	return nil
}

func createTraceEvent[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole comparable,
	TNodeKind comparable,
](
	cursor int,
	raw lexarch.Lexeme[TObservation, TToken, TTokenRole],
	logical lexarch.Lexeme[TObservation, TToken, TTokenRole],
	ruleSelected bool,
	rootRevision uint64,
) ParseTraceEvent[TToken] {
	return ParseTraceEvent[TToken]{
		Cursor:       cursor,
		RawToken:     raw.Token,
		LogicalToken: logical.Token,
		RuleSelected: ruleSelected,
		RootRevision: rootRevision,
	}
}

func handleNoRule[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole comparable,
	TLexerState,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	execCtx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	root *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
	trace *ParseTrace[TToken],
	event ParseTraceEvent[TToken],
	current lexarch.Lexeme[TObservation, TToken, TTokenRole],
	state *parseLoopState,
) {

	execCtx.Report(
		current.StartLine,
		current.StartColumn,
		"unexpected token",
	)

	errNode := execCtx.CreateErrorNode("unexpected token")
	errNode.tokens = append(errNode.tokens, current)
	execCtx.Editor.AttachChild(root, errNode)

	appendTrace(trace, event)

	handleRecovery(
		execCtx,
		current,
		parser.eofToken,
		event.Cursor,
		state,
	)
}

func updateTrace[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole comparable,
	TNodeKind comparable,
](
	event *ParseTraceEvent[TToken],
	result RuleResult[TObservation, TToken, TTokenRole, TNodeKind],
	ok bool,
	startPos int,
	endPos int,
) {
	event.RuleSucceeded = ok
	event.Consumed = endPos != startPos
	event.TopLevel = result.TopLevel
	event.NodeReturned = result.Node != nil
}

func appendTrace[TToken any](
	trace *ParseTrace[TToken],
	event ParseTraceEvent[TToken],
) {
	if trace != nil {
		trace.Events = append(trace.Events, event)
	}
}

func handleRecovery[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole comparable,
	TLexerState,
	TNodeKind comparable,
](
	execCtx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	current lexarch.Lexeme[TObservation, TToken, TTokenRole],
	eofToken TToken,
	startPos int,
	state *parseLoopState,
) {
	if !recoverWithContext(execCtx, current, eofToken) {
		return
	}

	if startPos == state.lastCursor {
		execCtx.Consume()
	}

	state.lastCursor = startPos
}
