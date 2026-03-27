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
	// treeDirty marks that at least one mutation happened since last full span reconciliation.
	treeDirty bool

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
	e.treeDirty = false

	e.inUse.Store(false)
}

func (e *LSTEditor[TObservation, TToken, TTokenRole, TNodeKind]) setRoot(root *SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind]) {
	e.root = root
	e.treeDirty = true
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

	e.markDirtyPair(child, parent)
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

	e.markDirtyPair(child, parent)
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
			e.markDirtyPair(newNode, parent)
			return
		}
	}

	for k, v := range parent.slots {
		if v == oldNode {
			parent.slots[k] = newNode
			newNode.parent = parent
			oldNode.parent = nil
			e.markDirtyPair(newNode, parent)
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
			e.markDirtyNode(node)
			e.markDirtyNode(parent)
			return
		}
	}

	for k, v := range parent.slots {
		if v == node {
			delete(parent.slots, k)
			node.parent = nil
			e.markDirtyNode(node)
			e.markDirtyNode(parent)
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
	e.markDirtyNode(node)
}

func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) DeleteAttribute(node *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind], key string) {
	e.ensureMutable()

	if node.attributes == nil {
		return
	}
	delete(node.attributes, key)
	e.markDirtyNode(node)
}

func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) AddToken(
	node *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind],
	tok lexarch.Lexeme[TObs, TToken, TTokenRole],
) {
	e.ensureMutable()
	node.tokens = append(node.tokens, tok)
	e.markDirtyNode(node)
}

func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) SetTokens(node *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind], toks []lexarch.Lexeme[TObs, TToken, TTokenRole]) {
	e.ensureMutable()

	node.tokens = make([]lexarch.Lexeme[TObs, TToken, TTokenRole], len(toks))
	copy(node.tokens, toks)

	e.markDirtyNode(node)
}

func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) SetSpan(
	node *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind],
	start, end int,
) {
	e.ensureMutable()

	node.start = start
	node.end = end

	e.markDirtyNode(node)
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

	e.markDirtyNode(node)
}

func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) markDirtyNode(n *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]) {
	if n == nil {
		return
	}
	n.revision++
	n.spanValid = false
	e.treeDirty = true
}

func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) markDirtyPair(
	nodeA *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind],
	nodeB *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind],
) {
	e.markDirtyNode(nodeA)
	if nodeA == nodeB {
		return
	}
	e.markDirtyNode(nodeB)
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
	forceRecompute bool,
) {
	if n == nil {
		return
	}
	if n.spanValid && !forceRecompute {
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
		if ch == nil {
			continue
		}
		e.ensureSpanValid(ch, forceRecompute)
		if !ch.spanValid {
			continue
		}
		acc = merge(acc, spanFromNode(ch), empty)
		empty = false
	}

	// 3. Named Slots
	for _, ch := range n.slots {
		if ch == nil {
			continue
		}
		e.ensureSpanValid(ch, forceRecompute)
		if !ch.spanValid {
			continue
		}
		acc = merge(acc, spanFromNode(ch), empty)
		empty = false
	}

	if !empty {
		n.start, n.end = acc.start, acc.end
		n.startLine, n.startColumn = acc.sl, acc.sc
		n.endLine, n.endColumn = acc.el, acc.ec
		n.spanValid = true
		return
	}
	n.spanValid = false
}

/* ComputeSpans should be called to ensure all spans inside the tree are valid. */
func (e *LSTEditor[TObservation, TToken, TTokenRole, TNodeKind]) ComputeSpans() {
	if e.root == nil {
		panic("editor does not have root set yet")
	}
	if e.treeDirty {
		e.ensureSpanValid(e.root, true)
		e.treeDirty = false
		return
	}
	e.ensureSpanValid(e.root, false)
}

/* Freeze calls editor.end because this is the only place the editor session actually ends. */
func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) Freeze() {
	if e.root != nil {
		e.ensureSpanValid(e.root, true)
		e.treeDirty = false
	}

	e.frozen = true
	e.end()
}

func (e *LSTEditor[TObs, TToken, TTokenRole, TKind]) ensureMutable() {
	if e.frozen {
		panic("LST is frozen and immutable")
	}
}
