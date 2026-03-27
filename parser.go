package syntaxa

import (
	"cmp"
	"fmt"
	"lexarch"
)

// ------------------------------------------------------------- POST PROCESSOR

/*
NodePostProcessor is an optional hook invoked immediately after a non-nil LST node
has been constructed during rule execution.

It is intended for local, syntactic-adjacent enrichment of newly created nodes,
such as:

  - normalizing literal values (e.g. parsing numbers, unescaping strings)
  - caching raw token text
  - tagging nodes with lightweight metadata derived from their own tokens
  - performing trivial structural annotations that require no tree context

The post-processor MUST operate purely on the produced node and its own
associated tokens. It MUST NOT:

  - inspect or modify parent or sibling nodes
  - depend on global semantic state (scope, symbols, types, etc.)
  - influence parsing control flow or token consumption
  - perform validation or semantic reasoning

All context-dependent analysis and language semantics belong in explicit
post-LST passes, not in this hook.

Violating these constraints will lead to fragile grammars, broken recovery,
and tightly coupled compiler phases.

The provided FinalizationCtx may be used only for lightweight node-local
utilities (e.g. attribute setting helpers, diagnostics tied to the node's
tokens). It must not be used to access or mutate global semantic structures.

This hook is optional and has zero behavioral impact when unset.
*/
type NodePostProcessor[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind comparable] func(
	node *SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind],
	finalizationCTX *FinalizationCtx[TObservation, TToken, TTokenRole, TNodeKind],
	ruleIdentity RuleIdentity,
)

// =============================================================
// PARSING SNAPSHOT
// =============================================================

/*
ParserSnapshot represents a snapshot of parser progress.

It is opaque by design and only meaningful to the RuleContext
implementation that created it.
*/
type snapshotKind uint8

const (
	snapSlice snapshotKind = iota
	snapLexer
	snapStreaming
)

type ParserSnapshot[TObs cmp.Ordered, TState comparable] struct {
	kind       snapshotKind
	tokenIndex int

	lexerSnap     lexarch.LexerSessionSnapshot[TState]
	streamingSnap lexarch.StreamingLexerSessionSnapshot[TObs, TState]
}

func (p *ParserSnapshot[_, _]) Index() int {
	return p.tokenIndex
}

// =============================================================
// PARSER
// =============================================================

type ParseTraceEvent[TToken any] struct {
	Cursor        int
	RawToken      TToken
	LogicalToken  TToken
	RuleSucceeded bool
	Consumed      bool
	NodeReturned  bool
	RuleName      RuleLabel

	RecoveryAttempted bool
	Recovered         bool
	LandedOnOurs      bool
	RecoveryTokenSet  []TToken
}

type ParseTrace[TToken any] struct {
	Events []ParseTraceEvent[TToken]
}

type ParseResult[TObs cmp.Ordered, TToken, TTokenRole, TKind comparable] struct {
	Root   *SyntaxaLSTNode[TObs, TToken, TTokenRole, TKind]
	Errors *SyntaxErrors[TObs]
	Trace  *ParseTrace[TToken]
}

/*
RuleRegistry maps grammar labels to executable parser rules.

It serves as the late-binding lookup table for GReference nodes.
Only named, context-boundary rules need to be registered here.
*/
type RuleRegistry[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] map[GrammarLabel]ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]

/*
SyntaxaParser is a generic parsing engine using top-down recursive parsing.

It is highly flexible but not paradigm-agnostic: rule factories can provide PEG-, LL(k)-,
or Pratt-style behaviour; LR and other bottom-up paradigms are not supported.

Responsibilities:
  - transactional rule execution
  - centralized error recovery
  - LST assembly
*/
type SyntaxaParser[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable] struct {
	grammarPackage *GrammarPackage[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]
	registry       RuleRegistry[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]

	programRule ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]

	postProcessor NodePostProcessor[TObservation, TToken, TTokenRole, TNodeKind]

	defaultSkipRoles []TTokenRole

	tokenFormatter       func(token TToken) string
	observationFormatter lexarch.ObservationFormatter[TObservation]

	eofToken TToken

	rootNodeKind  TNodeKind
	errorNodeKind TNodeKind

	freezeAfterParse bool
	debugTrace       bool

	// getAnalysis returns nullable/first/follow when set; used by rule context. Optional.
	getAnalysis func() *GrammarAnalysis[TToken]
}

