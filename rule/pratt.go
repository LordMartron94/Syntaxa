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
	Predict  func(peek TToken) bool
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

	name := p.sharedCore.createRuleName("Expression", grammarLabel)
	identity := p.sharedCore.createRuleIdentity(name, grammarLabel, "expression")

	grammar := p.buildPrattGrammar(grammarLabel, primary, config.PrefixOps, config.InfixOps)
	syntaxa.MarkAsContextBoundary(grammar)

	exec := func(ctx *syntaxa.ExecRuleContext[
		TObservation, TToken, TTokenRole, TLexerState, TNodeKind,
	]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
		return p.runPrattExpression(ctx, name, primary, prefixMap, infixMap, config.ImplicitInfix, 0)
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
	implicitOp *PrattImplicitInfix[TToken, TNodeKind], // Added parameter
	minBP int,
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	// 1. Handle Prefix / Primary Atom
	// Ensure parsePrattPrefix is updated to pass the implicitOp to recursive calls
	leftResult := p.parsePrattPrefix(ctx, name, primary, prefixMap, infixMap, implicitOp)
	if leftResult.Failed() {
		return leftResult
	}
	left := leftResult.Node

	// 2. Precedence Climbing Loop
	for {
		peek := ctx.Token.Peek(0)
		peekToken := peek.Token

		// OPTION A: Explicit Infix Operator (e.g., '|')
		if inf, ok := infixMap[peekToken]; ok {
			if inf.leftBP < minBP {
				return p.sharedCore.buildSuccessRuleResult(left)
			}

			// Consume the operator token
			opLex := ctx.Token.Consume()

			// Recurse to find the right operand
			rightResult := p.runPrattExpression(ctx, name, primary, prefixMap, infixMap, implicitOp, inf.rightBP)
			if rightResult.Failed() {
				return rightResult
			}

			right := rightResult.Node
			if right == nil {
				ctx.Error.ReportAt(string(name), opLex, "missing right operand")
				return p.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
			}

			// Construct the Infix Node
			opNode := ctx.Editor.NewNode(inf.nodeKind)
			ctx.Editor.AddToken(opNode, opLex)
			ctx.Editor.AttachChild(opNode, left)
			ctx.Editor.AttachChild(opNode, right)
			left = opNode
			continue
		}

		// OPTION B: Implicit Juxtaposition (Concatenation)
		// Check if we have an implicit config and if the next token starts a new primary
		if implicitOp != nil && implicitOp.LeftBP >= minBP && implicitOp.Predict(peekToken) {

			// Note: We do NOT consume a token here. Adjacency is the "operator".
			rightResult := p.runPrattExpression(ctx, name, primary, prefixMap, infixMap, implicitOp, implicitOp.RightBP)
			if rightResult.Failed() {
				return rightResult
			}

			right := rightResult.Node
			if right == nil {
				// This shouldn't happen if Predict is accurate, but we guard against it.
				peekLex := ctx.Token.Peek(0)
				ctx.Error.ReportAt(string(name), peekLex, "missing right operand in concatenation")
				return p.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
			}

			// Construct the Implicit Infix Node (Concat)
			opNode := ctx.Editor.NewNode(implicitOp.NodeKind)
			// No token is added to the node since the operator is implicit
			ctx.Editor.AttachChild(opNode, left)
			ctx.Editor.AttachChild(opNode, right)
			left = opNode
			continue
		}

		// OPTION C: No valid operator found or precedence is too low
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
	implicitOp *PrattImplicitInfix[TToken, TNodeKind], // Added parameter
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	peek := ctx.Token.Peek(0)

	// 1. Check if the current token is a Prefix Operator (e.g., !, -, ~)
	if info, ok := prefixMap[peek.Token]; ok {
		opLex := ctx.Token.Consume()

		// Recurse using runPrattExpression to handle binding power and implicit ops
		operandResult := p.runPrattExpression(ctx, name, primary, prefixMap, infixMap, implicitOp, info.rightBP)
		if operandResult.Failed() {
			return operandResult
		}

		operand := operandResult.Node
		if operand == nil {
			ctx.Error.ReportAt(string(name), opLex, "missing operand after prefix operator")
			return p.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
		}

		// Construct the Prefix Node
		nodeKind := info.nodeKind
		opNode := ctx.Editor.NewNode(nodeKind)
		ctx.Editor.AddToken(opNode, opLex)
		ctx.Editor.AttachChild(opNode, operand)

		return p.sharedCore.buildSuccessRuleResult(opNode)
	}

	// 2. If not a prefix, it must be a Primary Atom (Literal, Identifier, Group)
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
