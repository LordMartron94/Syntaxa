package rule

import (
	"cmp"
	"fmt"
	"slices"
	"syntaxa"
)

// ------------------------------------------------------------- PRATT CONFIG (DATA ONLY)

/*
PrattPrefixOp describes a prefix operator for Pratt expression parsing.
*/
type PrattPrefixOp[TToken, TNodeKind comparable] struct {
	Token             TToken
	RightBP           int
	NodeKind          TNodeKind
	TokenGrammarLabel syntaxa.GrammarLabel
}

/*
PrattPrefixRuleOp describes a complex prefix rule. It dynamically binds to its FIRST set at runtime.
*/
type PrattPrefixRuleOp[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	RightBP  int
	NodeKind TNodeKind
	Rule     Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
}

/*
PrattInfixOp describes an infix (binary) operator for Pratt expression parsing.
*/
type PrattInfixOp[TToken, TNodeKind comparable] struct {
	Token             TToken
	LeftBP            int
	RightBP           int
	NodeKind          TNodeKind
	TokenGrammarLabel syntaxa.GrammarLabel
}

/*
PrattInfixRuleOp describes a complex infix rule. It dynamically binds to its FIRST set at runtime.
*/
type PrattInfixRuleOp[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	LeftBP   int
	RightBP  int
	NodeKind TNodeKind
	Rule     Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
}

type PrattImplicitInfix[TToken, TNodeKind comparable] struct {
	LeftBP   int
	RightBP  int
	NodeKind TNodeKind
}

type PrattPostfixOp[TToken, TNodeKind comparable] struct {
	Token             TToken
	LeftBP            int
	NodeKind          TNodeKind
	TokenGrammarLabel syntaxa.GrammarLabel
}

/*
PrattPostfixRuleOp describes a postfix operator that requires executing a full rule.
It dynamically binds to its FIRST set at runtime.
*/
type PrattPostfixRuleOp[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	LeftBP   int
	NodeKind TNodeKind
	Rule     Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
}