/*
SyntaxaParserCreate constructs a new parser instance from a grammar package.

The package must have been produced with an entry rule (ProducePackage(..., &programRule));
panics if grammarPackage.EntryRuleParserRule is nil.
nodePostProcessor is optional and allowed to be nil.
getAnalysis is optional; when set, the rule context can use it for nullable/first/follow
(e.g. for Predict or Pratt). Pass lowering.GetAnalysis(grammarPackage) or nil.
*/
func SyntaxaParserCreate[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable](
	grammarPackage *GrammarPackage[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	registry RuleRegistry[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	tokenFormatter func(token TToken) string,
	observationFormatter lexarch.ObservationFormatter[TObservation],
	nodePostProcessor NodePostProcessor[TObservation, TToken, TTokenRole, TNodeKind],
	eofToken TToken,
	rootNodeKind, errorNodeKind TNodeKind,
	freezeAfterParse bool,
	getAnalysis func() *GrammarAnalysis[TToken],
) *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState] {
	if grammarPackage == nil || grammarPackage.EntryRuleParserRule == nil {
		panic("SyntaxaParserCreate: grammar package must have EntryRuleParserRule set (produce package with entry rule)")
	}
	boundRegistry := bindMergedRecoveryIntoRegistry(registry, grammarPackage.MergedRecoveryByGrammarLabel)
	programRule := bindMergedRecoveryToRule(*grammarPackage.EntryRuleParserRule, grammarPackage.MergedRecoveryByGrammarLabel)
	return &SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]{
		grammarPackage:       grammarPackage,
		registry:             boundRegistry,
		programRule:          programRule,
		tokenFormatter:       tokenFormatter,
		observationFormatter: observationFormatter,
		postProcessor:        nodePostProcessor,
		eofToken:             eofToken,
		rootNodeKind:         rootNodeKind,
		errorNodeKind:        errorNodeKind,
		freezeAfterParse:     freezeAfterParse,
		defaultSkipRoles:     nil,
		getAnalysis:          getAnalysis,
	}
}

func (p *SyntaxaParser[_, _, TTokenRole, _, _]) SetDefaultSkips(roles ...TTokenRole) {
	p.defaultSkipRoles = append([]TTokenRole(nil), roles...)
}

func (p *SyntaxaParser[_, _, TTokenRole, _, _]) GetDefaultSkips() []TTokenRole {
	return p.defaultSkipRoles
}

func (p *SyntaxaParser[_, _, _, _, _]) EnableTrace(enable bool) {
	p.debugTrace = enable
}

func (p *SyntaxaParser[_, _, _, _, _]) TraceEnabled() bool {
	return p.debugTrace
}

/*
SyntaxaParserParseWithContext drives parsing using a fully
configured RuleContext.

This is the advanced entry point for custom lexer integrations,
streaming scenarios, incremental systems, and complex pipelines.

The caller is responsible for constructing a valid RuleContext
and providing an initialized root node.

Typical use cases:
  - parsing directly from lexer sessions
  - online/streaming parsing
  - multi-stage lexing pipelines
  - editor-driven incremental parsing
  - custom recovery strategies

This function guarantees:
  - transactional rule execution
  - centralized error recovery
  - consistent LST assembly
*/
func SyntaxaParserParseWithContext[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TNodeKind,
	TLexerState comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	ctx *ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) (*SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind], *ParseTrace[TToken], error) {
	return parseWithContext(parser, ctx)
}

// -------------------------------------------------------- PRIVATE HELPERS

func parseWithContext[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	ctx *ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) (*SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind], *ParseTrace[TToken], error) {
	editor := ctx.Editor

	if parser.freezeAfterParse {
		defer editor.Freeze()
	}

	editor.begin()

	programResult := syntaxaParserExecuteRule(parser, ctx, parser.programRule, ExecutionNormal)

	// 1. Intercept top-level NoMatch and upgrade it to a diagnostic error
	if !programResult.Succeeded && programResult.Kind == FailureNoMatch {
		peeked := ctx.Token.Peek(0)
		ctx.Error.ReportAt(
			"SYNTAXA ENGINE",
			peeked,
			fmt.Sprintf("unexpected %v, expected valid program start", peeked.Token),
		)
	}

	// 2. Report Lexer Errors
	if lexErr := ctx.lastLexingError(); lexErr != nil {
		ctx.Error.reportLexerError(lexErr.StartLine, lexErr.StartColumn, fmt.Sprintf("lexing error: %s", lexErr.Error()))
	}

	// 3. Finalize AST
	editor.setRoot(programResult.Node)
	if editor.root != nil {
		editor.ComputeSpans()
	}

	// 4. Check for EOF ONLY if the program parse succeeded
	if programResult.Succeeded {
		peeked := ctx.Token.Peek(0)
		if peeked.Token != parser.eofToken {
			ctx.Error.ReportAt(
				"SYNTAXA ENGINE",
				peeked,
				fmt.Sprintf("unexpected %v, expected end of file", peeked.Token),
			)
			programResult.Succeeded = false
		}
	}

	// 5. Handle Final Failure State
	ctx.Error.sink.FlushFramesCommitAll()

	if !programResult.Succeeded || len(ctx.Error.sink.Errors) > 0 {
		return programResult.Node, ctx.trace, fmt.Errorf("parsing failed with %d errors", len(ctx.Error.sink.Errors))
	}

	return programResult.Node, ctx.trace, nil
}

