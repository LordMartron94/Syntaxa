package syntaxa

import (
	"cmp"
	"lexarch"
	"sync/atomic"
)

/*
LSTEditor is the exclusive authority for creating and mutating
LST (lossless syntax tree) structure.

All topology changes are invariant-checked and automatically
propagate span and incremental revision updates.

Once frozen, the LST becomes immutable.
*/
type LSTEditor[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind comparable] struct {
	nextID uint64
	root   *SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind]
	frozen bool

	created []*SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind]

	inUse atomic.Bool
}

func (e *LSTEditor[TObservation, TToken, TTokenRole, TNodeKind]) begin() {
	if e.inUse.Load() {
		panic("LSTEditor reused concurrently or across parses")
	}

	e.inUse.Store(true)
}

func (e *LSTEditor[TObservation, TToken, TTokenRole, TNodeKind]) end() {
	e.root = nil
	e.nextID = 0
	e.frozen = false

	e.inUse.Store(false)
}

func (e *LSTEditor[TObservation, TToken, TTokenRole, TNodeKind]) setRoot(root *SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind]) {
	e.root = root
}

/*
NewNode creates a detached LST node of the specified kind.
*/
func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) NewNode(kind TKind) *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind] {
	e.ensureMutable()

	e.nextID++

	node := &SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]{
		id:   e.nextID,
		kind: kind,
	}

	e.created = append(e.created, node)

	return node
}

/*
NewTransientNode creates a detached AST node that bypasses global tracking.

It does not receive a unique ID and is not added to the editor's created ledger.
Use this strictly for temporary container nodes (Fragments) that will be unpacked
and discarded before the parsing session ends.
*/
func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) NewTransientNode(kind TKind) *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind] {
	e.ensureMutable()

	return &SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]{
		kind: kind,
	}
}

func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) AttachChild(parent, child *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) {
	e.ensureMutable()

	if child == nil {
		return
	}

	if child.parent != nil {
		panic("LST invariant: node already has parent")
	}

	for p := parent; p != nil; p = p.parent {
		if p == child {
			panic("LST invariant: cycle detected")
		}
	}

	child.parent = parent
	parent.children = append(parent.children, child)

	e.markDirtyCascade(parent)
}

func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) AttachResult(
	parent *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind],
	result RuleResult[TObs, TToken, TTokenRole, TKind],
) {
	if result.Node == nil {
		return
	}

	if result.IsFragment {
		children := result.Node.Children()
		for _, child := range children {
			e.Detach(child)
			e.AttachChild(parent, child)
		}
	} else {
		e.AttachChild(parent, result.Node)
	}
}

func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) SetSlot(parent *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind], name string, child *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) {
	e.ensureMutable()

	if child.parent != nil {
		panic("LST invariant: node already has parent")
	}

	if parent.slots == nil {
		parent.slots = make(map[string]*SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind])
	}

	if old := parent.slots[name]; old != nil {
		old.parent = nil
	}

	parent.slots[name] = child
	child.parent = parent

	e.markDirtyCascade(parent)
}

func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) Replace(oldNode, newNode *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) {
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
			return
		}
	}

	for k, v := range parent.slots {
		if v == oldNode {
			parent.slots[k] = newNode
			newNode.parent = parent
			oldNode.parent = nil
			e.markDirtyCascade(parent)
			return
		}
	}

	panic("node not owned by parent")
}

func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) Detach(node *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) {
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
}

func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) SetAttribute(node *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind], key string, value any) {
	e.ensureMutable()

	if node.attributes == nil {
		node.attributes = make(map[string]any)
	}
	node.attributes[key] = value
	e.markDirtyCascade(node)
}

func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) DeleteAttribute(node *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind], key string) {
	e.ensureMutable()

	if node.attributes == nil {
		return
	}
	delete(node.attributes, key)
	e.markDirtyCascade(node)
}

func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) AddToken(
	node *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind],
	tok lexarch.Lexeme[TObs, TToken, TTokenRole],
) {
	e.ensureMutable()
	node.tokens = append(node.tokens, tok)
	e.markDirtyCascade(node)
}

func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) SetTokens(node *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind], toks []lexarch.Lexeme[TObs, TToken, TTokenRole]) {
	e.ensureMutable()

	node.tokens = make([]lexarch.Lexeme[TObs, TToken, TTokenRole], len(toks))
	copy(node.tokens, toks)

	e.markDirtyCascade(node)
}

func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) SetSpan(
	node *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind],
	start, end int,
) {
	e.ensureMutable()

	node.start = start
	node.end = end

	e.markDirtyCascade(node)
}

func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) SetLineSpan(
	node *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind],
	sl, sc, el, ec int,
) {
	e.ensureMutable()

	node.startLine = sl
	node.startColumn = sc
	node.endLine = el
	node.endColumn = ec

	e.markDirtyCascade(node)
}

func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) markDirtyCascade(n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) {
	for cur := n; cur != nil; cur = cur.parent {
		cur.revision++
		cur.spanValid = false
	}
}

type span struct {
	start, end int
	sl, sc     int
	el, ec     int
}

func merge(base span, next span, isEmpty bool) span {
	if isEmpty {
		return next
	}

	// 1. Widen Byte Span
	if next.start < base.start {
		base.start = next.start
	}
	if next.end > base.end {
		base.end = next.end
	}

	// 2. Widen Start (Lexicographical Min)
	// If next line is earlier, OR same line but earlier column
	if next.sl < base.sl || (next.sl == base.sl && next.sc < base.sc) {
		base.sl = next.sl
		base.sc = next.sc
	}

	// 3. Widen End (Lexicographical Max)
	// If next line is later, OR same line but later column
	if next.el > base.el || (next.el == base.el && next.ec > base.ec) {
		base.el = next.el
		base.ec = next.ec
	}

	return base
}

func spanFromNode[TObs cmp.Ordered, TToken, TTokenRole, TKind comparable](
	n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind],
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

func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) ensureSpanValid(
	n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind],
) {
	if n.spanValid {
		return
	}

	var acc span
	empty := true

	// 1. Tokens
	for _, tok := range n.tokens {
		acc = merge(acc, spanFromToken(tok), empty)
		empty = false
	}

	// 2. Positional Children
	for _, ch := range n.children {
		if ch != nil {
			e.ensureSpanValid(ch) // Recursive depth-first validation
			acc = merge(acc, spanFromNode(ch), empty)
			empty = false
		}
	}

	// 3. Named Slots
	for _, ch := range n.slots {
		if ch != nil {
			e.ensureSpanValid(ch)
			acc = merge(acc, spanFromNode(ch), empty)
			empty = false
		}
	}

	if !empty {
		n.start, n.end = acc.start, acc.end
		n.startLine, n.startColumn = acc.sl, acc.sc
		n.endLine, n.endColumn = acc.el, acc.ec
	}
	n.spanValid = true
}

/* ComputeSpans should be called to ensure all spans inside the tree are valid. */
func (e *LSTEditor[TObservation, TToken, TTokenRole, TNodeKind]) ComputeSpans() {
	if e.root == nil {
		panic("editor does not have root set yet")
	}

	e.ensureSpanValid(e.root)
}

/* Freeze calls editor.end because this is the only place the editor session actually ends. */
func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) Freeze() {
	if e.root != nil {
		e.ensureSpanValid(e.root)
	}

	e.frozen = true
	e.end()
}

func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) ensureMutable() {
	if e.frozen {
		panic("LST is frozen and immutable")
	}
}
