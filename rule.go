package syntaxa

import (
	"cmp"
	"fmt"
)

/*
FailureKind distinguishes between multiple kinds of failures.

A FailureNoMatch does not result in any syntax errors.
*/
type FailureKind int

const (
	/*
		FailureError indicates this is a true syntax error.

		This means a construct started but got malformed.
	*/
	FailureError FailureKind = iota + 1

	/*
		FailureNoMatch indicates the rule failed because the token(s) didn't match.

		This is returned when the first token did not match.
	*/
	FailureNoMatch
)

func (f FailureKind) String() string {
	switch f {
	case FailureError:
		return "Error"
	case FailureNoMatch:
		return "No Match"
	default:
		return "UNKNOWN KIND"
	}
}

/* RuleResult encapsulates the return value of a parser rule. */
type RuleResult[TObservation cmp.Ordered, TToken, TTokenRole, TKind comparable] struct {
	Node      *SyntaxaLSTNode[TObservation, TToken, TTokenRole, TKind]
	Succeeded bool

	Kind FailureKind

	IsFragment bool

	/*
		ConsumeSyncToken controls whether the parser consumes the recovery sync token after a failed rule.

		When true (default), the engine consumes the token at which recovery landed. When false, the sync
		token is left in the stream for the parent (e.g. so a TransparentNest can consume its closing
		delimiter). Set by the engine on FailureError after recovery when the rule has noConsumeOnRecoveryTokens
		and recovery landed on one of them. Repetition loops (NOrMore, TransparentZeroOrMore) use this to
		decide whether to retry or propagate: if false, they propagate the error instead of retrying.
	*/
	ConsumeSyncToken bool
}

func (r *RuleResult[TObservation, TToken, TTokenRole, TKind]) Failed() bool {
	return !r.Succeeded
}

func (r *RuleResult[TObservation, TToken, TTokenRole, TKind]) Format() string {
	return fmt.Sprintf("node filled? %v, success? %v, kind? %s", r.Node != nil, r.Succeeded, r.Kind.String())
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

type RuleIdentity struct {
	RuleName      RuleLabel
	GrammarLabel  GrammarLabel
	ExpectedLabel string
}

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
	identity RuleIdentity

	executionFn ParserRuleExecutor[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]

	contract       RuleContract
	recoveryTokens []TToken

	/*
		noConsumeOnRecoveryTokens are sync tokens at which recovery should not consume.

		When recovery lands on one of these, the engine sets result.ConsumeSyncToken = false so the
		parent rule can consume the token (e.g. a nest consuming its closing brace).
	*/
	noConsumeOnRecoveryTokens []TToken

	grammar *Grammar[TToken, TNodeKind]

	isRecoveryBarrier bool
}

func ParserRuleApplyRecoverySpec[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	rule ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	spec RecoverySpec[TToken],
) ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	rule.recoveryTokens = append([]TToken(nil), spec.Tokens...)
	rule.noConsumeOnRecoveryTokens = append([]TToken(nil), spec.NoConsume...)
	return rule
}

func (p *ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) GetIdentity() RuleIdentity {
	return p.identity
}

func (p *ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) GetName() RuleLabel {
	return p.identity.RuleName
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
	return p.identity.ExpectedLabel
}

func (p *ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) GetGrammarLabel() GrammarLabel {
	return p.identity.GrammarLabel
}

func (p *ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) GetGrammar() *Grammar[TToken, TNodeKind] {
	return p.grammar
}

func (r ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) WithRecoveryBarrier() ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	r.isRecoveryBarrier = true
	return r
}

func (r ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) IsRecoveryBarrier() bool {
	return r.isRecoveryBarrier
}

/*
IsNoConsumeRecoveryToken returns true if token is in the rule's noConsumeOnRecoveryTokens set.

Used by the engine after recovery to set result.ConsumeSyncToken so the sync token is left in the stream.
*/
func (p *ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) IsNoConsumeRecoveryToken(token TToken) bool {
	for _, t := range p.noConsumeOnRecoveryTokens {
		if t == token {
			return true
		}
	}
	return false
}

/*
IsSyncToken returns true if token is in the rule's recovery set (recoveryTokens or noConsumeOnRecoveryTokens).

Used by the engine to avoid reporting spurious "unexpected X" errors when the current token is a sync
token used for recovery (e.g. semicolon or closing brace).
*/
func (p *ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) IsSyncToken(token TToken) bool {
	for _, t := range p.recoveryTokens {
		if t == token {
			return true
		}
	}
	for _, t := range p.noConsumeOnRecoveryTokens {
		if t == token {
			return true
		}
	}
	return false
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
	identity RuleIdentity,
	executionFn ParserRuleExecutor[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	contract RuleContract,
	recoveryTokens []TToken,
	grammar *Grammar[TToken, TNodeKind],
	noConsumeOnRecovery []TToken,
) ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	noConsume := noConsumeOnRecovery
	if noConsume == nil {
		noConsume = []TToken{}
	}
	if grammar != nil {
		grammar.RecoveryTokens = append([]TToken(nil), recoveryTokens...)
		grammar.NoConsumeOnRecoveryTokens = append([]TToken(nil), noConsume...)
	}
	return ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{
		identity:                  identity,
		executionFn:               executionFn,
		contract:                  contract,
		recoveryTokens:            recoveryTokens,
		noConsumeOnRecoveryTokens: noConsume,
		grammar:                   grammar,
	}
}
