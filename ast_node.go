package syntaxa

import (
	"cmp"
	"lexarch"
	"structarch"
)

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
