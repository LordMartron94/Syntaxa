package lowering

import (
	"cmp"
	"autarch/pattern"
	"syntaxa"
)

/*
GetAnalysis computes nullable, first, and follow analysis for the grammar package.

It uses ToPatternGrammar and pattern.ComputeAnalysis; the result is keyed by NodeKey
via syntaxa.GrammarAnalysisFromPattern. Returns nil if the package or its Root is nil,
or if ToPatternGrammar returns nil.
*/
func GetAnalysis[
	TObservation cmp.Ordered,
	TToken comparable,
	TTokenRole comparable,
	TNodeKind comparable,
	TLexerState comparable,
](
	pkg *syntaxa.GrammarPackage[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
) *syntaxa.GrammarAnalysis[TToken] {
	if pkg == nil || pkg.Root == nil {
		return nil
	}
	cfg, ruleNameToNodeKey, _ := ToPatternGrammar(
		pkg.Root,
		pkg.AdditionalRules,
		pkg.Grammars,
	)
	if cfg == nil {
		return nil
	}
	pa := pattern.ComputeAnalysis(cfg)
	return syntaxa.GrammarAnalysisFromPattern(pa, ruleNameToNodeKey)
}
