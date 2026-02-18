package syntaxa

import (
	"cmp"
	"lexarch"
	"sync/atomic"
)

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
