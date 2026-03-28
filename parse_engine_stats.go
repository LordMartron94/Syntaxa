package syntaxa

/*
ParseEngineStats accumulates optional parser-engine counters when a non-nil pointer is wired
into ExecRuleContext (same pattern as ParseStreamStats).

RuleExecuteCalls counts each syntaxaParserExecuteRule entry (normal and probe). RuleExecuteSucceeded
and RuleExecuteFailed reflect the final outcome after handleFailureState. ReferenceDirectCalls
counts registry invocations via ExecuteReference, which bypass syntaxaParserExecuteRule.

RecoveryDiscardRawConsumes counts raw token consumes inside performRecovery’s discard loop.
RecoverySyncRawConsumes counts the optional sync ConsumeRaw after a successful recovery.

Choice counters are incremented once per Choice rule execution (dispatch path) plus ChoiceBranchTries
per sub-rule attempt inside the choice loops.

LSTNodePoolHits / LSTNodePoolMisses count nodeAcquire free-list reuse vs allocation path.
*/
type ParseEngineStats struct {
	RuleExecuteCalls           uint64
	RuleExecuteSucceeded       uint64
	RuleExecuteFailed          uint64
	ProbeInvocations           uint64
	ProbeFailures              uint64
	ReferenceDirectCalls       uint64
	RecoveryAttempts           uint64
	RecoveriesSucceeded        uint64
	RecoveryDiscardRawConsumes uint64
	RecoverySyncRawConsumes    uint64

	ChoiceDispatchFallback   uint64
	ChoiceDispatchCandidates uint64
	ChoiceDispatchNoMatch    uint64
	ChoiceBranchTries        uint64

	LSTNodePoolHits   uint64
	LSTNodePoolMisses uint64
}
