package lowering

import (
	"fmt"
	"unicode"

	"autarch/pattern"
	"foundation/hash"
	"lexarch"
	"syntaxa"
)

/*
ToPatternGrammar converts a Syntaxa grammar tree (and rule map) into a
pattern.Grammar (Contexta IR). One pattern rule is emitted per Syntaxa node. Rule names
are readable: GrammarLabel (or "n" if empty) plus an XXH3 hash of NodePath for uniqueness.

root and rules must have NodePath set on every node (e.g. after ProducePackage's collectAll).
rules is used to resolve GReference targets. additionalRules are disconnected roots to include.

Returns the grammar, ruleNameToNodeKey (Contexta rule name -> NodeKey), and
ruleNameToRecovery (Contexta rule name -> recovery tokens for that rule root).
*/
func ToPatternGrammar[TNodeKind comparable](
	root *syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
	additionalRules []*syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
) (*pattern.Grammar[lexarch.TokenKind, struct{}], map[string]syntaxa.NodeKey, map[string]syntaxa.RecoverySpec) {
	if root == nil {
		return nil, nil, nil
	}

	order := make([]*syntaxa.Grammar[lexarch.TokenKind, TNodeKind], 0)
	visited := make(map[syntaxa.NodePath]struct{})
	collectPostOrder(root, &order, visited)
	for _, r := range additionalRules {
		collectPostOrder(r, &order, visited)
	}

	hasher := hash.XXH3HasherCreateWithSeed(0)
	pathToRuleName := make(map[syntaxa.NodePath]string)
	ruleNameToNodeKey := make(map[string]syntaxa.NodeKey)
	ruleNameToRecovery := make(map[string]syntaxa.RecoverySpec)
	mergedRecoveryByLabel := syntaxa.RecoverySpecMergedByGrammarLabel(rules)
	for _, g := range order {
		if g.NodePath == nil {
			continue
		}
		name := contextaRuleName(hasher, g)
		pathToRuleName[*g.NodePath] = name
		ruleNameToNodeKey[name] = syntaxa.NodeKey(*g.NodePath)
		if g.IsContextBoundary {
			if mergedSpec, ok := mergedRecoveryByLabel[g.GrammarLabel]; ok {
				ruleNameToRecovery[name] = syntaxa.RecoverySpec{
					Tokens:    append([]lexarch.TokenKind(nil), mergedSpec.Tokens...),
					NoConsume: append([]lexarch.TokenKind(nil), mergedSpec.NoConsume...),
				}
				continue
			}
		}
		if len(g.RecoveryTokens) > 0 || len(g.NoConsumeOnRecoveryTokens) > 0 {
			ruleNameToRecovery[name] = syntaxa.RecoverySpec{
				Tokens:    append([]lexarch.TokenKind(nil), g.RecoveryTokens...),
				NoConsume: append([]lexarch.TokenKind(nil), g.NoConsumeOnRecoveryTokens...),
			}
		}
	}

	cfg := &pattern.Grammar[lexarch.TokenKind, struct{}]{
		StartSymbol: pathToRuleName[*root.NodePath],
		Rules:       make(map[string]*pattern.Rule[lexarch.TokenKind, struct{}]),
	}

	for _, g := range order {
		if g.NodePath == nil {
			continue
		}
		name := pathToRuleName[*g.NodePath]
		cfg.Rules[name] = syntaxaNodeToContextaRule(g, rules, pathToRuleName)
	}

	return cfg, ruleNameToNodeKey, ruleNameToRecovery
}

func contextaRuleName[TNodeKind comparable](hasher *hash.XXH3Hasher, g *syntaxa.Grammar[lexarch.TokenKind, TNodeKind]) string {
	path := *g.NodePath
	h := hash.XXH3HasherHash64(hasher, []byte(path))
	label := string(g.GrammarLabel)
	if label == "" {
		label = "n"
	} else {
		label = sanitizeLabelForRuleName(label)
		if label == "" {
			label = "n"
		}
	}
	return fmt.Sprintf("%s_%016x", label, h)
}

func sanitizeLabelForRuleName(s string) string {
	var b []byte
	for _, r := range s {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b = append(b, byte(r))
		} else if unicode.IsSpace(r) || unicode.IsPunct(r) {
			if len(b) > 0 && b[len(b)-1] != '_' {
				b = append(b, '_')
			}
		}
	}
	for len(b) > 0 && b[len(b)-1] == '_' {
		b = b[:len(b)-1]
	}
	return string(b)
}

