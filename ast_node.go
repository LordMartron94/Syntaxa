package syntaxa

import (
	"cmp"
	"fmt"
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

	tokens []Lexeme[TObservation, TToken, TTokenRole]

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

/*
ChildrenUnsafe returns the underlying children slice without copying.

The returned slice MUST be treated as read-only by callers. Its contents and backing storage
may change after editor mutations (attach/detach/replace), so callers must not hold long-lived
references or mutate the slice.
*/
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) ChildrenUnsafe() []*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind] {
	return n.children
}

/*
ForEachChild iterates direct positional children without allocating.

The callback receives each child in stored order. If callback returns false, iteration stops early.
*/
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) ForEachChild(
	callback func(*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) bool,
) {
	for _, child := range n.children {
		if !callback(child) {
			return
		}
	}
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
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) Tokens() []Lexeme[TObs, TToken, TTokenRole] {
	out := make([]Lexeme[TObs, TToken, TTokenRole], len(n.tokens))
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

func (n *SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind]) FullSpan() Span {
	return Span{
		Start:       n.start,
		End:         n.end,
		StartLine:   n.startLine,
		EndLine:     n.endLine,
		StartColumn: n.startColumn,
		EndColumn:   n.endColumn,
	}
}

type Span struct {
	Start       int
	End         int
	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int
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

			ChildrenInto: func(
				n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind],
				out []*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind],
			) []*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind] {
				return n.walkChildrenInto(out)
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

func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) walkChildrenInto(
	out []*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind],
) []*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind] {
	if n == nil {
		return out
	}
	if len(n.children) == 0 && len(n.slots) == 0 {
		return out
	}

	out = append(out, n.children...)

	if len(n.slots) > 0 {
		for _, ch := range n.slots {
			if ch != nil {
				out = append(out, ch)
			}
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
	for _, child := range n.children {
		if predicate(child) {
			return child
		}
	}
	for _, child := range n.slots {
		if child == nil {
			continue
		}
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

	for _, child := range n.children {
		if predicate(child) {
			out = append(out, child)
		}
	}
	for _, child := range n.slots {
		if child == nil {
			continue
		}
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

/*
Unwrap traverses down a chain of single-child wrapper nodes of specific kinds.
It stops and returns the first node that is NOT in the allowed wrappers list,
or the first node that has multiple children.
*/
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) Unwrap(
	allowedWrappers ...TKind,
) *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind] {

	current := n
	wrapperSet := make(map[TKind]struct{}, len(allowedWrappers))
	for _, w := range allowedWrappers {
		wrapperSet[w] = struct{}{}
	}

	for {
		_, isWrapper := wrapperSet[current.kind]
		var onlyChild *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]
		childCount := 0
		for _, child := range current.children {
			childCount++
			if childCount == 1 {
				onlyChild = child
			}
		}
		for _, child := range current.slots {
			if child == nil {
				continue
			}
			childCount++
			if childCount == 1 {
				onlyChild = child
			}
		}

		// If it's not a wrapper, or it branches, stop unwrapping.
		if !isWrapper || childCount != 1 {
			return current
		}

		current = onlyChild
	}
}

/*
FlattenByKind returns a slice of nodes by flattening the subtree when the current
node has the given kind. If the node's kind is not the given kind, returns a
single-element slice containing the node. Otherwise returns the concatenation of
FlattenByKind applied to each child (including slots). Use for associative chains
(e.g. expression lists, alternations).
*/
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) FlattenByKind(
	kind TKind,
) []*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind] {
	stack := make([]*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind], 0, 8)
	out := make([]*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind], 0, 8)
	stack = append(stack, n)

	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if cur.kind != kind {
			out = append(out, cur)
			continue
		}

		directChildren := make(
			[]*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind],
			0,
			len(cur.children)+len(cur.slots),
		)
		for _, ch := range cur.children {
			directChildren = append(directChildren, ch)
		}
		for _, ch := range cur.slots {
			if ch == nil {
				continue
			}
			directChildren = append(directChildren, ch)
		}
		if len(directChildren) == 0 {
			out = append(out, cur)
			continue
		}

		for i := len(directChildren) - 1; i >= 0; i-- {
			stack = append(stack, directChildren[i])
		}
	}

	return out
}

/*
SingleChild returns the single logical child and true if the node has exactly one child
(including slots). Otherwise returns (nil, false).
*/
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) SingleChild() (*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind], bool) {
	var onlyChild *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]
	childCount := 0
	for _, child := range n.children {
		childCount++
		if childCount == 1 {
			onlyChild = child
		}
	}
	for _, child := range n.slots {
		if child == nil {
			continue
		}
		childCount++
		if childCount == 1 {
			onlyChild = child
		}
	}
	if childCount != 1 {
		return nil, false
	}
	return onlyChild, true
}

/*
RequireSingleChild returns the single logical child (including slots). Panics if the node
does not have exactly one child.
*/
func (n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) RequireSingleChild() *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind] {
	child, ok := n.SingleChild()
	if !ok {
		childCount := len(n.children)
		for _, current := range n.slots {
			if current != nil {
				childCount++
			}
		}
		panic(fmt.Sprintf("expected exactly one child, got %d", childCount))
	}
	return child
}
