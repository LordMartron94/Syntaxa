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
func (g *Grammar[TToken]) Walk(
	strategy structarch.StructArchWalkStrategy,
	callback func(
		node *Grammar[TToken],
	) (skipSubtree, stopWalk bool),
) error {
	if g == nil {
		return fmt.Errorf("Walk called on a nil grammar node")
	}

	return structarch.StructArchWalk(
		structarch.WalkConfig[
			*Grammar[TToken],
			*Grammar[TToken],
		]{
			Strategy: strategy,

			ID: func(n *Grammar[TToken]) *Grammar[TToken] {
				return n
			},

			Children: func(n *Grammar[TToken]) []*Grammar[TToken] {
				return grammarWalkChildren(n)
			},

			Callback: callback,
		},
		g,
	)
}

/*
WalkPre traverses the grammar tree in pre-order (top-down).
*/
func (g *Grammar[TToken]) WalkPre(
	callback func(*Grammar[TToken]) (skip, stop bool),
) error {
	return g.Walk(structarch.WALK_STRATEGY_PRE, callback)
}

/*
WalkPost traverses the grammar tree in post-order (bottom-up).
*/
func (g *Grammar[TToken]) WalkPost(
	callback func(*Grammar[TToken]) (skip, stop bool),
) error {
	return g.Walk(structarch.WALK_STRATEGY_POST, callback)
}

/*
WalkBreadth traverses the grammar tree in breadth-first order.
*/
func (g *Grammar[TToken]) WalkBreadth(
	callback func(*Grammar[TToken]) (skip, stop bool),
) error {
	return g.Walk(structarch.WALK_STRATEGY_BREADTH, callback)
}

/*
FindFirst returns the first node in pre-order for which predicate returns true.

Returns nil if no node matches or if the receiver is nil.
*/
func (g *Grammar[TToken]) FindFirst(
	predicate func(*Grammar[TToken]) bool,
) *Grammar[TToken] {
	if g == nil {
		return nil
	}
	var found *Grammar[TToken]
	_ = g.WalkPre(func(n *Grammar[TToken]) (skip, stop bool) {
		if predicate(n) {
			found = n
			return false, true
		}
		return false, false
	})
	return found
}

func grammarWalkChildren[TToken comparable](g *Grammar[TToken]) []*Grammar[TToken] {
	if g == nil || len(g.Children) == 0 {
		return nil
	}

	out := make([]*Grammar[TToken], 0, len(g.Children))
	for _, c := range g.Children {
		if c != nil {
			out = append(out, c)
		}
	}
	return out
}
