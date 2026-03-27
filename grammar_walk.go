package syntaxa

import (
	"fmt"

	"structarch"
)

/*
Walk traverses the grammar tree starting at this node using the selected strategy.

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
func (g *Grammar[TToken, TNodeKind]) Walk(
	strategy structarch.StructArchWalkStrategy,
	callback func(
		node *Grammar[TToken, TNodeKind],
	) (skipSubtree, stopWalk bool),
) error {
	if g == nil {
		return fmt.Errorf("Walk called on a nil grammar node")
	}

	return structarch.StructArchWalk(
		structarch.WalkConfig[
			*Grammar[TToken, TNodeKind],
			*Grammar[TToken, TNodeKind],
		]{
			Strategy: strategy,

			ID: func(n *Grammar[TToken, TNodeKind]) *Grammar[TToken, TNodeKind] {
				return n
			},

			ChildrenInto: func(
				n *Grammar[TToken, TNodeKind],
				out []*Grammar[TToken, TNodeKind],
			) []*Grammar[TToken, TNodeKind] {
				return grammarWalkChildrenInto(n, out)
			},

			Callback: callback,
		},
		g,
	)
}

/*
WalkPre traverses the grammar tree in pre-order (top-down).
*/
func (g *Grammar[TToken, TNodeKind]) WalkPre(
	callback func(*Grammar[TToken, TNodeKind]) (skip, stop bool),
) error {
	return g.Walk(structarch.WALK_STRATEGY_PRE, callback)
}

/*
WalkPost traverses the grammar tree in post-order (bottom-up).
*/
func (g *Grammar[TToken, TNodeKind]) WalkPost(
	callback func(*Grammar[TToken, TNodeKind]) (skip, stop bool),
) error {
	return g.Walk(structarch.WALK_STRATEGY_POST, callback)
}

/*
WalkBreadth traverses the grammar tree in breadth-first order.
*/
func (g *Grammar[TToken, TNodeKind]) WalkBreadth(
	callback func(*Grammar[TToken, TNodeKind]) (skip, stop bool),
) error {
	return g.Walk(structarch.WALK_STRATEGY_BREADTH, callback)
}

/*
GrammarWalkPreWithContext traverses the grammar tree in pre-order, passing inherited context
from each node to its children. The callback receives (node, ctx) and returns the context
to pass to children plus skip/stop. Cycle-safe. Use when traversal logic depends on
context accumulated from ancestors (e.g. recovery tokens, repeat nesting).
*/
func GrammarWalkPreWithContext[TToken, TNodeKind comparable, TContext any](
	g *Grammar[TToken, TNodeKind],
	initial TContext,
	callback func(node *Grammar[TToken, TNodeKind], ctx TContext) (childCtx TContext, skipSubtree, stopWalk bool),
) error {
	if g == nil {
		return fmt.Errorf("GrammarWalkPreWithContext called on a nil grammar node")
	}
	return structarch.StructArchWalkWithContext(
		structarch.WalkConfigWithContext[*Grammar[TToken, TNodeKind], *Grammar[TToken, TNodeKind], TContext]{
			ID: func(n *Grammar[TToken, TNodeKind]) *Grammar[TToken, TNodeKind] {
				return n
			},
			ChildrenInto: func(
				n *Grammar[TToken, TNodeKind],
				out []*Grammar[TToken, TNodeKind],
			) []*Grammar[TToken, TNodeKind] {
				return grammarWalkChildrenInto(n, out)
			},
			Callback: callback,
		},
		g,
		initial,
	)
}

/*
FindFirst returns the first node in pre-order for which predicate returns true.

Returns nil if no node matches or if the receiver is nil.
*/
func (g *Grammar[TToken, TNodeKind]) FindFirst(
	predicate func(*Grammar[TToken, TNodeKind]) bool,
) *Grammar[TToken, TNodeKind] {
	if g == nil {
		return nil
	}
	var found *Grammar[TToken, TNodeKind]
	_ = g.WalkPre(func(n *Grammar[TToken, TNodeKind]) (skip, stop bool) {
		if predicate(n) {
			found = n
			return false, true
		}
		return false, false
	})
	return found
}

func grammarWalkChildrenInto[TToken, TNodeKind comparable](
	g *Grammar[TToken, TNodeKind],
	out []*Grammar[TToken, TNodeKind],
) []*Grammar[TToken, TNodeKind] {
	if g == nil {
		return out
	}
	if g.Kind == GReference && g.ResolvedReference != nil {
		return append(out, g.ResolvedReference)
	}
	if len(g.Children) == 0 {
		return out
	}

	startLen := len(out)
	out = append(out, g.Children...)

	write := startLen
	for i := startLen; i < len(out); i++ {
		if out[i] != nil {
			out[write] = out[i]
			write++
		}
	}
	return out[:write]
}

func grammarWalkChildren[TToken, TNodeKind comparable](g *Grammar[TToken, TNodeKind]) []*Grammar[TToken, TNodeKind] {
	return grammarWalkChildrenInto(g, nil)
}
