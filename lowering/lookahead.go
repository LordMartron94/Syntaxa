package lowering

import (
	"lexarch"
	"syntaxa"
)

type gStackEntry[TNodeKind comparable] struct {
	isRepetition bool
	label        syntaxa.GrammarLabel
	repeatNode   *syntaxa.Grammar[lexarch.TokenKind, TNodeKind]
	remaining    []*syntaxa.Grammar[lexarch.TokenKind, TNodeKind]

	recovery  []lexarch.TokenKind
	noConsume []lexarch.TokenKind
}

type gTerminal[TNodeKind comparable] struct {
	token     lexarch.TokenKind
	nodeKind  *TNodeKind
	remaining []*syntaxa.Grammar[lexarch.TokenKind, TNodeKind]
	stack     []gStackEntry[TNodeKind]
	nestNode  *syntaxa.Grammar[lexarch.TokenKind, TNodeKind]
	popOffset int
}

func (t *gTerminal[TNodeKind]) getLastRemaining() *[]*syntaxa.Grammar[lexarch.TokenKind, TNodeKind] {
	if len(t.stack) == 0 {
		return &t.remaining
	}
	return &t.stack[len(t.stack)-1].remaining
}

type visiting map[syntaxa.GrammarLabel]bool

func lookahead[TNodeKind comparable](
	node *syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
	visiting visiting,
	analysis *syntaxa.GrammarAnalysis,
) ([]gTerminal[TNodeKind], bool) {
	if node == nil {
		return nil, true
	}

	var ts []gTerminal[TNodeKind]
	var n bool

	switch node.Kind {
	case syntaxa.GToken:
		ts, n = []gTerminal[TNodeKind]{{token: node.Token, nodeKind: node.OutputNodeKind}}, false
	case syntaxa.GEpsilon:
		ts, n = nil, true
	case syntaxa.GConcat:
		ts, n = lookaheadConcat(node, node.Children, rules, visiting, analysis)
	case syntaxa.GChoice:
		ts, n = lookaheadChoice(node, rules, visiting, analysis)
	case syntaxa.GRepeat, syntaxa.GOptional:
		ts, n = lookaheadRepetition(node, rules, visiting, analysis)
	case syntaxa.GReference:
		ts, n = lookaheadReference(node, rules, visiting, analysis)
	case syntaxa.GNest:
		ts, n = lookaheadNest(node)
	}

	if analysis != nil && node.NodePath != nil {
		n = syntaxa.GrammarAnalysisNullable(analysis, node)
	}

	if node.Kind != syntaxa.GRepeat && node.Kind != syntaxa.GOptional && node.Kind != syntaxa.GReference {
		if len(node.RecoveryTokens) > 0 || len(node.NoConsumeOnRecoveryTokens) > 0 {
			for i := range ts {
				ts[i].stack = append(ts[i].stack, gStackEntry[TNodeKind]{
					isRepetition: false,
					label:        node.GrammarLabel,
					recovery:     node.RecoveryTokens,
					noConsume:    node.NoConsumeOnRecoveryTokens,
				})
			}
		}
	}

	return ts, n
}

func lookaheadChoice[TNodeKind comparable](
	node *syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
	visiting visiting,
	analysis *syntaxa.GrammarAnalysis,
) ([]gTerminal[TNodeKind], bool) {
	var all []gTerminal[TNodeKind]
	anyNull := false
	for _, child := range node.Children {
		ts, n := lookahead(child, rules, visiting, analysis)
		all = append(all, ts...)
		anyNull = anyNull || n
	}
	if analysis != nil && node.NodePath != nil {
		anyNull = syntaxa.GrammarAnalysisNullable(analysis, node)
	}
	return all, anyNull
}

func lookaheadRepetition[TNodeKind comparable](
	node *syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
	visiting visiting,
	analysis *syntaxa.GrammarAnalysis,
) ([]gTerminal[TNodeKind], bool) {
	child := node.Children[0]
	ts, _ := lookahead(child, rules, visiting, analysis)

	isRep := node.Kind == syntaxa.GRepeat

	for i := range ts {
		ts[i].stack = append(ts[i].stack, gStackEntry[TNodeKind]{
			isRepetition: isRep,
			repeatNode:   node,
			label:        node.GrammarLabel,
			recovery:     node.RecoveryTokens,
			noConsume:    node.NoConsumeOnRecoveryTokens,
		})
	}
	nullable := node.Min == 0 || node.Kind == syntaxa.GOptional
	if analysis != nil && node.NodePath != nil {
		nullable = syntaxa.GrammarAnalysisNullable(analysis, node)
	}
	return ts, nullable
}

func lookaheadReference[TNodeKind comparable](
	node *syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
	visiting visiting,
	analysis *syntaxa.GrammarAnalysis,
) ([]gTerminal[TNodeKind], bool) {
	target := node.ResolvedReference
	if target == nil {
		target = rules[node.ReferenceTarget]
	}
	if target == nil || visiting[node.ReferenceTarget] {
		return nil, false
	}
	visiting[node.ReferenceTarget] = true
	ts, n := lookahead(target, rules, visiting, analysis)
	delete(visiting, node.ReferenceTarget)
	for i := range ts {
		ts[i].stack = append(ts[i].stack, gStackEntry[TNodeKind]{
			isRepetition: false,
			label:        node.ReferenceTarget,
			recovery:     node.RecoveryTokens,
			noConsume:    node.NoConsumeOnRecoveryTokens,
		})
	}
	if analysis != nil && node.NodePath != nil {
		n = syntaxa.GrammarAnalysisNullable(analysis, node)
	}
	return ts, n
}

func lookaheadNest[TNodeKind comparable](
	node *syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
) ([]gTerminal[TNodeKind], bool) {
	t := gTerminal[TNodeKind]{
		token:    *node.OpenToken,
		nodeKind: node.OutputNodeKind,
		nestNode: node,
	}
	return []gTerminal[TNodeKind]{t}, false
}

func lookaheadConcat[TNodeKind comparable](
	concatNode *syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
	nodes []*syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
	visiting visiting,
	analysis *syntaxa.GrammarAnalysis,
) ([]gTerminal[TNodeKind], bool) {
	nullable := true
	var terms []gTerminal[TNodeKind]

	for i, node := range nodes {
		ts, n := lookahead(node, rules, visiting, analysis)
		suffix := nodes[i+1:]

		for j := range ts {
			last := ts[j].getLastRemaining()
			extended := make([]*syntaxa.Grammar[lexarch.TokenKind, TNodeKind], len(*last)+len(suffix))
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
	if analysis != nil && concatNode != nil && concatNode.NodePath != nil {
		nullable = syntaxa.GrammarAnalysisNullable(analysis, concatNode)
	}
	return terms, nullable
}
