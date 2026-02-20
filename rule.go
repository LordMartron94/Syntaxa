package syntaxa

import "cmp"

/* RuleResult encapsulates the return value of a parser rule. */
type RuleResult[TObservation cmp.Ordered, TToken, TTokenRole, TKind comparable] struct {
	Node      *SyntaxaASTNode[TObservation, TToken, TTokenRole, TKind]
	Succeeded bool
}

func (r *RuleResult[TObservation, TToken, TTokenRole, TKind]) Failed() bool {
	return !r.Succeeded
}

/*
RuleContract defines the semantics for the grammar rule.

This helps with debugging languages, as violations will return in a panic.
*/
type RuleContract struct {
	/*
		MustConsume means this rule HAS to consume tokens.

		If MustConsume is set to true and the rule returns success without token consumption,
		the parser panics.
	*/
	MustConsume bool

	/*
		MustReturnNode indicates that on success this rule must produce a node to attach.

		If MustReturnNode is set to true and the rule returns success without a node,
		the parser panics.
	*/
	MustReturnNode bool
}

/*
RuleExecutionMode controls how the parser engine interprets rule failures.

Syntaxa distinguishes between committed grammar execution and speculative
execution used for branching, optional constructs, and lookahead.

The execution mode determines whether a rule failure represents a real
syntax error that must trigger diagnostics and recovery, or a normal
control-flow mismatch that should be silently rolled back.

All rule bodies are identical; only the execution mode changes how the
engine reacts to failure.
*/
type RuleExecutionMode int

const (
	/*
		ExecutionNormal executes a rule in committed grammar mode.

		In this mode, a rule failure represents a real syntax error.

		Semantics:
		  - the parser state is rolled back to the rule entry snapshot
		  - syntax diagnostics are reported (unless explicitly suppressed)
		  - recovery synchronization is attempted using the rule’s recovery set

		This mode is used for:
		  - required grammar productions
		  - structural rules
		  - token expectations
		  - top-level parsing

		A failure in this mode indicates invalid input.
	*/
	ExecutionNormal RuleExecutionMode = iota + 1

	/*
		ExecutionProbe executes a rule in speculative mode.

		In this mode, a rule failure represents a normal grammar mismatch,
		not a syntax error.

		Semantics:
		  - the parser state is rolled back to the rule entry snapshot
		  - no diagnostics are committed
		  - no recovery synchronization is performed

		This mode is used for:
		  - Optional(...)
		  - Choice / ordered alternatives
		  - lookahead predicates
		  - backtracking and branching constructs

		A failure in this mode is expected and does not indicate invalid input.
	*/
	ExecutionProbe
)

/*
ParserRuleExecutor is the functional body of a rule.
*/
type ParserRuleExecutor[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
] func(ctx *ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) RuleResult[TObservation, TToken, TTokenRole, TNodeKind]

/*
ParserRule attempts to parse input at the current cursor position.

On failure, consumption is automatically rolled back by the engine.

The struct is deliberately opaque, such that all creation and execution goes through the engine.
*/
type ParserRule[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
] struct {
	name          string
	expectedLabel string

	executionFn ParserRuleExecutor[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]

	contract       RuleContract
	recoveryTokens []TToken
}

func (p *ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) GetName() string {
	return p.name
}

func (p *ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) GetRecoveryTokens() []TToken {
	cp := make([]TToken, len(p.recoveryTokens))
	copy(cp, p.recoveryTokens)

	return cp
}

func (p *ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) GetContract() RuleContract {
	return p.contract
}

func (p *ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) GetExpectedLabel() string {
	return p.expectedLabel
}

/*
ParserRuleCreate constructs a rule.

The Syntaxa core on purpose does not provide implemented rules.
This is to keep the core clean and maintainable.

For preset behaviour, use Syntaxa/rule.
For advanced behaviour, construct rules manually through this function.
*/
func ParserRuleCreate[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	ruleName, expectedLabel string,
	executionFn ParserRuleExecutor[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	contract RuleContract,
	recoveryTokens []TToken,
) ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{
		name:           ruleName,
		expectedLabel:  expectedLabel,
		executionFn:    executionFn,
		contract:       contract,
		recoveryTokens: recoveryTokens,
	}
}