func syntaxaParserExecuteRule[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TNodeKind,
	TLexerState comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	ctx *ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	rule ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	mode RuleExecutionMode,
) RuleResult[TObservation, TToken, TTokenRole, TNodeKind] {
	startSnap := ctx.save()
	startPos := startSnap.tokenIndex
	startLSTNodeCreationIdx := len(ctx.Editor.created)

	lexemePreRule := ctx.Token.Peek(0)
	lexemePreRuleRaw := ctx.Token.PeekRaw(0)

	if mode == ExecutionNormal {
		ctx.Recovery.pushRecovery(rule.IsRecoveryBarrier(), rule.recoveryTokens...)
		defer ctx.Recovery.popRecovery()
	}

	ctx.Error.sink.pushFrame()
	ruleResult := rule.executionFn(ctx)

	var recoveryAttempted, recovered, landedOnOurs bool
	var recoveryTokenSet []TToken

	if !ruleResult.Succeeded {
		ctx.Editor.created = ctx.Editor.created[:startLSTNodeCreationIdx]
		ruleResult, recoveryAttempted, recovered, landedOnOurs, recoveryTokenSet = handleFailureState(ctx, parser, rule, ruleResult, startSnap, mode, lexemePreRule)
	} else {
		ctx.Error.sink.popFrame(true)
		if err := validateRuleSuccess(parser, rule, ruleResult, startPos, ctx.save().tokenIndex, lexemePreRule); err != nil {
			panic(err)
		}
		processPostRuleHooks(parser, ctx, rule, startLSTNodeCreationIdx)
	}

	if ctx.trace != nil {
		ctx.trace.Events = append(ctx.trace.Events, ParseTraceEvent[TToken]{
			Cursor:            startPos,
			RawToken:          lexemePreRuleRaw.Token,
			LogicalToken:      lexemePreRule.Token,
			RuleSucceeded:     ruleResult.Succeeded,
			Consumed:          ctx.save().tokenIndex != startPos,
			NodeReturned:      ruleResult.Node != nil,
			RuleName:          rule.identity.RuleName,
			RecoveryAttempted: recoveryAttempted,
			Recovered:         recovered,
			LandedOnOurs:      landedOnOurs,
			RecoveryTokenSet:  recoveryTokenSet,
		})
	}

	return ruleResult
}

func handleFailureState[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TNodeKind,
	TLexerState comparable,
](
	ctx *ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	rule ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	result RuleResult[TObservation, TToken, TTokenRole, TNodeKind],
	startSnap ParserSnapshot[TObservation, TLexerState],
	mode RuleExecutionMode,
	lexemePreRule lexarch.Lexeme[TObservation, TToken, TTokenRole],
) (RuleResult[TObservation, TToken, TTokenRole, TNodeKind], bool, bool, bool, []TToken) {

	if mode != ExecutionNormal || result.Kind != FailureError {
		ctx.restore(startSnap)
		ctx.Error.sink.popFrame(false)
		return result, false, false, false, nil
	}

	recoveryAttempted := true
	isProgramRule := rule.GetGrammarLabel() == parser.programRule.GetGrammarLabel()

	if !isProgramRule && !ctx.Recovery.IsRecoveryToken(lexemePreRule.Token) {
		wouldBeEmpty := ctx.Error.sink.currentFrameWouldBeEmptyOnPop()
		bestPos, ok := ctx.Error.sink.currentBestPosition()
		if wouldBeEmpty || (ok && bestPos == startSnap.tokenIndex) {
			ctx.Error.replaceBestErrorAt(
				string(rule.GetName()),
				lexemePreRule,
				fmt.Sprintf("unexpected %v, expected '%s'", lexemePreRule.Token, rule.identity.ExpectedLabel),
			)
		}
	}

	ctx.Error.sink.popFrame(true)
	ctx.Error.sink.pushFrame()

	recovered, landedOnOurs, recoveryTokenSet := performRecovery(ctx, parser, parser.eofToken, rule)
	ctx.Error.sink.popFrame(false)

	if recovered {
		result.ConsumeSyncToken = landedOnOurs && !rule.IsNoConsumeRecoveryToken(ctx.Token.PeekRaw(0).Token)
		if result.ConsumeSyncToken {
			ctx.Token.ConsumeRaw()
		}

		if landedOnOurs && rule.IsRecoveryBarrier() {
			errorNode := ctx.Editor.NewNode(parser.errorNodeKind)

			result.Succeeded = true
			result.Node = errorNode
		}
	}

	return result, recoveryAttempted, recovered, landedOnOurs, recoveryTokenSet
}

