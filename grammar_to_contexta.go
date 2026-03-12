package syntaxa

import (
	"fmt"
	"unicode"

	"autarch/pattern"
	"foundation/hash"
)

/*
SyntaxaTreeToContextaGrammar converts a Syntaxa grammar tree (and rule map) into a
pattern.Grammar (Contexta IR). One pattern rule is emitted per Syntaxa node. Rule names
are readable: GrammarLabel (or "n" if empty) plus an XXH3 hash of NodePath for uniqueness,
so the CFG dump is human-readable while remaining deterministic.

root and rules must have NodePath set on every node (e.g. after FinalizeNodePaths and
ProducePackage's collectAll). rules is used to resolve GReference targets to their
NodePath. additionalRules are disconnected roots to include in the graph.

Returns the grammar, ruleNameToNodeKey (Contexta rule name -> NodeKey), and
ruleNameToRecovery (Contexta rule name -> recovery tokens for that rule root).
*/
func SyntaxaTreeToContextaGrammar[TToken, TNodeKind comparable](
	root *Grammar[TToken, TNodeKind],
	additionalRules []*Grammar[TToken, TNodeKind],
	rules map[GrammarLabel]*Grammar[TToken, TNodeKind],
) (*pattern.Grammar[TToken], map[string]NodeKey, map[string]RecoverySpec[TToken]) {
	if root == nil {
		return nil, nil, nil
	}

	// Post-order collect all nodes (so children are defined before parents)
	order := make([]*Grammar[TToken, TNodeKind], 0)
	visited := make(map[NodePath]struct{})
	collectPostOrder(root, &order, visited)
	for _, r := range additionalRules {
		collectPostOrder(r, &order, visited)
	}

	hasher := hash.XXH3HasherCreateWithSeed(0)
	pathToRuleName := make(map[NodePath]string)
	ruleNameToNodeKey := make(map[string]NodeKey)
	ruleNameToRecovery := make(map[string]RecoverySpec[TToken])
	for _, g := range order {
		if g.NodePath == nil {
			continue
		}
		name := contextaRuleName(hasher, g)
		pathToRuleName[*g.NodePath] = name
		ruleNameToNodeKey[name] = NodeKey(*g.NodePath)
		if len(g.RecoveryTokens) > 0 || len(g.NoConsumeOnRecoveryTokens) > 0 {
			ruleNameToRecovery[name] = RecoverySpec[TToken]{
				Tokens:    append([]TToken(nil), g.RecoveryTokens...),
				NoConsume: append([]TToken(nil), g.NoConsumeOnRecoveryTokens...),
			}
		}
	}

	cfg := &pattern.Grammar[TToken]{
		StartSymbol: pathToRuleName[*root.NodePath],
		Rules:       make(map[string]*pattern.Rule[TToken]),
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

// contextaRuleName returns a readable, unique rule name: sanitized GrammarLabel (or "n") + "_" + hex(XXH3(path)).
func contextaRuleName[TToken, TNodeKind comparable](hasher *hash.XXH3Hasher, g *Grammar[TToken, TNodeKind]) string {
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

// sanitizeLabelForRuleName turns a GrammarLabel into a valid rule-name segment (alphanumeric + underscore).
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
	// trim trailing underscore
	for len(b) > 0 && b[len(b)-1] == '_' {
		b = b[:len(b)-1]
	}
	return string(b)
}

func collectPostOrder[TToken, TNodeKind comparable](
	g *Grammar[TToken, TNodeKind],
	order *[]*Grammar[TToken, TNodeKind],
	visited map[NodePath]struct{},
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

func syntaxaNodeToContextaRule[TToken, TNodeKind comparable](
	g *Grammar[TToken, TNodeKind],
	rules map[GrammarLabel]*Grammar[TToken, TNodeKind],
	pathToRuleName map[NodePath]string,
) *pattern.Rule[TToken] {
	rule := &pattern.Rule[TToken]{NonTerminal: pathToRuleName[*g.NodePath], Productions: nil}

	switch g.Kind {
	case GToken:
		rule.Productions = []pattern.Production[TToken]{
			{Symbols: []pattern.Symbol[TToken]{{Type: pattern.SYMBOL_TERMINAL, Token: g.Token}}},
		}
	case GEpsilon:
		rule.Productions = []pattern.Production[TToken]{
			{Symbols: []pattern.Symbol[TToken]{{Type: pattern.SYMBOL_EPSILON}}},
		}
	case GReference:
		target := g.ResolvedReference
		if target == nil {
			if rules != nil {
				target = rules[g.ReferenceTarget]
			}
		}
		if target != nil && target.NodePath != nil {
			rule.Productions = []pattern.Production[TToken]{
				{Symbols: []pattern.Symbol[TToken]{{Type: pattern.SYMBOL_NON_TERMINAL, Name: pathToRuleName[*target.NodePath]}}},
			}
		} else {
			rule.Productions = []pattern.Production[TToken]{
				{Symbols: []pattern.Symbol[TToken]{{Type: pattern.SYMBOL_EPSILON}}},
			}
		}
	case GConcat:
		syms := make([]pattern.Symbol[TToken], 0, len(g.Children))
		for _, c := range g.Children {
			if c != nil && c.NodePath != nil {
				syms = append(syms, pattern.Symbol[TToken]{Type: pattern.SYMBOL_NON_TERMINAL, Name: pathToRuleName[*c.NodePath]})
			}
		}
		rule.Productions = []pattern.Production[TToken]{{Symbols: syms}}
	case GChoice:
		prods := make([]pattern.Production[TToken], 0, len(g.Children))
		for _, c := range g.Children {
			if c != nil && c.NodePath != nil {
				prods = append(prods, pattern.Production[TToken]{
					Symbols: []pattern.Symbol[TToken]{{Type: pattern.SYMBOL_NON_TERMINAL, Name: pathToRuleName[*c.NodePath]}},
				})
			}
		}
		rule.Productions = prods
	case GOptional:
		if len(g.Children) == 0 || g.Children[0] == nil {
			rule.Productions = []pattern.Production[TToken]{{Symbols: []pattern.Symbol[TToken]{{Type: pattern.SYMBOL_EPSILON}}}}
		} else {
			bodyPath := g.Children[0].NodePath
			if bodyPath == nil {
				rule.Productions = []pattern.Production[TToken]{{Symbols: []pattern.Symbol[TToken]{{Type: pattern.SYMBOL_EPSILON}}}}
			} else {
				rule.Productions = []pattern.Production[TToken]{
					{Symbols: []pattern.Symbol[TToken]{{Type: pattern.SYMBOL_EPSILON}}},
					{Symbols: []pattern.Symbol[TToken]{{Type: pattern.SYMBOL_NON_TERMINAL, Name: pathToRuleName[*bodyPath]}}},
				}
			}
		}
	case GRepeat:
		if len(g.Children) == 0 || g.Children[0] == nil {
			rule.Productions = []pattern.Production[TToken]{{Symbols: []pattern.Symbol[TToken]{{Type: pattern.SYMBOL_EPSILON}}}}
		} else {
			bodyPath := g.Children[0].NodePath
			selfPath := *g.NodePath
			if bodyPath == nil {
				rule.Productions = []pattern.Production[TToken]{{Symbols: []pattern.Symbol[TToken]{{Type: pattern.SYMBOL_EPSILON}}}}
			} else if g.Min == 0 {
				// 0..* or 0..N: epsilon | body self
				rule.Productions = []pattern.Production[TToken]{
					{Symbols: []pattern.Symbol[TToken]{{Type: pattern.SYMBOL_EPSILON}}},
					{Symbols: []pattern.Symbol[TToken]{
						{Type: pattern.SYMBOL_NON_TERMINAL, Name: pathToRuleName[*bodyPath]},
						{Type: pattern.SYMBOL_NON_TERMINAL, Name: pathToRuleName[selfPath]},
					}},
				}
			} else {
				// 1..* or 1..N: body self | body (for exactly 1 we'd need more productions; minimal: body self | body)
				rule.Productions = []pattern.Production[TToken]{
					{Symbols: []pattern.Symbol[TToken]{
						{Type: pattern.SYMBOL_NON_TERMINAL, Name: pathToRuleName[*bodyPath]},
						{Type: pattern.SYMBOL_NON_TERMINAL, Name: pathToRuleName[selfPath]},
					}},
					{Symbols: []pattern.Symbol[TToken]{{Type: pattern.SYMBOL_NON_TERMINAL, Name: pathToRuleName[*bodyPath]}}},
				}
			}
		}
	case GNest:
		if g.OpenToken == nil || g.CloseToken == nil || len(g.Children) == 0 || g.Children[0] == nil {
			rule.Productions = []pattern.Production[TToken]{{Symbols: []pattern.Symbol[TToken]{{Type: pattern.SYMBOL_EPSILON}}}}
		} else {
			bodyPath := g.Children[0].NodePath
			if bodyPath == nil {
				rule.Productions = []pattern.Production[TToken]{{Symbols: []pattern.Symbol[TToken]{{Type: pattern.SYMBOL_EPSILON}}}}
			} else {
				rule.Productions = []pattern.Production[TToken]{
					{
						Symbols: []pattern.Symbol[TToken]{
							{Type: pattern.SYMBOL_TERMINAL, Token: *g.OpenToken},
							{Type: pattern.SYMBOL_NON_TERMINAL, Name: pathToRuleName[*bodyPath]},
							{Type: pattern.SYMBOL_TERMINAL, Token: *g.CloseToken},
						},
					},
				}
			}
		}
	default:
		rule.Productions = []pattern.Production[TToken]{{Symbols: []pattern.Symbol[TToken]{{Type: pattern.SYMBOL_EPSILON}}}}
	}

	return rule
}
