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
TokenGrammarLabel is the GrammarLabel for this operator's token in the grammar IR; required (panic if empty).
*/
type PrattPrefixOp[TToken, TNodeKind comparable] struct {
	Token             TToken
	RightBP           int
	NodeKind          TNodeKind
	TokenGrammarLabel syntaxa.GrammarLabel
}

/*
PrattInfixOp describes an infix (binary) operator for Pratt expression parsing.

LeftBP and RightBP define precedence and associativity: when we have a left operand
and see this operator, we consume it and parse the right operand with RightBP.
Left-associative: use RightBP < LeftBP (e.g. RightBP = LeftBP - 1).
Right-associative: use RightBP = LeftBP.
TokenGrammarLabel is the GrammarLabel for this operator's token in the grammar IR; required (panic if empty).
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

/*
PrattConfig holds the configuration for a Pratt-style expression rule.

Primary is the atom rule (literal, identifier, parenthesized expression); it is
invoked for the first element and after each prefix operator.
PrefixOps and InfixOps define the operators; binding powers determine precedence.
RecoveryTokens are used by the engine to resync on expression boundaries (e.g. ",", ")", ";").
*/
type PrattConfig[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	Primary        Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
	PrefixOps      []PrattPrefixOp[TToken, TNodeKind]
	InfixOps       []PrattInfixOp[TToken, TNodeKind]
	ImplicitInfix  *PrattImplicitInfix[TToken, TNodeKind]
	RecoveryTokens []TToken
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
	grammarLabel syntaxa.GrammarLabel,
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

	// Dynamic FIRST set computation for Implicit Concatenation
	var predictMap map[TToken]bool
	if config.ImplicitInfix != nil {
		predictMap = make(map[TToken]bool)

		// 1. Get FIRST set of the Primary rule via your existing Analysis
		primaryGrammar := primary.GetGrammar()
		if primaryGrammar != nil {
			// Ensure paths are finalized so analysis has valid keys
			primaryGrammar.FinalizeNodePaths()
			analysis := syntaxa.ComputeAnalysisSingleTree(primaryGrammar)
			rootKey := syntaxa.NodeKeyFromPath(*primaryGrammar.NodePath)

			for tok := range analysis.First[rootKey] {
				predictMap[tok] = true
			}
		}

		// 2. Prefix operators also start an expression, so they trigger concatenation
		for tok := range prefixMap {
			predictMap[tok] = true
		}
	}

	name := p.sharedCore.createRuleName("Expression", grammarLabel)
	identity := p.sharedCore.createRuleIdentity(name, grammarLabel, "expression")

	grammar := p.buildPrattGrammar(grammarLabel, primary, config.PrefixOps, config.InfixOps)
	syntaxa.MarkAsContextBoundary(grammar)

	exec := func(ctx *syntaxa.ExecRuleContext[
		TObservation, TToken, TTokenRole, TLexerState, TNodeKind,
	]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
		// Pass both the config AND the pre-computed map
		return p.runPrattExpression(ctx, name, primary, prefixMap, infixMap, config.ImplicitInfix, predictMap, 0)
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

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) buildPrattGrammar(
	grammarLabel syntaxa.GrammarLabel,
	primary Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	prefixOps []PrattPrefixOp[TToken, TNodeKind],
	infixOps []PrattInfixOp[TToken, TNodeKind],
) *syntaxa.Grammar[TToken] {
	children := []*syntaxa.Grammar[TToken]{primary.GetGrammar()}
	for _, op := range prefixOps {
		if op.TokenGrammarLabel == "" {
			panic("Pratt prefix op has empty TokenGrammarLabel; explicitness required")
		}
		children = append(children, syntaxa.Token(op.TokenGrammarLabel, op.Token))
	}
	for _, op := range infixOps {
		if op.TokenGrammarLabel == "" {
			panic("Pratt infix op has empty TokenGrammarLabel; explicitness required")
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
	name syntaxa.RuleLabel,
	primary Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	prefixMap map[TToken]prefixInfo[TNodeKind],
	infixMap map[TToken]infixInfo[TNodeKind],
	implicitOp *PrattImplicitInfix[TToken, TNodeKind],
	predictMap map[TToken]bool,
	minBP int,
) Result[TObservation, TToken, TTokenRole, TNodeKind] {

	// 1. Parse the left side (Prefix or Primary)
	leftResult := p.parsePrattPrefix(ctx, name, primary, prefixMap, infixMap, implicitOp, predictMap)
	if leftResult.Failed() {
		return leftResult
	}
	left := leftResult.Node

	// 2. Climb precedence
	for {
		peek := ctx.Token.Peek(0)
		peekToken := peek.Token

		// OPTION A: Explicit Infix Operator
		if inf, ok := infixMap[peekToken]; ok {
			if inf.leftBP < minBP {
				break
			}

			opLex := ctx.Token.Consume()
			rightResult := p.runPrattExpression(ctx, name, primary, prefixMap, infixMap, implicitOp, predictMap, inf.rightBP)
			if rightResult.Failed() {
				return rightResult
			}

			right := rightResult.Node
			if right == nil {
				ctx.Error.ReportAt(string(name), opLex, "missing right operand")
				return p.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
			}

			opNode := ctx.Editor.NewNode(inf.nodeKind)
			ctx.Editor.AddToken(opNode, opLex)
			ctx.Editor.AttachChild(opNode, left)
			ctx.Editor.AttachChild(opNode, right)
			left = opNode
			continue
		}

		// OPTION B: Implicit Juxtaposition (Concatenation)
		if implicitOp != nil && implicitOp.LeftBP >= minBP && predictMap[peekToken] {

			// We do NOT consume a token; adjacency IS the operator.
			rightResult := p.runPrattExpression(ctx, name, primary, prefixMap, infixMap, implicitOp, predictMap, implicitOp.RightBP)
			if rightResult.Failed() {
				return rightResult
			}

			right := rightResult.Node
			if right == nil {
				peekLex := ctx.Token.Peek(0)
				ctx.Error.ReportAt(string(name), peekLex, "missing right operand in concatenation")
				return p.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
			}

			// Create the node representing the implicit sequence
			opNode := ctx.Editor.NewNode(implicitOp.NodeKind)
			ctx.Editor.AttachChild(opNode, left)
			ctx.Editor.AttachChild(opNode, right)
			left = opNode
			continue
		}

		// No more operators to process at this precedence level
		break
	}

	return p.sharedCore.buildSuccessRuleResult(left)
}

func (p *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) parsePrattPrefix(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	name syntaxa.RuleLabel,
	primary Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	prefixMap map[TToken]prefixInfo[TNodeKind],
	infixMap map[TToken]infixInfo[TNodeKind],
	implicitOp *PrattImplicitInfix[TToken, TNodeKind],
	predictMap map[TToken]bool,
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	peek := ctx.Token.Peek(0)

	// Check for Prefix Operator
	if info, ok := prefixMap[peek.Token]; ok {
		opLex := ctx.Token.Consume()

		operandResult := p.runPrattExpression(ctx, name, primary, prefixMap, infixMap, implicitOp, predictMap, info.rightBP)
		if operandResult.Failed() {
			return operandResult
		}

		operand := operandResult.Node
		if operand == nil {
			ctx.Error.ReportAt(string(name), opLex, "missing operand after prefix operator")
			return p.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
		}

		opNode := ctx.Editor.NewNode(info.nodeKind)
		ctx.Editor.AddToken(opNode, opLex)
		ctx.Editor.AttachChild(opNode, operand)
		return p.sharedCore.buildSuccessRuleResult(opNode)
	}

	// Fallback to the Primary rule (Literals, VarRefs, etc.)
	result := ctx.ExecuteRule(primary, syntaxa.ExecutionNormal)
	if result.Failed() {
		return result
	}

	if result.Node == nil {
		peekLex := ctx.Token.Peek(0)
		ctx.Error.ReportAt(string(name), peekLex, "expected expression")
		return p.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
	}

	return result
}
