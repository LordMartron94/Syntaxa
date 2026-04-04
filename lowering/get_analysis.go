package lowering

import (
	"autarch/pattern"
	"syntaxa"
)

/*
GetAnalysis computes nullable, first, and follow analysis for the grammar package.
*/
func GetAnalysis[TNodeKind comparable](
	pkg *syntaxa.GrammarPackage[TNodeKind],
) *syntaxa.GrammarAnalysis {
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
	return syntaxa.GrammarAnalysisFromPattern(cfg, pa, ruleNameToNodeKey)
}