func collectPostOrder[TNodeKind comparable](
	g *syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
	order *[]*syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
	visited map[syntaxa.NodePath]struct{},
) {
	if g == nil || g.NodePath == nil {
		return
	}
	path := *g.NodePath
	if _, seen := visited[path]; seen {
		return
	}
	visited[path] = struct{}{}
	for _, c := range grammarWalkChildren(g) {
		collectPostOrder(c, order, visited)
	}
	*order = append(*order, g)
}

func grammarWalkChildren[TNodeKind comparable](g *syntaxa.Grammar[lexarch.TokenKind, TNodeKind]) []*syntaxa.Grammar[lexarch.TokenKind, TNodeKind] {
	if g == nil {
		return nil
	}
	if g.Kind == syntaxa.GReference && g.ResolvedReference != nil {
		return []*syntaxa.Grammar[lexarch.TokenKind, TNodeKind]{g.ResolvedReference}
	}
	if len(g.Children) == 0 {
		return nil
	}
	out := make([]*syntaxa.Grammar[lexarch.TokenKind, TNodeKind], 0, len(g.Children))
	for _, c := range g.Children {
		if c != nil {
			out = append(out, c)
		}
	}
	return out
}

func syntaxaNodeToContextaRule[TNodeKind comparable](
	g *syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[lexarch.TokenKind, TNodeKind],
	pathToRuleName map[syntaxa.NodePath]string,
) *pattern.Rule[lexarch.TokenKind, struct{}] {
	rule := &pattern.Rule[lexarch.TokenKind, struct{}]{NonTerminal: pathToRuleName[*g.NodePath], Productions: nil}

	switch g.Kind {
	case syntaxa.GToken:
		rule.Productions = []pattern.Production[lexarch.TokenKind, struct{}]{
			{Symbols: []pattern.Symbol[lexarch.TokenKind, struct{}]{{Type: pattern.SYMBOL_TERMINAL, Token: g.Token}}},
		}
	case syntaxa.GEpsilon:
		rule.Productions = []pattern.Production[lexarch.TokenKind, struct{}]{
			{Symbols: []pattern.Symbol[lexarch.TokenKind, struct{}]{{Type: pattern.SYMBOL_EPSILON}}},
		}
	case syntaxa.GReference:
		target := g.ResolvedReference
		if target == nil && rules != nil {
			target = rules[g.ReferenceTarget]
		}
		if target != nil && target.NodePath != nil {
			rule.Productions = []pattern.Production[lexarch.TokenKind, struct{}]{
				{Symbols: []pattern.Symbol[lexarch.TokenKind, struct{}]{{Type: pattern.SYMBOL_NON_TERMINAL, Name: pathToRuleName[*target.NodePath]}}},
			}
		} else {
			rule.Productions = []pattern.Production[lexarch.TokenKind, struct{}]{
				{Symbols: []pattern.Symbol[lexarch.TokenKind, struct{}]{{Type: pattern.SYMBOL_EPSILON}}},
			}
		}
	case syntaxa.GConcat:
		syms := make([]pattern.Symbol[lexarch.TokenKind, struct{}], 0, len(g.Children))
		for _, c := range g.Children {
			if c != nil && c.NodePath != nil {
				syms = append(syms, pattern.Symbol[lexarch.TokenKind, struct{}]{Type: pattern.SYMBOL_NON_TERMINAL, Name: pathToRuleName[*c.NodePath]})
			}
		}
		rule.Productions = []pattern.Production[lexarch.TokenKind, struct{}]{{Symbols: syms}}
	case syntaxa.GChoice:
		prods := make([]pattern.Production[lexarch.TokenKind, struct{}], 0, len(g.Children))
		for _, c := range g.Children {
			if c != nil && c.NodePath != nil {
				prods = append(prods, pattern.Production[lexarch.TokenKind, struct{}]{
					Symbols: []pattern.Symbol[lexarch.TokenKind, struct{}]{{Type: pattern.SYMBOL_NON_TERMINAL, Name: pathToRuleName[*c.NodePath]}},
				})
			}
		}
		rule.Productions = prods
	case syntaxa.GOptional:
		if len(g.Children) == 0 || g.Children[0] == nil {
			rule.Productions = []pattern.Production[lexarch.TokenKind, struct{}]{{Symbols: []pattern.Symbol[lexarch.TokenKind, struct{}]{{Type: pattern.SYMBOL_EPSILON}}}}
		} else {
			bodyPath := g.Children[0].NodePath
			if bodyPath == nil {
				rule.Productions = []pattern.Production[lexarch.TokenKind, struct{}]{{Symbols: []pattern.Symbol[lexarch.TokenKind, struct{}]{{Type: pattern.SYMBOL_EPSILON}}}}
			} else {
				rule.Productions = []pattern.Production[lexarch.TokenKind, struct{}]{
					{Symbols: []pattern.Symbol[lexarch.TokenKind, struct{}]{{Type: pattern.SYMBOL_EPSILON}}},
					{Symbols: []pattern.Symbol[lexarch.TokenKind, struct{}]{{Type: pattern.SYMBOL_NON_TERMINAL, Name: pathToRuleName[*bodyPath]}}},
				}
			}
		}
	case syntaxa.GRepeat:
		if len(g.Children) == 0 || g.Children[0] == nil {
			rule.Productions = []pattern.Production[lexarch.TokenKind, struct{}]{{Symbols: []pattern.Symbol[lexarch.TokenKind, struct{}]{{Type: pattern.SYMBOL_EPSILON}}}}
		} else {
			bodyPath := g.Children[0].NodePath
			selfPath := *g.NodePath
			if bodyPath == nil {
				rule.Productions = []pattern.Production[lexarch.TokenKind, struct{}]{{Symbols: []pattern.Symbol[lexarch.TokenKind, struct{}]{{Type: pattern.SYMBOL_EPSILON}}}}
			} else if g.Min == 0 {
				rule.Productions = []pattern.Production[lexarch.TokenKind, struct{}]{
					{Symbols: []pattern.Symbol[lexarch.TokenKind, struct{}]{{Type: pattern.SYMBOL_EPSILON}}},
					{Symbols: []pattern.Symbol[lexarch.TokenKind, struct{}]{
						{Type: pattern.SYMBOL_NON_TERMINAL, Name: pathToRuleName[*bodyPath]},
						{Type: pattern.SYMBOL_NON_TERMINAL, Name: pathToRuleName[selfPath]},
					}},
				}
			} else {
				rule.Productions = []pattern.Production[lexarch.TokenKind, struct{}]{
					{Symbols: []pattern.Symbol[lexarch.TokenKind, struct{}]{
						{Type: pattern.SYMBOL_NON_TERMINAL, Name: pathToRuleName[*bodyPath]},
						{Type: pattern.SYMBOL_NON_TERMINAL, Name: pathToRuleName[selfPath]},
					}},
					{Symbols: []pattern.Symbol[lexarch.TokenKind, struct{}]{{Type: pattern.SYMBOL_NON_TERMINAL, Name: pathToRuleName[*bodyPath]}}},
				}
			}
		}
	case syntaxa.GNest:
		if g.OpenToken == nil || g.CloseToken == nil || len(g.Children) == 0 || g.Children[0] == nil {
			rule.Productions = []pattern.Production[lexarch.TokenKind, struct{}]{{Symbols: []pattern.Symbol[lexarch.TokenKind, struct{}]{{Type: pattern.SYMBOL_EPSILON}}}}
		} else {
			bodyPath := g.Children[0].NodePath
			if bodyPath == nil {
				rule.Productions = []pattern.Production[lexarch.TokenKind, struct{}]{{Symbols: []pattern.Symbol[lexarch.TokenKind, struct{}]{{Type: pattern.SYMBOL_EPSILON}}}}
			} else {
				rule.Productions = []pattern.Production[lexarch.TokenKind, struct{}]{
					{
						Symbols: []pattern.Symbol[lexarch.TokenKind, struct{}]{
							{Type: pattern.SYMBOL_TERMINAL, Token: *g.OpenToken},
							{Type: pattern.SYMBOL_NON_TERMINAL, Name: pathToRuleName[*bodyPath]},
							{Type: pattern.SYMBOL_TERMINAL, Token: *g.CloseToken},
						},
					},
				}
			}
		}
	default:
		rule.Productions = []pattern.Production[lexarch.TokenKind, struct{}]{{Symbols: []pattern.Symbol[lexarch.TokenKind, struct{}]{{Type: pattern.SYMBOL_EPSILON}}}}
	}

	return rule
}
