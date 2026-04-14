package lowering

import (
	"lexarch"
	"syntaxa"
)

func advanceTerminal[TNodeKind comparable](
	term gTerminal[TNodeKind],
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
	analysis *syntaxa.GrammarAnalysis,
) ([]gTerminal[TNodeKind], bool) {
	levels := len(term.stack) + 1
	var result []gTerminal[TNodeKind]
	isNullable := true

	for i := 0; i < levels; i++ {
		remaining, isRep, repNode := extractFrameDetails(term, i)

		if len(remaining) == 0 && !isRep {
			continue
		}

		la, nullable := computeLookaheadForFrame(remaining, isRep, repNode, rules, analysis)
		result = appendLookaheadWithStack(la, term.token, term.choiceGuard, term.stack[i:], result)

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

func extractFrameDetails[TNodeKind comparable](
	term gTerminal[TNodeKind],
	level int,
) ([]*syntaxa.Grammar[lexarch.TokenKind, TNodeKind], bool, *syntaxa.Grammar[lexarch.TokenKind, TNodeKind]) {
	if level == 0 {
		return term.remaining, false, nil
	}
	entry := term.stack[level-1]
	return entry.remaining, entry.isRepetition, entry.repeatNode
}

func computeLookaheadForFrame[TNodeKind comparable](
	remaining []*syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
	isRep bool,
	repNode *syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
	analysis *syntaxa.GrammarAnalysis,
) ([]gTerminal[TNodeKind], bool) {
	visiting := make(visiting)

	if isRep {
		// 1. Branch A: Loop Again (lookahead into the repetition body)
		loopTs, _ := lookahead(repNode, rules, visiting, analysis)

		// Attach the continuation to the loop terminals so they know what follows
		for j := range loopTs {
			last := loopTs[j].getLastRemaining()
			extended := make([]*syntaxa.Grammar[lexarch.TokenKind, TNodeKind], len(*last)+len(remaining))
			copy(extended, *last)
			copy(extended[len(*last):], remaining)
			*last = extended
		}

		// 2. Branch B: Exit Loop (lookahead into the continuation)
		remTs, remNull := lookaheadConcat(nil, remaining, rules, visiting, analysis)

		// 3. Union the branches. The repetition frame is nullable if its continuation is nullable.
		return append(loopTs, remTs...), remNull
	}

	return lookaheadConcat(nil, remaining, rules, visiting, analysis)
}

func appendLookaheadWithStack[TNodeKind comparable](
	la []gTerminal[TNodeKind],
	consumedTok lexarch.TokenKind,
	parentGuard []syntaxa.Lookahead[lexarch.TokenKind],
	outerStack []gStackEntry[TNodeKind],
	result []gTerminal[TNodeKind],
) []gTerminal[TNodeKind] {
	shifted := shiftChoiceGuardAfterConsume(parentGuard, consumedTok)
	for j := range la {
		newStack := make([]gStackEntry[TNodeKind], len(la[j].stack)+len(outerStack))
		copy(newStack, la[j].stack)
		copy(newStack[len(la[j].stack):], outerStack)
		la[j].stack = newStack
		la[j].choiceGuard = mergeLookaheads(shifted, la[j].choiceGuard)
	}
	return append(result, la...)
}
