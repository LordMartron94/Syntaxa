package rule

import (
	"cmp"
	"syntaxa"
)

// ------------------------------------------------------------- PRATT CONFIG (DATA ONLY)

/*
PrattPrefixOp describes a prefix operator for Pratt expression parsing.

RightBP is the binding power used when parsing the operand to the right.
NodeKind is the LST node kind for the prefix operator node (one child: the operand).
*/
type PrattPrefixOp[TToken, TNodeKind comparable] struct {
	Token   TToken
	RightBP int
	NodeKind TNodeKind
}

/*
PrattInfixOp describes an infix (binary) operator for Pratt expression parsing.

LeftBP and RightBP define precedence and associativity: when we have a left operand
and see this operator, we consume it and parse the right operand with RightBP.
Left-associative: use RightBP < LeftBP (e.g. RightBP = LeftBP - 1).
Right-associative: use RightBP = LeftBP.
*/
type PrattInfixOp[TToken, TNodeKind comparable] struct {
	Token    TToken
	LeftBP   int
	RightBP  int
	NodeKind TNodeKind
}

/*
PrattConfig holds the configuration for a Pratt-style expression rule.

Primary is the atom rule (literal, identifier, parenthesized expression); it is
invoked for the first element and after each prefix operator.
PrefixOps and InfixOps define the operators; binding powers determine precedence.
RecoveryTokens are used by the engine to resync on expression boundaries (e.g. ",", ")", ";").
*/
type PrattConfig[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	Primary         Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
	PrefixOps       []PrattPrefixOp[TToken, TNodeKind]
	InfixOps        []PrattInfixOp[TToken, TNodeKind]
	RecoveryTokens  []TToken
}

// ------------------------------------------------------------- PRATT ENDPOINT

type prattEndpoint[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	sharedCore *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
}

type prefixInfo[TNodeKind comparable] struct {
	rightBP  int
	nodeKind TNodeKind
}

type infixInfo[TNodeKind comparable] struct {
	leftBP   int
	rightBP  int
	nodeKind TNodeKind
}

/*
Expression builds a Pratt-style precedence-climbing expression rule.

It parses one expression: primary (or a chain of prefix operators applied to primary),
then zero or more infix operators with leftBP >= 0. Primary failure yields FailureNoMatch.
Missing operand after prefix/infix or unknown operator in expression context yields
FailureError with diagnostics. Recovery uses config.RecoveryTokens.
*/
func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Expression(
	grammarID syntaxa.GrammarID,
	config PrattConfig[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	primary := config.Primary
	prefixMap := make(map[TToken]prefixInfo[TNodeKind])
	for _, op := range config.PrefixOps {
		prefixMap[op.Token] = prefixInfo[TNodeKind]{rightBP: op.RightBP, nodeKind: op.NodeKind}
	}
	infixMap := make(map[TToken]infixInfo[TNodeKind])
	for _, op := range config.InfixOps {
		infixMap[op.Token] = infixInfo[TNodeKind]{
			leftBP:   op.LeftBP,
			rightBP:  op.RightBP,
			nodeKind: op.NodeKind,
		}
	}

	name     := p.sharedCore.createRuleName("Expression", grammarID)
	identity := p.sharedCore.createRuleIdentity(name, grammarID, "expression")

	grammar := p.buildPrattGrammar(grammarID, primary, config.PrefixOps)
	syntaxa.MarkAsContextBoundary(grammar)

	exec := func(ctx *syntaxa.ExecRuleContext[
		TObservation, TToken, TTokenRole, TLexerState, TNodeKind,
	]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
		return p.runPrattExpression(ctx, name, primary, prefixMap, infixMap, 0)
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
		grammar,
	)
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) buildPrattGrammar(
	grammarID syntaxa.GrammarID,
	primary Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	prefixOps []PrattPrefixOp[TToken, TNodeKind],
) *syntaxa.Grammar[TToken] {
	children := []*syntaxa.Grammar[TToken]{primary.GetGrammar()}
	for _, op := range prefixOps {
		children = append(children, syntaxa.Token(grammarID, op.Token))
	}
	if len(children) == 1 {
		return children[0]
	}
	return syntaxa.Choice(grammarID, children...)
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) runPrattExpression(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	name syntaxa.RuleLabel,
	primary Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	prefixMap map[TToken]prefixInfo[TNodeKind],
	infixMap map[TToken]infixInfo[TNodeKind],
	minBP int,
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	leftResult := p.parsePrattPrefix(ctx, name, primary, prefixMap, infixMap)
	if leftResult.Failed() {
		return leftResult
	}
	left := leftResult.Node

	for {
		peek := ctx.Token.Peek(0)
		inf, ok := infixMap[peek.Token]
		if !ok || inf.leftBP < minBP {
			return p.sharedCore.buildSuccessRuleResult(left)
		}
		opLex := ctx.Token.Consume()
		rightResult := p.runPrattExpression(ctx, name, primary, prefixMap, infixMap, inf.rightBP)
		if rightResult.Failed() {
			return rightResult
		}
		right := rightResult.Node
		if right == nil {
			ctx.Error.ReportAt(string(name), opLex, "missing right operand")
			return p.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
		}
		nodeKind := inf.nodeKind
		opNode := ctx.Editor.NewNode(nodeKind)
		ctx.Editor.AddToken(opNode, opLex)
		ctx.Editor.AttachChild(opNode, left)
		ctx.Editor.AttachChild(opNode, right)
		left = opNode
	}
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) parsePrattPrefix(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	name syntaxa.RuleLabel,
	primary Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	prefixMap map[TToken]prefixInfo[TNodeKind],
	infixMap map[TToken]infixInfo[TNodeKind],
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	peek := ctx.Token.Peek(0)
	if info, ok := prefixMap[peek.Token]; ok {
		opLex := ctx.Token.Consume()
		operandResult := p.runPrattExpression(ctx, name, primary, prefixMap, infixMap, info.rightBP)
		if operandResult.Failed() {
			return operandResult
		}
		operand := operandResult.Node
		if operand == nil {
			ctx.Error.ReportAt(string(name), opLex, "missing operand after prefix operator")
			return p.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
		}
		nodeKind := info.nodeKind
		opNode := ctx.Editor.NewNode(nodeKind)
		ctx.Editor.AddToken(opNode, opLex)
		ctx.Editor.AttachChild(opNode, operand)
		return p.sharedCore.buildSuccessRuleResult(opNode)
	}

	result := ctx.ExecuteRule(primary, syntaxa.ExecutionNormal)
	if result.Failed() {
		return result
	}
	if result.Node == nil {
		peek := ctx.Token.Peek(0)
		ctx.Error.ReportAt(string(name), peek, "expected expression")
		return p.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
	}
	return result
}