func processPostRuleHooks[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TNodeKind,
	TLexerState comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	ctx *ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	rule ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	startLSTNodeCreationIdx int,
) {
	if parser.postProcessor == nil {
		return
	}

	newNodes := ctx.Editor.created[startLSTNodeCreationIdx:]
	for _, node := range newNodes {
		if !node.postProcessed {
			parser.postProcessor(node, ctx.Finalization, rule.identity)
			node.postProcessed = true
		}
	}
}

func performRecovery[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	ctx *ExecRuleContext[TObs, TToken, TTokenRole, TLexerState, TKind],
	parser *SyntaxaParser[TObs, TToken, TTokenRole, TKind, TLexerState],
	eof TToken,
	recoveryFromRule ParserRule[TObs, TToken, TTokenRole, TLexerState, TKind],
) (recovered bool, landedOnCurrentRule bool, recoveryTokenSet []TToken) {
	recoveryTokenSet = ctx.Recovery.CollectAllRecoveryTokens()

	for {
		cur := ctx.Token.PeekRaw(0)

		if cur.Token == eof {
			return false, false, recoveryTokenSet
		}

		// Query the engine directly. No local map allocations.
		if ctx.Recovery.IsInAllFrames(cur.Token) {
			landedOnCurrentRule = recoveryFromRule.IsSyncToken(cur.Token)
			return true, landedOnCurrentRule, recoveryTokenSet
		}

		ctx.Token.ConsumeRaw()
	}
}

func validateRuleSuccess[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole comparable,
	TLexerState,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	rule ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	result RuleResult[TObservation, TToken, TTokenRole, TNodeKind],
	startPos int,
	endPos int,
	lexemePreRule lexarch.Lexeme[TObservation, TToken, TTokenRole],
) error {

	contract := rule.contract

	if rule.grammar != nil && rule.grammar.Kind == GReference {
		if targetRule, exists := parser.registry[rule.grammar.ReferenceTarget]; exists {
			contract = targetRule.contract
		}
		// If target is not in the registry (e.g. Pratt level grammar-only refs), use this rule's contract.
	}

	if endPos == startPos && contract.MustConsume && lexemePreRule.Token != parser.eofToken {
		return fmt.Errorf(
			"parser invariant violated: non-optional rule succeeded without consuming input at cursor %d (token=%v) [%d:%d] | rule = '%s'",
			startPos,
			lexemePreRule.Token,
			lexemePreRule.StartLine,
			lexemePreRule.StartColumn,
			rule.identity.RuleName,
		)
	}

	if result.Node == nil && contract.MustReturnNode {
		return fmt.Errorf(
			"parser invariant violated: rule returned nil node without explicit skip at cursor %d (token=%v) [%d:%d] | rule = '%s'",
			startPos,
			lexemePreRule.Token,
			lexemePreRule.StartLine,
			lexemePreRule.StartColumn,
			rule.identity.RuleName,
		)
	}

	return nil
}

func bindMergedRecoveryIntoRegistry[
	TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable,
](
	registry RuleRegistry[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	mergedByLabel map[GrammarLabel]RecoverySpec[TToken],
) RuleRegistry[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	if len(registry) == 0 {
		return registry
	}
	out := make(RuleRegistry[TObservation, TToken, TTokenRole, TLexerState, TNodeKind], len(registry))
	for label, rule := range registry {
		out[label] = bindMergedRecoveryToRule(rule, mergedByLabel)
	}
	return out
}

func bindMergedRecoveryToRule[
	TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable,
](
	rule ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	mergedByLabel map[GrammarLabel]RecoverySpec[TToken],
) ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	spec, exists := mergedByLabel[rule.GetGrammarLabel()]
	if !exists {
		return rule
	}
	return ParserRuleApplyRecoverySpec(rule, spec)
}
