package syntaxa

import "lexarch"

/*
ChoiceDispatchMode classifies how a GChoice arm list was filtered using GrammarAnalysis.

ChoiceDispatchFallback means analysis was nil (caller should try all arms in order).

ChoiceDispatchNoMatch means every arm was filtered out for the current peek prefix.

ChoiceDispatchCandidates means one or more arms are viable; try them in ascending index order
(PEG / ordered choice: first success wins).
*/
type ChoiceDispatchMode uint8

const (
	ChoiceDispatchFallback ChoiceDispatchMode = iota
	ChoiceDispatchCandidates
	ChoiceDispatchNoMatch
)

/*
ChoiceCandidateIndicesFromAnalysis returns the subset of choice arm indices that the parser
would consider for the current visible token stream prefix, using the same rules as
rule.Choice (nullable handling, ArmPredict.First override, predict guards).

peek must follow SelectRuleContext.Peek semantics (offset 0 = current visible token after skip).

When analysis is nil, returns (nil, ChoiceDispatchFallback). When no arm matches, returns
(nil, ChoiceDispatchNoMatch). Otherwise returns indices in ascending order.
*/
func ChoiceCandidateIndicesFromAnalysis[TNodeKind comparable](
	analysis *GrammarAnalysis,
	arms []*Grammar[lexarch.TokenKind, TNodeKind],
	peek func(int) Lexeme,
) ([]int, ChoiceDispatchMode) {
	if analysis == nil {
		return nil, ChoiceDispatchFallback
	}

	peekToken := peek(0).Token
	candidateIndices := make([]int, 0, len(arms))

	for idx, grammar := range arms {
		if grammar == nil || grammar.NodePath == nil {
			candidateIndices = append(candidateIndices, idx)
			continue
		}

		nodeKey := NodeKeyFromPath(*grammar.NodePath)
		firstSet, hasFirst := analysis.First[nodeKey]
		nullable, hasNullable := analysis.Nullable[nodeKey]
		if arm, ok := analysis.ArmPredict[nodeKey]; ok && len(arm.First) > 0 {
			firstSet = arm.First
			hasFirst = true
		}
		if !hasFirst || !hasNullable {
			candidateIndices = append(candidateIndices, idx)
			continue
		}

		if nullable {
			candidateIndices = append(candidateIndices, idx)
			continue
		}
		if _, exists := firstSet[peekToken]; !exists {
			continue
		}
		var guard []Lookahead[lexarch.TokenKind]
		if arm, ok := analysis.ArmPredict[nodeKey]; ok && len(arm.Guard) > 0 {
			guard = arm.Guard
		} else if len(grammar.Lookaheads) > 0 {
			guard = grammar.Lookaheads
		}
		if len(guard) > 0 && !GuardMatchesLookahead(peek, guard) {
			continue
		}
		candidateIndices = append(candidateIndices, idx)
	}

	if len(candidateIndices) == 0 {
		return nil, ChoiceDispatchNoMatch
	}
	return candidateIndices, ChoiceDispatchCandidates
}
