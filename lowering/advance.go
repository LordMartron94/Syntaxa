package lowering

import "syntaxa"

func advanceTerminal[TToken, TNodeKind comparable](
	term gTerminal[TToken, TNodeKind],
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[TToken, TNodeKind],
	analysis *syntaxa.GrammarAnalysis[TToken],
) ([]gTerminal[TToken, TNodeKind], bool) {
	levels := len(term.stack) + 1
	var result []gTerminal[TToken, TNodeKind]
	isNullable := true

	for i := 0; i < levels; i++ {
		remaining, isRep, repNode := extractFrameDetails(term, i)

		if len(remaining) == 0 && !isRep {
			continue
		}

		la, nullable := computeLookaheadForFrame(remaining, isRep, repNode, rules, analysis)
		result = appendLookaheadWithStack(la, term.stack[i:], result)

		if !nullable {
			isNullable = false
			break
		}
	}

	if len(result) == 0 {
		return nil, true
	}
	return result, isNullable
}

func extractFrameDetails[TToken, TNodeKind comparable](
	term gTerminal[TToken, TNodeKind],
	level int,
) ([]*syntaxa.Grammar[TToken, TNodeKind], bool, *syntaxa.Grammar[TToken, TNodeKind]) {
	if level == 0 {
		return term.remaining, false, nil
	}
	entry := term.stack[level-1]
	return entry.remaining, entry.isRepetition, entry.repeatNode
}

func computeLookaheadForFrame[TToken, TNodeKind comparable](
	remaining []*syntaxa.Grammar[TToken, TNodeKind],
	isRep bool,
	repNode *syntaxa.Grammar[TToken, TNodeKind],
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[TToken, TNodeKind],
	analysis *syntaxa.GrammarAnalysis[TToken],
) ([]gTerminal[TToken, TNodeKind], bool) {
	visiting := make(visiting)
	if isRep {
		loopNodes := make([]*syntaxa.Grammar[TToken, TNodeKind], 0, 1+len(remaining))
		loopNodes = append(loopNodes, repNode)
		loopNodes = append(loopNodes, remaining...)
		return lookaheadConcat(nil, loopNodes, rules, visiting, analysis)
	}
	return lookaheadConcat(nil, remaining, rules, visiting, analysis)
}

func appendLookaheadWithStack[TToken, TNodeKind comparable](
	la []gTerminal[TToken, TNodeKind],
	outerStack []gStackEntry[TToken, TNodeKind],
	result []gTerminal[TToken, TNodeKind],
) []gTerminal[TToken, TNodeKind] {
	for j := range la {
		newStack := make([]gStackEntry[TToken, TNodeKind], len(la[j].stack)+len(outerStack))
		copy(newStack, la[j].stack)
		copy(newStack[len(la[j].stack):], outerStack)
		la[j].stack = newStack
	}
	return append(result, la...)
}
