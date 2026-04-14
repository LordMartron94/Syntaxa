package lowering

import (
	"lexarch"
	"sort"
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
	// choiceGuard is the predict / Lookaheads conjunction for the GChoice arm that
	// produced this terminal (relative peek offsets). It is shifted when consuming
	// tokens in advanceTerminal so overlapping FIRST sets stay in distinct contexts.
	choiceGuard []syntaxa.Lookahead[lexarch.TokenKind]
}

func (t *gTerminal[TNodeKind]) getLastRemaining() *[]*syntaxa.Grammar[lexarch.TokenKind, TNodeKind] {
	if len(t.stack) == 0 {
		return &t.remaining
	}
	return &t.stack[len(t.stack)-1].remaining
}

func copyLookaheads(g []syntaxa.Lookahead[lexarch.TokenKind]) []syntaxa.Lookahead[lexarch.TokenKind] {
	if len(g) == 0 {
		return nil
	}
	out := make([]syntaxa.Lookahead[lexarch.TokenKind], len(g))
	copy(out, g)
	return out
}

func mergeLookaheads(
	a, b []syntaxa.Lookahead[lexarch.TokenKind],
) []syntaxa.Lookahead[lexarch.TokenKind] {
	if len(a) == 0 {
		return normalizeLookaheadSlice(b)
	}
	if len(b) == 0 {
		return normalizeLookaheadSlice(a)
	}
	out := make([]syntaxa.Lookahead[lexarch.TokenKind], 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Offset != out[j].Offset {
			return out[i].Offset < out[j].Offset
		}
		return out[i].Expected < out[j].Expected
	})
	return dedupeSortedLookaheads(out)
}

func normalizeLookaheadSlice(g []syntaxa.Lookahead[lexarch.TokenKind]) []syntaxa.Lookahead[lexarch.TokenKind] {
	if len(g) == 0 {
		return nil
	}
	if len(g) == 1 {
		return copyLookaheads(g)
	}
	cp := append([]syntaxa.Lookahead[lexarch.TokenKind](nil), g...)
	sort.Slice(cp, func(i, j int) bool {
		if cp[i].Offset != cp[j].Offset {
			return cp[i].Offset < cp[j].Offset
		}
		return cp[i].Expected < cp[j].Expected
	})
	return dedupeSortedLookaheads(cp)
}

func dedupeSortedLookaheads(g []syntaxa.Lookahead[lexarch.TokenKind]) []syntaxa.Lookahead[lexarch.TokenKind] {
	if len(g) <= 1 {
		return g
	}
	dst := g[:0]
	for i := 0; i < len(g); i++ {
		if len(dst) > 0 && dst[len(dst)-1].Offset == g[i].Offset && dst[len(dst)-1].Expected == g[i].Expected {
			continue
		}
		dst = append(dst, g[i])
	}
	return dst
}

// shiftChoiceGuardAfterConsume removes satisfied peek(0) constraints and decrements
// remaining offsets after consuming consumedTok on the outgoing transition.
func shiftChoiceGuardAfterConsume(
	g []syntaxa.Lookahead[lexarch.TokenKind],
	consumedTok lexarch.TokenKind,
) []syntaxa.Lookahead[lexarch.TokenKind] {
	if len(g) == 0 {
		return nil
	}
	var out []syntaxa.Lookahead[lexarch.TokenKind]
	for _, l := range g {
		if l.Offset > 0 {
			out = append(out, syntaxa.Lookahead[lexarch.TokenKind]{
				Offset:   l.Offset - 1,
				Expected: l.Expected,
			})
			continue
		}
		if l.Expected != consumedTok {
			out = append(out, l)
			continue
		}
	}
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Offset != out[j].Offset {
			return out[i].Offset < out[j].Offset
		}
		return out[i].Expected < out[j].Expected
	})
	return dedupeSortedLookaheads(out)
}

func armGuardForChild[TNodeKind comparable](
	analysis *syntaxa.GrammarAnalysis,
	child *syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
) []syntaxa.Lookahead[lexarch.TokenKind] {
	if child == nil {
		return nil
	}
	if analysis != nil && child.NodePath != nil {
		key := syntaxa.NodeKeyFromPath(*child.NodePath)
		if arm, ok := analysis.ArmPredict[key]; ok && len(arm.Guard) > 0 {
			return copyLookaheads(arm.Guard)
		}
	}
	if len(child.Lookaheads) > 0 {
		return copyLookaheads(child.Lookaheads)
	}
	return nil
}

func filterTermsByArmFirst[TNodeKind comparable](
	ts []gTerminal[TNodeKind],
	first syntaxa.TokenSet,
) []gTerminal[TNodeKind] {
	if len(first) == 0 || len(ts) == 0 {
		return ts
	}
	out := ts[:0]
	for _, t := range ts {
		if _, ok := first[t.token]; ok {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
		if child == nil {
			continue
		}
		ts, n := lookahead(child, rules, visiting, analysis)
		if analysis != nil && child.NodePath != nil {
			key := syntaxa.NodeKeyFromPath(*child.NodePath)
			if arm, ok := analysis.ArmPredict[key]; ok && len(arm.First) > 0 {
				if !syntaxa.GrammarAnalysisNullable(analysis, child) {
					ts = filterTermsByArmFirst(ts, arm.First)
				}
			}
		}
		guard := armGuardForChild(analysis, child)
		if len(guard) > 0 {
			gc := copyLookaheads(guard)
			for j := range ts {
				ts[j].choiceGuard = mergeLookaheads(gc, ts[j].choiceGuard)
			}
		}
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