/*
PrattConfig holds the configuration for a Pratt-style expression rule.
*/
type PrattConfig[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	Primary        Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
	PrefixOps      []PrattPrefixOp[TToken, TNodeKind]
	PrefixRuleOps  []PrattPrefixRuleOp[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
	PostfixOps     []PrattPostfixOp[TToken, TNodeKind]
	PostfixRuleOps []PrattPostfixRuleOp[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
	InfixOps       []PrattInfixOp[TToken, TNodeKind]
	InfixRuleOps   []PrattInfixRuleOp[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
	ImplicitInfix  *PrattImplicitInfix[TToken, TNodeKind]
	RecoveryTokens []TToken
}

// ------------------------------------------------------------- INTERNAL STATE

type prefixInfo[TNodeKind comparable] struct {
	rightBP  int
	nodeKind TNodeKind
}

type prefixRuleInfo[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	rightBP  int
	nodeKind TNodeKind
	rule     Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
}

type infixInfo[TNodeKind comparable] struct {
	leftBP   int
	rightBP  int
	nodeKind TNodeKind
}

type infixRuleInfo[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	leftBP   int
	rightBP  int
	nodeKind TNodeKind
	rule     Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
}

type postfixInfo[TNodeKind comparable] struct {
	leftBP   int
	nodeKind TNodeKind
}

type postfixRuleInfo[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	leftBP   int
	nodeKind TNodeKind
	rule     Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
}

/*
prattConfigMaps holds raw rule slices and pre-resolved token maps.
*/
type prattConfigMaps[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	primary Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
	name    syntaxa.RuleLabel

	rawPrefixRuleOps  []PrattPrefixRuleOp[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
	rawInfixRuleOps   []PrattInfixRuleOp[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
	rawPostfixRuleOps []PrattPostfixRuleOp[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]

	prefixMap      map[TToken]prefixInfo[TNodeKind]
	prefixRuleMap  map[TToken]prefixRuleInfo[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
	infixMap       map[TToken]infixInfo[TNodeKind]
	infixRuleMap   map[TToken]infixRuleInfo[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
	postfixMap     map[TToken]postfixInfo[TNodeKind]
	postfixRuleMap map[TToken]postfixRuleInfo[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
	implicitOp     *PrattImplicitInfix[TToken, TNodeKind]
	predictMap     map[TToken]bool

	isResolved bool
}

// ------------------------------------------------------------- PRATT ENDPOINT

type prattEndpoint[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	sharedCore *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
}

/*
Expression builds a Pratt-style precedence-climbing expression rule.
*/
func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Expression(
	grammarLabel syntaxa.GrammarLabel,
	config PrattConfig[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	maps := p.buildConfigMaps(grammarLabel, config)
	identity := p.sharedCore.createRuleIdentity(maps.name, grammarLabel, "expression")

	grammar := p.buildPrattGrammar(grammarLabel, config)
	syntaxa.MarkAsContextBoundary(grammar)

	hasImplicitInfix := config.ImplicitInfix != nil

	exec := func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
		// Late-bound resolution guarantees FIRST sets are available
		if !maps.isResolved {
			p.resolveRuleTriggers(ctx.GetAnalysis(), &maps)
			if hasImplicitInfix {
				maps.predictMap = p.buildPredictMap(ctx, config.Primary, maps.prefixMap, maps.prefixRuleMap)
			}
		}

		return p.runPrattExpression(ctx, maps, 0)
	}

	recovery := config.RecoveryTokens
	if recovery == nil {
		recovery = []TToken{}
	}

	return p.sharedCore.constructRule(
		identity,
		p.sharedCore.createContract(true, true),
		exec,
		recovery,
		nil,
		grammar,
	)
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) buildConfigMaps(
	grammarLabel syntaxa.GrammarLabel,
	config PrattConfig[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) prattConfigMaps[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	maps := prattConfigMaps[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{
		primary:           config.Primary,
		name:              p.sharedCore.createRuleName("Expression", grammarLabel),
		rawPrefixRuleOps:  config.PrefixRuleOps,
		rawInfixRuleOps:   config.InfixRuleOps,
		rawPostfixRuleOps: config.PostfixRuleOps,
		prefixMap:         make(map[TToken]prefixInfo[TNodeKind]),
		infixMap:          make(map[TToken]infixInfo[TNodeKind]),
		postfixMap:        make(map[TToken]postfixInfo[TNodeKind]),
		implicitOp:        config.ImplicitInfix,
	}

	// Token ops are resolved at compile time
	for _, op := range config.PrefixOps {
		maps.prefixMap[op.Token] = prefixInfo[TNodeKind]{rightBP: op.RightBP, nodeKind: op.NodeKind}
	}
	for _, op := range config.InfixOps {
		maps.infixMap[op.Token] = infixInfo[TNodeKind]{leftBP: op.LeftBP, rightBP: op.RightBP, nodeKind: op.NodeKind}
	}
	for _, op := range config.PostfixOps {
		maps.postfixMap[op.Token] = postfixInfo[TNodeKind]{leftBP: op.LeftBP, nodeKind: op.NodeKind}
	}

	return maps
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) resolveRuleTriggers(
	analysis *syntaxa.GrammarAnalysis[TToken],
	maps *prattConfigMaps[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) {
	maps.prefixRuleMap = make(map[TToken]prefixRuleInfo[TObservation, TToken, TTokenRole, TLexerState, TNodeKind])
	maps.infixRuleMap = make(map[TToken]infixRuleInfo[TObservation, TToken, TTokenRole, TLexerState, TNodeKind])
	maps.postfixRuleMap = make(map[TToken]postfixRuleInfo[TObservation, TToken, TTokenRole, TLexerState, TNodeKind])

	p.bindPrefixRuleOps(analysis, maps)
	p.bindInfixRuleOps(analysis, maps)
	p.bindPostfixRuleOps(analysis, maps)

	maps.isResolved = true
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) bindPrefixRuleOps(
	analysis *syntaxa.GrammarAnalysis[TToken],
	maps *prattConfigMaps[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) {
	for _, op := range maps.rawPrefixRuleOps {
		ruleKey := syntaxa.NodeKeyFromPath(*op.Rule.GetGrammar().NodePath)
		for token := range analysis.First[ruleKey] {
			if _, exists := maps.prefixMap[token]; exists {
				panic(fmt.Sprintf("pratt engine error: ambiguity detected. Token prefix op and rule prefix op overlap on token '%v'", token))
			}
			if _, exists := maps.prefixRuleMap[token]; exists {
				panic(fmt.Sprintf("pratt engine error: ambiguity detected. Multiple prefix rules trigger on token '%v'", token))
			}
			maps.prefixRuleMap[token] = prefixRuleInfo[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{
				rightBP:  op.RightBP,
				nodeKind: op.NodeKind,
				rule:     op.Rule,
			}
		}
	}
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) bindInfixRuleOps(
	analysis *syntaxa.GrammarAnalysis[TToken],
	maps *prattConfigMaps[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) {
	for _, op := range maps.rawInfixRuleOps {
		ruleKey := syntaxa.NodeKeyFromPath(*op.Rule.GetGrammar().NodePath)
		for token := range analysis.First[ruleKey] {
			if _, exists := maps.infixMap[token]; exists {
				panic(fmt.Sprintf("pratt engine error: ambiguity detected. Token infix op and rule infix op overlap on token '%v'", token))
			}
			if _, exists := maps.infixRuleMap[token]; exists {
				panic(fmt.Sprintf("pratt engine error: ambiguity detected. Multiple infix rules trigger on token '%v'", token))
			}
			maps.infixRuleMap[token] = infixRuleInfo[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{
				leftBP:   op.LeftBP,
				rightBP:  op.RightBP,
				nodeKind: op.NodeKind,
				rule:     op.Rule,
			}
		}
	}
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) bindPostfixRuleOps(
	analysis *syntaxa.GrammarAnalysis[TToken],
	maps *prattConfigMaps[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) {
	for _, op := range maps.rawPostfixRuleOps {
		ruleKey := syntaxa.NodeKeyFromPath(*op.Rule.GetGrammar().NodePath)
		for token := range analysis.First[ruleKey] {
			if _, exists := maps.postfixMap[token]; exists {
				panic(fmt.Sprintf("pratt engine error: ambiguity detected. Token postfix op and rule postfix op overlap on token '%v'", token))
			}
			if _, exists := maps.postfixRuleMap[token]; exists {
				panic(fmt.Sprintf("pratt engine error: ambiguity detected. Multiple postfix rules trigger on token '%v'", token))
			}
			maps.postfixRuleMap[token] = postfixRuleInfo[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{
				leftBP:   op.LeftBP,
				nodeKind: op.NodeKind,
				rule:     op.Rule,
			}
		}
	}
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) buildPredictMap(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	primary Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	prefixMap map[TToken]prefixInfo[TNodeKind],
	prefixRuleMap map[TToken]prefixRuleInfo[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) map[TToken]bool {
	predictMap := make(map[TToken]bool)
	analysis := ctx.GetAnalysis()
	rootKey := syntaxa.NodeKeyFromPath(*primary.GetGrammar().NodePath)

	for tok := range analysis.First[rootKey] {
		predictMap[tok] = true
	}
	for tok := range prefixMap {
		predictMap[tok] = true
	}
	for tok := range prefixRuleMap {
		predictMap[tok] = true
	}
	return predictMap
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) buildPrattGrammar(
	grammarLabel syntaxa.GrammarLabel,
	config PrattConfig[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) *syntaxa.Grammar[TToken, TNodeKind] {

	// 1. Group operators by their Binding Power
	type prattLevel struct {
		BP          int
		Prefixes    []*syntaxa.Grammar[TToken, TNodeKind]
		Postfixes   []*syntaxa.Grammar[TToken, TNodeKind]
		Infixes     []*syntaxa.Grammar[TToken, TNodeKind]
		HasImplicit bool
	}

	levelsMap := make(map[int]*prattLevel)
	getLvl := func(bp int) *prattLevel {
		if _, ok := levelsMap[bp]; !ok {
			levelsMap[bp] = &prattLevel{BP: bp}
		}
		return levelsMap[bp]
	}

	for _, op := range config.PrefixOps {
		getLvl(op.RightBP).Prefixes = append(getLvl(op.RightBP).Prefixes, syntaxa.Token[TToken, TNodeKind](op.TokenGrammarLabel, op.Token))
	}
	for _, op := range config.PrefixRuleOps {
		getLvl(op.RightBP).Prefixes = append(getLvl(op.RightBP).Prefixes, op.Rule.GetGrammar())
	}

	for _, op := range config.PostfixOps {
		getLvl(op.LeftBP).Postfixes = append(getLvl(op.LeftBP).Postfixes, syntaxa.Token[TToken, TNodeKind](op.TokenGrammarLabel, op.Token))
	}
	for _, op := range config.PostfixRuleOps {
		getLvl(op.LeftBP).Postfixes = append(getLvl(op.LeftBP).Postfixes, op.Rule.GetGrammar())
	}

	for _, op := range config.InfixOps {
		getLvl(op.LeftBP).Infixes = append(getLvl(op.LeftBP).Infixes, syntaxa.Token[TToken, TNodeKind](op.TokenGrammarLabel, op.Token))
	}
	for _, op := range config.InfixRuleOps {
		getLvl(op.LeftBP).Infixes = append(getLvl(op.LeftBP).Infixes, op.Rule.GetGrammar())
	}

	if config.ImplicitInfix != nil {
		getLvl(config.ImplicitInfix.LeftBP).HasImplicit = true
	}

	// 2. Sort BPs descending (highest precedence binds tightest)
	var bps []int
	for bp := range levelsMap {
		bps = append(bps, bp)
	}
	slices.SortFunc(bps, func(a, b int) int { return cmp.Compare(b, a) })

	// 3. Build the cascade from the inside out
	currentLevel := config.Primary.GetGrammar()

	for _, bp := range bps {
		lvl := levelsMap[bp]

		// Apply Prefixes: (Prefix)* CurrentLevel
		if len(lvl.Prefixes) > 0 {
			prefixChoice := p.safeChoice(grammarLabel, lvl.Prefixes)
			prefixLoop := syntaxa.ZeroOrMore(grammarLabel, prefixChoice)
			currentLevel = syntaxa.Concat(grammarLabel, prefixLoop, currentLevel)
		}

		// Apply Postfixes, Infixes, and Implicit Concat
		var suffixChoices []*syntaxa.Grammar[TToken, TNodeKind]

		if len(lvl.Postfixes) > 0 {
			suffixChoices = append(suffixChoices, p.safeChoice(grammarLabel, lvl.Postfixes))
		}

		if len(lvl.Infixes) > 0 {
			infixChoice := p.safeChoice(grammarLabel, lvl.Infixes)
			// Infix CurrentLevel
			infixConcat := syntaxa.Concat(grammarLabel, infixChoice, currentLevel)
			suffixChoices = append(suffixChoices, infixConcat)
		}

		if lvl.HasImplicit {
			suffixChoices = append(suffixChoices, currentLevel)
		}

		// CurrentLevel (Suffixes)*
		if len(suffixChoices) > 0 {
			suffixLoop := syntaxa.ZeroOrMore(grammarLabel, p.safeChoice(grammarLabel, suffixChoices))
			currentLevel = syntaxa.Concat(grammarLabel, currentLevel, suffixLoop)
		}
	}

	if primary := config.Primary.GetGrammar(); primary != nil && primary.OutputNodeKind != nil {
		currentLevel.OutputNodeKind = primary.OutputNodeKind
	}

	return currentLevel
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) safeChoice(
	label syntaxa.GrammarLabel,
	options []*syntaxa.Grammar[TToken, TNodeKind],
) *syntaxa.Grammar[TToken, TNodeKind] {
	if len(options) == 1 {
		return options[0]
	}
	return syntaxa.Choice(label, options...)
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) runPrattExpression(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	maps prattConfigMaps[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	minBP int,
) Result[TObservation, TToken, TTokenRole, TNodeKind] {

	leftResult := p.parsePrattPrefix(ctx, maps)
	if leftResult.Failed() {
		return leftResult
	}
	left := leftResult.Node

	for {
		peekToken := ctx.Token.Peek(0).Token

		if handled, newLeft := p.tryPostfixOp(ctx, peekToken, left, maps, minBP); handled {
			left = newLeft
			continue
		}
		if handled, newLeft, res := p.tryPostfixRuleOp(ctx, peekToken, left, maps, minBP); handled {
			if res.Failed() {
				return res
			}
			left = newLeft
			continue
		}
		if handled, newLeft, res := p.tryInfixOp(ctx, peekToken, left, maps, minBP); handled {
			if res.Failed() {
				return res
			}
			left = newLeft
			continue
		}
		if handled, newLeft, res := p.tryInfixRuleOp(ctx, peekToken, left, maps, minBP); handled {
			if res.Failed() {
				return res
			}
			left = newLeft
			continue
		}
		if handled, newLeft, res := p.tryImplicitInfix(ctx, peekToken, left, maps, minBP); handled {
			if res.Failed() {
				return res
			}
			left = newLeft
			continue
		}

		break
	}

	return p.sharedCore.buildSuccessRuleResult(left)
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) parsePrattPrefix(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	maps prattConfigMaps[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Result[TObservation, TToken, TTokenRole, TNodeKind] {

	peekToken := ctx.Token.Peek(0).Token

	if handled, res := p.tryPrefixOp(ctx, peekToken, maps); handled {
		return res
	}
	if handled, res := p.tryPrefixRuleOp(ctx, peekToken, maps); handled {
		return res
	}

	return p.parsePrimary(ctx, maps)
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) tryPrefixOp(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	peekToken TToken,
	maps prattConfigMaps[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) (bool, Result[TObservation, TToken, TTokenRole, TNodeKind]) {
	info, ok := maps.prefixMap[peekToken]
	if !ok {
		return false, p.sharedCore.buildSuccessRuleResult(nil)
	}

	opLex := ctx.Token.Consume()
	operandResult := p.runPrattExpression(ctx, maps, info.rightBP)
	if operandResult.Failed() {
		return true, operandResult
	}

	if operandResult.Node == nil {
		ctx.Error.ReportAt(string(maps.name), opLex, "missing operand after prefix operator")
		return true, p.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
	}

	opNode := ctx.Editor.NewNode(info.nodeKind)
	ctx.Editor.AddToken(opNode, opLex)
	ctx.Editor.AttachChild(opNode, operandResult.Node)
	return true, p.sharedCore.buildSuccessRuleResult(opNode)
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) tryPrefixRuleOp(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	peekToken TToken,
	maps prattConfigMaps[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) (bool, Result[TObservation, TToken, TTokenRole, TNodeKind]) {
	info, ok := maps.prefixRuleMap[peekToken]
	if !ok {
		return false, p.sharedCore.buildSuccessRuleResult(nil)
	}

	ruleResult := ctx.ExecuteRule(info.rule, syntaxa.ExecutionNormal)
	if ruleResult.Failed() {
		return true, ruleResult
	}

	operandResult := p.runPrattExpression(ctx, maps, info.rightBP)
	if operandResult.Failed() {
		return true, operandResult
	}
	if operandResult.Node == nil {
		ctx.Error.ReportAt(string(maps.name), ctx.Token.Peek(0), "missing operand after complex prefix operator")
		return true, p.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
	}

	opNode := ctx.Editor.NewNode(info.nodeKind)
	ctx.Editor.AttachChild(opNode, ruleResult.Node)
	ctx.Editor.AttachChild(opNode, operandResult.Node)
	return true, p.sharedCore.buildSuccessRuleResult(opNode)
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) parsePrimary(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	maps prattConfigMaps[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	result := ctx.ExecuteRule(maps.primary, syntaxa.ExecutionNormal)
	if result.Failed() {
		return result
	}

	if result.Node == nil {
		ctx.Error.ReportAt(string(maps.name), ctx.Token.Peek(0), "expected expression")
		return p.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
	}

	return result
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) tryPostfixOp(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	peekToken TToken,
	left *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind],
	maps prattConfigMaps[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	minBP int,
) (bool, *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind]) {
	post, ok := maps.postfixMap[peekToken]
	if !ok || post.leftBP < minBP {
		return false, nil
	}

	opLex := ctx.Token.Consume()
	opNode := ctx.Editor.NewNode(post.nodeKind)
	ctx.Editor.AddToken(opNode, opLex)
	ctx.Editor.AttachChild(opNode, left)
	return true, opNode
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) tryPostfixRuleOp(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	peekToken TToken,
	left *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind],
	maps prattConfigMaps[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	minBP int,
) (bool, *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind], Result[TObservation, TToken, TTokenRole, TNodeKind]) {
	postRule, ok := maps.postfixRuleMap[peekToken]
	if !ok || postRule.leftBP < minBP {
		return false, nil, p.sharedCore.buildSuccessRuleResult(nil)
	}

	rightResult := ctx.ExecuteRule(postRule.rule, syntaxa.ExecutionNormal)
	if rightResult.Failed() {
		return true, nil, rightResult
	}

	opNode := ctx.Editor.NewNode(postRule.nodeKind)
	ctx.Editor.AttachChild(opNode, left)
	if rightResult.Node != nil {
		ctx.Editor.AttachChild(opNode, rightResult.Node)
	}
	return true, opNode, p.sharedCore.buildSuccessRuleResult(opNode)
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) tryInfixOp(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	peekToken TToken,
	left *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind],
	maps prattConfigMaps[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	minBP int,
) (bool, *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind], Result[TObservation, TToken, TTokenRole, TNodeKind]) {
	inf, ok := maps.infixMap[peekToken]
	if !ok || inf.leftBP < minBP {
		return false, nil, p.sharedCore.buildSuccessRuleResult(nil)
	}

	opLex := ctx.Token.Consume()
	rightResult := p.runPrattExpression(ctx, maps, inf.rightBP)
	if rightResult.Failed() {
		return true, nil, rightResult
	}
	if rightResult.Node == nil {
		ctx.Error.ReportAt(string(maps.name), opLex, "missing right operand")
		return true, nil, p.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
	}

	opNode := ctx.Editor.NewNode(inf.nodeKind)
	ctx.Editor.AddToken(opNode, opLex)
	ctx.Editor.AttachChild(opNode, left)
	ctx.Editor.AttachChild(opNode, rightResult.Node)
	return true, opNode, p.sharedCore.buildSuccessRuleResult(opNode)
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) tryInfixRuleOp(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	peekToken TToken,
	left *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind],
	maps prattConfigMaps[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	minBP int,
) (bool, *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind], Result[TObservation, TToken, TTokenRole, TNodeKind]) {
	infRule, ok := maps.infixRuleMap[peekToken]
	if !ok || infRule.leftBP < minBP {
		return false, nil, p.sharedCore.buildSuccessRuleResult(nil)
	}

	midResult := ctx.ExecuteRule(infRule.rule, syntaxa.ExecutionNormal)
	if midResult.Failed() {
		return true, nil, midResult
	}

	rightResult := p.runPrattExpression(ctx, maps, infRule.rightBP)
	if rightResult.Failed() {
		return true, nil, rightResult
	}
	if rightResult.Node == nil {
		ctx.Error.ReportAt(string(maps.name), ctx.Token.Peek(0), "missing right operand after complex infix operator")
		return true, nil, p.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
	}

	opNode := ctx.Editor.NewNode(infRule.nodeKind)
	ctx.Editor.AttachChild(opNode, left)
	if midResult.Node != nil {
		ctx.Editor.AttachChild(opNode, midResult.Node)
	}
	ctx.Editor.AttachChild(opNode, rightResult.Node)
	return true, opNode, p.sharedCore.buildSuccessRuleResult(opNode)
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) tryImplicitInfix(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	peekToken TToken,
	left *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind],
	maps prattConfigMaps[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	minBP int,
) (bool, *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind], Result[TObservation, TToken, TTokenRole, TNodeKind]) {
	if maps.implicitOp == nil || maps.implicitOp.LeftBP < minBP || !maps.predictMap[peekToken] {
		return false, nil, p.sharedCore.buildSuccessRuleResult(nil)
	}

	rightResult := p.runPrattExpression(ctx, maps, maps.implicitOp.RightBP)
	if rightResult.Failed() {
		return true, nil, rightResult
	}
	if rightResult.Node == nil {
		ctx.Error.ReportAt(string(maps.name), ctx.Token.Peek(0), "missing right operand in concatenation")
		return true, nil, p.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
	}

	opNode := ctx.Editor.NewNode(maps.implicitOp.NodeKind)
	ctx.Editor.AttachChild(opNode, left)
	ctx.Editor.AttachChild(opNode, rightResult.Node)
	return true, opNode, p.sharedCore.buildSuccessRuleResult(opNode)
}
