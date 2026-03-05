package rule

import (
	"cmp"
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
PrattInfixOp describes an infix (binary) operator for Pratt expression parsing.
*/
type PrattInfixOp[TToken, TNodeKind comparable] struct {
	Token             TToken
	LeftBP            int
	RightBP           int
	NodeKind          TNodeKind
	TokenGrammarLabel syntaxa.GrammarLabel
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
PrattPostfixRuleOp describes a postfix operator that requires executing a full rule
(e.g., composite bounds `{min,max}`) rather than consuming a single atomic token.
*/
type PrattPostfixRuleOp[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	TriggerToken      TToken
	LeftBP            int
	NodeKind          TNodeKind
	Rule              Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
	TokenGrammarLabel syntaxa.GrammarLabel
}

/*
PrattConfig holds the configuration for a Pratt-style expression rule.
*/
type PrattConfig[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	Primary        Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
	PrefixOps      []PrattPrefixOp[TToken, TNodeKind]
	PostfixOps     []PrattPostfixOp[TToken, TNodeKind]
	PostfixRuleOps []PrattPostfixRuleOp[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
	InfixOps       []PrattInfixOp[TToken, TNodeKind]
	ImplicitInfix  *PrattImplicitInfix[TToken, TNodeKind]
	RecoveryTokens []TToken
}

// ------------------------------------------------------------- INTERNAL STATE

type prefixInfo[TNodeKind comparable] struct {
	rightBP  int
	nodeKind TNodeKind
}

type infixInfo[TNodeKind comparable] struct {
	leftBP   int
	rightBP  int
	nodeKind TNodeKind
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
prattConfigMaps acts as a transport object to eliminate bloated function signatures.
*/
type prattConfigMaps[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	primary        Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
	name           syntaxa.RuleLabel
	prefixMap      map[TToken]prefixInfo[TNodeKind]
	infixMap       map[TToken]infixInfo[TNodeKind]
	postfixMap     map[TToken]postfixInfo[TNodeKind]
	postfixRuleMap map[TToken]postfixRuleInfo[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
	implicitOp     *PrattImplicitInfix[TToken, TNodeKind]
	predictMap     map[TToken]bool
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
		if hasImplicitInfix {
			maps.predictMap = p.buildPredictMap(ctx, config.Primary, maps.prefixMap)
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
		primary:        config.Primary,
		name:           p.sharedCore.createRuleName("Expression", grammarLabel),
		prefixMap:      make(map[TToken]prefixInfo[TNodeKind]),
		infixMap:       make(map[TToken]infixInfo[TNodeKind]),
		postfixMap:     make(map[TToken]postfixInfo[TNodeKind]),
		postfixRuleMap: make(map[TToken]postfixRuleInfo[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]),
		implicitOp:     config.ImplicitInfix,
	}

	for _, op := range config.PrefixOps {
		maps.prefixMap[op.Token] = prefixInfo[TNodeKind]{rightBP: op.RightBP, nodeKind: op.NodeKind}
	}
	for _, op := range config.InfixOps {
		maps.infixMap[op.Token] = infixInfo[TNodeKind]{leftBP: op.LeftBP, rightBP: op.RightBP, nodeKind: op.NodeKind}
	}
	for _, op := range config.PostfixOps {
		maps.postfixMap[op.Token] = postfixInfo[TNodeKind]{leftBP: op.LeftBP, nodeKind: op.NodeKind}
	}
	for _, op := range config.PostfixRuleOps {
		maps.postfixRuleMap[op.TriggerToken] = postfixRuleInfo[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{
			leftBP:   op.LeftBP,
			nodeKind: op.NodeKind,
			rule:     op.Rule,
		}
	}

	return maps
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) buildPredictMap(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	primary Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	prefixMap map[TToken]prefixInfo[TNodeKind],
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
	return predictMap
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) buildPrattGrammar(
	grammarLabel syntaxa.GrammarLabel,
	config PrattConfig[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) *syntaxa.Grammar[TToken] {
	children := []*syntaxa.Grammar[TToken]{config.Primary.GetGrammar()}

	for _, op := range config.PrefixOps {
		if op.TokenGrammarLabel == "" {
			panic("Pratt prefix op has empty TokenGrammarLabel")
		}
		children = append(children, syntaxa.Token(op.TokenGrammarLabel, op.Token))
	}
	for _, op := range config.PostfixOps {
		if op.TokenGrammarLabel == "" {
			panic("Pratt postfix op has empty TokenGrammarLabel")
		}
		children = append(children, syntaxa.Token(op.TokenGrammarLabel, op.Token))
	}
	for _, op := range config.PostfixRuleOps {
		if op.TokenGrammarLabel == "" {
			panic("Pratt postfix rule op has empty TokenGrammarLabel")
		}
		// The grammar for a complex rule postfix incorporates its internal sub-grammar
		children = append(children, op.Rule.GetGrammar())
	}
	for _, op := range config.InfixOps {
		if op.TokenGrammarLabel == "" {
			panic("Pratt infix op has empty TokenGrammarLabel")
		}
		children = append(children, syntaxa.Token(op.TokenGrammarLabel, op.Token))
	}

	if len(children) == 1 {
		return children[0]
	}
	return syntaxa.Choice(grammarLabel, children...)
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

	right := rightResult.Node
	if right == nil {
		ctx.Error.ReportAt(string(maps.name), opLex, "missing right operand")
		return true, nil, p.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
	}

	opNode := ctx.Editor.NewNode(inf.nodeKind)
	ctx.Editor.AddToken(opNode, opLex)
	ctx.Editor.AttachChild(opNode, left)
	ctx.Editor.AttachChild(opNode, right)

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

	right := rightResult.Node
	if right == nil {
		peekLex := ctx.Token.Peek(0)
		ctx.Error.ReportAt(string(maps.name), peekLex, "missing right operand in concatenation")
		return true, nil, p.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
	}

	opNode := ctx.Editor.NewNode(maps.implicitOp.NodeKind)
	ctx.Editor.AttachChild(opNode, left)
	ctx.Editor.AttachChild(opNode, right)

	return true, opNode, p.sharedCore.buildSuccessRuleResult(opNode)
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) parsePrattPrefix(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	maps prattConfigMaps[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Result[TObservation, TToken, TTokenRole, TNodeKind] {

	peek := ctx.Token.Peek(0)

	if info, ok := maps.prefixMap[peek.Token]; ok {
		opLex := ctx.Token.Consume()

		operandResult := p.runPrattExpression(ctx, maps, info.rightBP)
		if operandResult.Failed() {
			return operandResult
		}

		operand := operandResult.Node
		if operand == nil {
			ctx.Error.ReportAt(string(maps.name), opLex, "missing operand after prefix operator")
			return p.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
		}

		opNode := ctx.Editor.NewNode(info.nodeKind)
		ctx.Editor.AddToken(opNode, opLex)
		ctx.Editor.AttachChild(opNode, operand)
		return p.sharedCore.buildSuccessRuleResult(opNode)
	}

	result := ctx.ExecuteRule(maps.primary, syntaxa.ExecutionNormal)
	if result.Failed() {
		return result
	}

	if result.Node == nil {
		peekLex := ctx.Token.Peek(0)
		ctx.Error.ReportAt(string(maps.name), peekLex, "expected expression")
		return p.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
	}

	return result
}
