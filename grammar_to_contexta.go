package syntaxa

import (
	"autarch/pattern"
)

/*
SyntaxaTreeToContextaGrammar converts a Syntaxa grammar tree (and rule map) into a
pattern.Grammar (Contexta IR). One pattern rule is emitted per Syntaxa node, with
rule name equal to that node's NodePath, so pattern's analysis (keyed by rule name)
maps 1:1 to Syntaxa's NodeKey.

root and rules must have NodePath set on every node (e.g. after FinalizeNodePaths and
ProducePackage's collectAll). rules is used to resolve GReference targets to their
NodePath. additionalRules are disconnected roots to include in the graph.
*/
func SyntaxaTreeToContextaGrammar[TToken, TNodeKind comparable](
	root *Grammar[TToken, TNodeKind],
	additionalRules []*Grammar[TToken, TNodeKind],
	rules map[GrammarLabel]*Grammar[TToken, TNodeKind],
) *pattern.Grammar[TToken] {
	if root == nil {
		return nil
	}

	// Post-order collect all nodes (so children are defined before parents)
	order := make([]*Grammar[TToken, TNodeKind], 0)
	visited := make(map[NodePath]struct{})
	collectPostOrder(root, &order, visited)
	for _, r := range additionalRules {
		collectPostOrder(r, &order, visited)
	}

	cfg := &pattern.Grammar[TToken]{
		StartSymbol: string(*root.NodePath),
		Rules:       make(map[string]*pattern.Rule[TToken]),
	}

	for _, g := range order {
		if g.NodePath == nil {
			continue
		}
		name := string(*g.NodePath)
		cfg.Rules[name] = syntaxaNodeToContextaRule(g, rules)
	}

	return cfg
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
) *pattern.Rule[TToken] {
	rule := &pattern.Rule[TToken]{NonTerminal: string(*g.NodePath), Productions: nil}

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
				{Symbols: []pattern.Symbol[TToken]{{Type: pattern.SYMBOL_NON_TERMINAL, Name: string(*target.NodePath)}}},
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
				syms = append(syms, pattern.Symbol[TToken]{Type: pattern.SYMBOL_NON_TERMINAL, Name: string(*c.NodePath)})
			}
		}
		rule.Productions = []pattern.Production[TToken]{{Symbols: syms}}
	case GChoice:
		prods := make([]pattern.Production[TToken], 0, len(g.Children))
		for _, c := range g.Children {
			if c != nil && c.NodePath != nil {
				prods = append(prods, pattern.Production[TToken]{
					Symbols: []pattern.Symbol[TToken]{{Type: pattern.SYMBOL_NON_TERMINAL, Name: string(*c.NodePath)}},
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
					{Symbols: []pattern.Symbol[TToken]{{Type: pattern.SYMBOL_NON_TERMINAL, Name: string(*bodyPath)}}},
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
						{Type: pattern.SYMBOL_NON_TERMINAL, Name: string(*bodyPath)},
						{Type: pattern.SYMBOL_NON_TERMINAL, Name: string(selfPath)},
					}},
				}
			} else {
				// 1..* or 1..N: body self | body (for exactly 1 we'd need more productions; minimal: body self | body)
				rule.Productions = []pattern.Production[TToken]{
					{Symbols: []pattern.Symbol[TToken]{
						{Type: pattern.SYMBOL_NON_TERMINAL, Name: string(*bodyPath)},
						{Type: pattern.SYMBOL_NON_TERMINAL, Name: string(selfPath)},
					}},
					{Symbols: []pattern.Symbol[TToken]{{Type: pattern.SYMBOL_NON_TERMINAL, Name: string(*bodyPath)}}},
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
							{Type: pattern.SYMBOL_NON_TERMINAL, Name: string(*bodyPath)},
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
