package syntaxa

import (
	"fmt"
	"lexarch"
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
type RuleResult[TKind comparable] struct {
	Node      *SyntaxaLSTNode[TKind]
	Succeeded bool

	Kind FailureKind

	IsFragment bool

	/*
		ConsumeSyncToken controls whether the parser consumes the recovery sync token after a failed rule.

		When true, the engine consumes the token at which recovery landed. When false, the sync token is
		left in the stream for the parent (e.g. so a TransparentNest can consume its closing delimiter).
		Set by the engine on FailureError after recovery when the landed token is a noConsumeOnRecovery
		boundary. Repetition loops continue only when ConsumeSyncToken is true and the cursor advanced;
		otherwise they propagate the error. Parent handleFailureState skips a second performRecovery when
		ConsumeSyncToken is false and the cursor already moved (descendant already resynced).
	*/
	ConsumeSyncToken bool
}

func (r *RuleResult[TKind]) Failed() bool {
	return !r.Succeeded
}

func (r *RuleResult[TKind]) Format() string {
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
	TNodeKind comparable,
] func(ctx *ExecRuleContext[TNodeKind]) RuleResult[TNodeKind]

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
type ParserRule[TNodeKind comparable] struct {
	identity RuleIdentity

	executionFn ParserRuleExecutor[TNodeKind]

	contract       RuleContract
	recoveryTokens []lexarch.TokenKind

	/*
		noConsumeOnRecoveryTokens are sync tokens at which recovery should not consume.

		When recovery lands on one of these, the engine sets result.ConsumeSyncToken = false so the
		parent rule can consume the token (e.g. a nest consuming its closing brace).
	*/
	noConsumeOnRecoveryTokens []lexarch.TokenKind

	grammar *Grammar[lexarch.TokenKind, TNodeKind]

	isRecoveryBarrier bool
}

func ParserRuleApplyRecoverySpec[TNodeKind comparable](
	rule ParserRule[TNodeKind],
	spec RecoverySpec,
) ParserRule[TNodeKind] {
	rule.recoveryTokens = append([]lexarch.TokenKind(nil), spec.Tokens...)
	rule.noConsumeOnRecoveryTokens = append([]lexarch.TokenKind(nil), spec.NoConsume...)
	return rule
}

func (p *ParserRule[TNodeKind]) GetIdentity() RuleIdentity {
	return p.identity
}

func (p *ParserRule[TNodeKind]) GetName() RuleLabel {
	return p.identity.RuleName
}

func (p *ParserRule[TNodeKind]) GetRecoveryTokens() []lexarch.TokenKind {
	cp := make([]lexarch.TokenKind, len(p.recoveryTokens))
	copy(cp, p.recoveryTokens)

	return cp
}

func (p *ParserRule[TNodeKind]) GetContract() RuleContract {
	return p.contract
}

func (p *ParserRule[TNodeKind]) GetExpectedLabel() string {
	return p.identity.ExpectedLabel
}

func (p *ParserRule[TNodeKind]) GetGrammarLabel() GrammarLabel {
	return p.identity.GrammarLabel
}

func (p *ParserRule[TNodeKind]) GetGrammar() *Grammar[lexarch.TokenKind, TNodeKind] {
	return p.grammar
}

func (r ParserRule[TNodeKind]) WithRecoveryBarrier() ParserRule[TNodeKind] {
	r.isRecoveryBarrier = true
	return r
}

func (r ParserRule[TNodeKind]) IsRecoveryBarrier() bool {
	return r.isRecoveryBarrier
}

/*
IsNoConsumeRecoveryToken returns true if token is in the rule's noConsumeOnRecoveryTokens set.

Used by the engine after recovery to set result.ConsumeSyncToken so the sync token is left in the stream.
*/
func (p *ParserRule[TNodeKind]) IsNoConsumeRecoveryToken(token lexarch.TokenKind) bool {
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
func (p *ParserRule[TNodeKind]) IsSyncToken(token lexarch.TokenKind) bool {
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
func ParserRuleCreate[TNodeKind comparable](
	identity RuleIdentity,
	executionFn ParserRuleExecutor[TNodeKind],
	contract RuleContract,
	recoveryTokens []lexarch.TokenKind,
	grammar *Grammar[lexarch.TokenKind, TNodeKind],
	noConsumeOnRecovery []lexarch.TokenKind,
) ParserRule[TNodeKind] {
	noConsume := noConsumeOnRecovery
	if noConsume == nil {
		noConsume = []lexarch.TokenKind{}
	}
	if grammar != nil {
		grammar.RecoveryTokens = append([]lexarch.TokenKind(nil), recoveryTokens...)
		grammar.NoConsumeOnRecoveryTokens = append([]lexarch.TokenKind(nil), noConsume...)
	}
	return ParserRule[TNodeKind]{
		identity:                  identity,
		executionFn:               executionFn,
		contract:                  contract,
		recoveryTokens:            recoveryTokens,
		noConsumeOnRecoveryTokens: noConsume,
		grammar:                   grammar,
	}
}
