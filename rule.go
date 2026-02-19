package syntaxa

import "cmp"

/* RuleResult encapsulates the return value of a parser rule. */
type RuleResult[TObservation cmp.Ordered, TToken, TTokenRole, TKind comparable] struct {
	Node     *SyntaxaASTNode[TObservation, TToken, TTokenRole, TKind]
	SkipAdd  bool // Explicitly allow nil.
	Optional bool
}

/*
ParserRule attempts to parse input at the current cursor position.

Contract:
  - On success: returns (node, true) and commits consumption
  - On failure: returns (nil, false); consumption is rolled back
*/
type ParserRule[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
] func(
	token internalRuleExecutionToken,
	ctx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) (RuleResult[TObservation, TToken, TTokenRole, TNodeKind], bool)

/*
RuleSelector is client-owned logic that determines which rule
should be attempted at the current position.

Returning nil indicates that no rule applies and triggers recovery.
*/
type RuleSelector[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
] func(
	ctx SelectRuleContext[TObservation, TToken, TTokenRole],
) ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
