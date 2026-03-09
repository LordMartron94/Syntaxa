package syntaxa

import (
	"cmp"
	"fmt"
	"lexarch"
	"strings"
	"structarch"
)

/*
SyntaxaLSTNode represents a node in a lossless syntax tree (LST).

The tree structure is determined by grammar and semantic rules; tokens and
spans are preserved for tooling, diffing, and incremental parsing. This is
not an abstract syntax tree (AST), which would drop syntax and carry only
semantics. The node is a purely structural intermediate representation (IR);
all semantic meaning is defined externally via node kinds and attached metadata.

The node is designed to support:
  - precise source mapping
  - incremental parsing and diffing
  - semantic annotation passes
  - format-preserving transformations
  - efficient tree navigation
*/
type SyntaxaLSTNode[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind comparable] struct {

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

	spanValid bool

	// ---------------------------------------------------------
	// STRUCTURAL RELATIONSHIPS
	// ---------------------------------------------------------

	parent   *SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind]
	children []*SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind]
	slots    map[string]*SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind]

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

	postProcessed bool
}

func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) ID() uint64 {
	return n.id
}

func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) Kind() TKind {
	return n.kind
}

func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) Parent() *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind] {
	return n.parent
}

func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) GetContent(
	sep string,
) string {

	if len(n.tokens) == 0 {
		return ""
	}

	var b strings.Builder

	for i, lex := range n.tokens {
		if i > 0 {
			b.WriteString(sep)
		}
		b.WriteString(lex.FormatRawDiagnostic())
	}

	return b.String()
}

/* Children returns a defensive copy of child nodes. */
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) Children() []*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind] {
	out := make([]*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind], len(n.children))
	copy(out, n.children)
	return out
}

/* Slot retrieves a node assigned to a named slot or nil. */
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) Slot(name string) *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind] {
	if n.slots == nil {
		return nil
	}
	return n.slots[name]
}

/* SlotNames returns all defined slot keys. */
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) SlotNames() []string {
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
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) Tokens() []lexarch.Lexeme[TObs, TToken, TTokenRole] {
	out := make([]lexarch.Lexeme[TObs, TToken, TTokenRole], len(n.tokens))
	copy(out, n.tokens)
	return out
}

func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) Span() (int, int) {
	if !n.spanValid {
		panic("Span called without valid span!")
	}

	return n.start, n.end
}

func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) LineSpan() (int, int, int, int) {
	if !n.spanValid {
		panic("LineSpan called without valid span!")
	}

	return n.startLine, n.startColumn, n.endLine, n.endColumn
}

func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) Revision() uint64 {
	return n.revision
}

/* Attribute returns an attribute value and presence flag. */
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) Attribute(key string) (any, bool) {
	if n.attributes == nil {
		return nil, false
	}
	v, ok := n.attributes[key]
	return v, ok
}

func AttributeAs[TObs cmp.Ordered, TToken, TTokenRole, TKind comparable, TAttribute any](
	n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind],
	key string,
) (TAttribute, bool) {
	var zero TAttribute

	v, ok := n.Attribute(key)
	if !ok {
		return zero, false
	}

	casted, ok := v.(TAttribute)
	if !ok {
		return zero, false
	}

	return casted, true
}

/* AttributeKeys returns all attribute keys. */
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) AttributeKeys() []string {
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
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) Walk(
	strategy structarch.StructArchWalkStrategy,
	callback func(
		node *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind],
	) (skipSubtree, stopWalk bool),
) error {
	if n == nil {
		return fmt.Errorf("Walk called on a nil node")
	}

	return structarch.StructArchWalk(
		structarch.WalkConfig[
			*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind],
			uint64,
		]{
			Strategy: strategy,

			ID: func(n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) uint64 {
				return n.id
			},

			Children: func(n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) []*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind] {
				return n.walkChildren()
			},

			Parent: func(n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind] {
				return n.parent
			},

			Callback: callback,
		},
		n,
	)
}

func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) WalkPre(
	callback func(*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) (skip, stop bool),
) error {
	return n.Walk(structarch.WALK_STRATEGY_PRE, callback)
}

func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) WalkPost(
	callback func(*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) (skip, stop bool),
) error {
	return n.Walk(structarch.WALK_STRATEGY_POST, callback)
}

func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) WalkBreadth(
	callback func(*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) (skip, stop bool),
) error {
	return n.Walk(structarch.WALK_STRATEGY_BREADTH, callback)
}

/*
FindFirst traverses the subtree and returns the first node
satisfying the predicate.

Traversal order: pre-order (top-down).

Returns nil if no match exists.
*/
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) FindFirst(
	predicate func(*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) bool,
) *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind] {

	var found *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]

	_ = n.WalkPre(func(cur *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) (bool, bool) {
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
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) FindAll(
	predicate func(*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) bool,
) []*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind] {

	out := make([]*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind], 0)

	_ = n.WalkPre(func(cur *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) (bool, bool) {
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
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) FindFirstKind(
	kind TKind,
) *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind] {

	return n.FindFirst(func(cur *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) bool {
		return cur.kind == kind
	})
}

/*
FindAllKind returns all nodes of the given kind in the subtree.
*/
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) FindAllKind(
	kind TKind,
) []*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind] {

	return n.FindAll(func(cur *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) bool {
		return cur.kind == kind
	})
}

/*
Exists reports whether any node in the subtree satisfies the predicate.
*/
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) Exists(
	predicate func(*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) bool,
) bool {
	return n.FindFirst(predicate) != nil
}

/*
Count returns the number of nodes satisfying the predicate.
*/
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) Count(
	predicate func(*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) bool,
) int {

	count := 0

	_ = n.WalkPre(func(cur *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) (bool, bool) {
		if predicate(cur) {
			count++
		}
		return false, false
	})

	return count
}

func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) walkChildren() []*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind] {

	total := len(n.children)

	if n.slots != nil {
		total += len(n.slots)
	}

	if total == 0 {
		return nil
	}

	out := make([]*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind], 0, total)
	out = append(out, n.children...)

	for _, ch := range n.slots {
		if ch != nil {
			out = append(out, ch)
		}
	}

	return out
}

/*
FindDirectChild inspects only the immediate children and slots.
Returns the first node satisfying the predicate, or nil.
*/
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) FindDirectChild(
	predicate func(*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) bool,
) *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind] {

	for _, child := range n.walkChildren() {
		if predicate(child) {
			return child
		}
	}

	return nil
}

/*
FindAllDirectChildren returns all immediate children satisfying the predicate.
*/
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) FindAllDirectChildren(
	predicate func(*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) bool,
) []*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind] {

	var out []*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]

	for _, child := range n.walkChildren() {
		if predicate(child) {
			out = append(out, child)
		}
	}

	return out
}

/*
FindDirectChildKind returns the first immediate child of the specified kind.
Use this to resolve grammatical unions and structural boundaries.
*/
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) FindDirectChildKind(
	kind TKind,
) *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind] {

	return n.FindDirectChild(func(cur *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) bool {
		return cur.kind == kind
	})
}

/*
HasDirectChildKind reports if an immediate child of the specified kind exists.
*/
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) HasDirectChildKind(
	kind TKind,
) bool {
	return n.FindDirectChildKind(kind) != nil
}
