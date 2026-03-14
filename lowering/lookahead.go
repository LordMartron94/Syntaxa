package lowering

import "syntaxa"

type gStackEntry[TToken, TNodeKind comparable] struct {
	isRepetition bool
	label        syntaxa.GrammarLabel
	repeatNode   *syntaxa.Grammar[TToken, TNodeKind]
	remaining    []*syntaxa.Grammar[TToken, TNodeKind]
}

type gTerminal[TToken, TNodeKind comparable] struct {
	token     TToken
	nodeKind  *TNodeKind
	remaining []*syntaxa.Grammar[TToken, TNodeKind]
	stack     []gStackEntry[TToken, TNodeKind]
	nestNode  *syntaxa.Grammar[TToken, TNodeKind]
	popOffset int
}

func (t *gTerminal[TToken, TNodeKind]) getLastRemaining() *[]*syntaxa.Grammar[TToken, TNodeKind] {
	if len(t.stack) == 0 {
		return &t.remaining
	}
	return &t.stack[len(t.stack)-1].remaining
}

type visiting map[syntaxa.GrammarLabel]bool

func lookahead[TToken, TNodeKind comparable](
	node *syntaxa.Grammar[TToken, TNodeKind],
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[TToken, TNodeKind],
	visiting visiting,
) ([]gTerminal[TToken, TNodeKind], bool) {
	if node == nil {
		return nil, true
	}

	switch node.Kind {
	case syntaxa.GToken:
		return []gTerminal[TToken, TNodeKind]{{token: node.Token, nodeKind: node.OutputNodeKind}}, false
	case syntaxa.GEpsilon:
		return nil, true
	case syntaxa.GConcat:
		return lookaheadConcat(node.Children, rules, visiting)
	case syntaxa.GChoice:
		return lookaheadChoice(node, rules, visiting)
	case syntaxa.GRepeat, syntaxa.GOptional:
		return lookaheadRepetition(node, rules, visiting)
	case syntaxa.GReference:
		return lookaheadReference(node, rules, visiting)
	case syntaxa.GNest:
		return lookaheadNest(node)
	}

	return nil, false
}

func lookaheadChoice[TToken, TNodeKind comparable](
	node *syntaxa.Grammar[TToken, TNodeKind],
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[TToken, TNodeKind],
	visiting visiting,
) ([]gTerminal[TToken, TNodeKind], bool) {
	var all []gTerminal[TToken, TNodeKind]
	anyNull := false
	for _, child := range node.Children {
		ts, n := lookahead(child, rules, visiting)
		all = append(all, ts...)
		anyNull = anyNull || n
	}
	return all, anyNull
}

func lookaheadRepetition[TToken, TNodeKind comparable](
	node *syntaxa.Grammar[TToken, TNodeKind],
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[TToken, TNodeKind],
	visiting visiting,
) ([]gTerminal[TToken, TNodeKind], bool) {
	child := node.Children[0]
	ts, _ := lookahead(child, rules, visiting)
	for i := range ts {
		ts[i].stack = append(ts[i].stack, gStackEntry[TToken, TNodeKind]{
			isRepetition: true,
			repeatNode:   node,
			label:        node.GrammarLabel,
		})
	}
	return ts, node.Min == 0 || node.Kind == syntaxa.GOptional
}

func lookaheadReference[TToken, TNodeKind comparable](
	node *syntaxa.Grammar[TToken, TNodeKind],
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[TToken, TNodeKind],
	visiting visiting,
) ([]gTerminal[TToken, TNodeKind], bool) {
	target := node.ResolvedReference
	if target == nil {
		target = rules[node.ReferenceTarget]
	}
	if target == nil || visiting[node.ReferenceTarget] {
		return nil, false
	}
	visiting[node.ReferenceTarget] = true
	ts, n := lookahead(target, rules, visiting)
	delete(visiting, node.ReferenceTarget)
	for i := range ts {
		ts[i].stack = append(ts[i].stack, gStackEntry[TToken, TNodeKind]{
			isRepetition: false,
			label:        node.ReferenceTarget,
		})
	}
	return ts, n
}

func lookaheadNest[TToken, TNodeKind comparable](
	node *syntaxa.Grammar[TToken, TNodeKind],
) ([]gTerminal[TToken, TNodeKind], bool) {
	t := gTerminal[TToken, TNodeKind]{
		token:    *node.OpenToken,
		nodeKind: node.OutputNodeKind,
		nestNode: node,
	}
	return []gTerminal[TToken, TNodeKind]{t}, false
}

func lookaheadConcat[TToken, TNodeKind comparable](
	nodes []*syntaxa.Grammar[TToken, TNodeKind],
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[TToken, TNodeKind],
	visiting visiting,
) ([]gTerminal[TToken, TNodeKind], bool) {
	nullable := true
	var terms []gTerminal[TToken, TNodeKind]

	for i, node := range nodes {
		ts, n := lookahead(node, rules, visiting)
		suffix := nodes[i+1:]

		for j := range ts {
			last := ts[j].getLastRemaining()
			extended := make([]*syntaxa.Grammar[TToken, TNodeKind], len(*last)+len(suffix))
			copy(extended, *last)
			copy(extended[len(*last):], suffix)
			*last = extended
		}

		terms = append(terms, ts...)

		if !n {
			nullable = false
			break
		}
	}
	return terms, nullable
}
