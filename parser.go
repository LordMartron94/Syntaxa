package syntaxa

import (
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
type NodePostProcessor[TNodeKind comparable] func(
	node *SyntaxaLSTNode[TNodeKind],
	finalizationCTX *FinalizationCtx[TNodeKind],
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
type ParserSnapshot struct {
	tokenIndex int

	lexerSnap lexarch.LexingSnapshot

	nextVisibleRawIndex int
	nextVisibleCached   bool
}

func (p *ParserSnapshot) Index() int {
	return p.tokenIndex
}

// =============================================================
// PARSER
// =============================================================

type ParseTraceEvent struct {
	Cursor        int
	RawToken      lexarch.TokenKind
	LogicalToken  lexarch.TokenKind
	RuleSucceeded bool
	Consumed      bool
	NodeReturned  bool
	RuleName      RuleLabel

	RecoveryAttempted bool
	Recovered         bool
	LandedOnOurs      bool
	RecoveryTokenSet  []lexarch.TokenKind
}

type ParseTrace struct {
	Events []ParseTraceEvent
}

type ParseResult[TKind comparable] struct {
	Root   *SyntaxaLSTNode[TKind]
	Errors *SyntaxErrors
	Trace  *ParseTrace
}

/*
RuleRegistry maps grammar labels to executable parser rules.

It serves as the late-binding lookup table for GReference nodes.
Only named, context-boundary rules need to be registered here.
*/
type RuleRegistry[TNodeKind comparable] map[GrammarLabel]ParserRule[TNodeKind]

/*
SyntaxaParser is a generic parsing engine using top-down recursive parsing.

It is highly flexible but not paradigm-agnostic: rule factories can provide PEG-, LL(k)-,
or Pratt-style behaviour; LR and other bottom-up paradigms are not supported.

Responsibilities:
  - transactional rule execution
  - centralized error recovery
  - LST assembly
*/
type SyntaxaParser[TNodeKind comparable] struct {
	grammarPackage *GrammarPackage[TNodeKind]
	registry       RuleRegistry[TNodeKind]

	programRule ParserRule[TNodeKind]

	postProcessor NodePostProcessor[TNodeKind]

	defaultSkipRoles []lexarch.TokenRole
	nodePoolPrefill  int
	nodePoolGrow     func(currentCap, needed int) int

	tokenFormatter func(token lexarch.TokenKind) string

	eofToken lexarch.TokenKind

	rootNodeKind  TNodeKind
	errorNodeKind TNodeKind

	freezeAfterParse bool
	debugTrace       bool

	// getAnalysis returns nullable/first/follow when set; used by rule context. Optional.
	getAnalysis func() *GrammarAnalysis
}

/*
SyntaxaParserCreate constructs a new parser instance from a grammar package.

The package must have been produced with an entry rule (ProducePackage(..., &programRule));
panics if grammarPackage.EntryRuleParserRule is nil.
nodePostProcessor is optional and allowed to be nil.
getAnalysis is optional; when set, the rule context can use it for nullable/first/follow
(e.g. for Predict or Pratt). Pass lowering.GetAnalysis(grammarPackage) or nil.
*/
func SyntaxaParserCreate[TNodeKind comparable](
	grammarPackage *GrammarPackage[TNodeKind],
	registry RuleRegistry[TNodeKind],
	tokenFormatter func(token lexarch.TokenKind) string,
	nodePostProcessor NodePostProcessor[TNodeKind],
	eofToken lexarch.TokenKind,
	rootNodeKind, errorNodeKind TNodeKind,
	freezeAfterParse bool,
	getAnalysis func() *GrammarAnalysis,
) *SyntaxaParser[TNodeKind] {
	if grammarPackage == nil || grammarPackage.EntryRuleParserRule == nil {
		panic("SyntaxaParserCreate: grammar package must have EntryRuleParserRule set (produce package with entry rule)")
	}
	boundRegistry := bindMergedRecoveryIntoRegistry(registry, grammarPackage.MergedRecoveryByGrammarLabel)
	programRule := bindMergedRecoveryToRule(*grammarPackage.EntryRuleParserRule, grammarPackage.MergedRecoveryByGrammarLabel)
	return &SyntaxaParser[TNodeKind]{
		grammarPackage:   grammarPackage,
		registry:         boundRegistry,
		programRule:      programRule,
		tokenFormatter:   tokenFormatter,
		postProcessor:    nodePostProcessor,
		eofToken:         eofToken,
		rootNodeKind:     rootNodeKind,
		errorNodeKind:    errorNodeKind,
		freezeAfterParse: freezeAfterParse,
		defaultSkipRoles: nil,
		getAnalysis:      getAnalysis,
	}
}

func (p *SyntaxaParser[TNodeKind]) SetDefaultSkips(roles ...lexarch.TokenRole) {
	p.defaultSkipRoles = append([]lexarch.TokenRole(nil), roles...)
}

func (p *SyntaxaParser[TNodeKind]) GetDefaultSkips() []lexarch.TokenRole {
	return p.defaultSkipRoles
}

func (p *SyntaxaParser[TNodeKind]) SetNodePoolPrefill(hint int) {
	if hint < 0 {
		panic("SetNodePoolPrefill: hint must be >= 0")
	}
	p.nodePoolPrefill = hint
}

func (p *SyntaxaParser[TNodeKind]) GetNodePoolPrefill() int {
	return p.nodePoolPrefill
}

/*
SetNodePoolGrowFn sets the LSTEditor free-list growth policy (nil = default).

growFn receives currentCap = cap(nodeFree) before growth and needed = len(nodeFree)+1
(minimum length after growth). It returns the target capacity for the free list after
this grow (same idea as memforge.GrowthStrategy); the editor clamps to at least needed
and allocates that many new nodes when the pool was empty.
*/
func (p *SyntaxaParser[TNodeKind]) SetNodePoolGrowFn(growFn func(currentCap, needed int) int) {
	p.nodePoolGrow = growFn
}

func (p *SyntaxaParser[TNodeKind]) EnableTrace(enable bool) {
	p.debugTrace = enable
}

func (p *SyntaxaParser[TNodeKind]) TraceEnabled() bool {
	return p.debugTrace
}

/*
SyntaxaParserParseWithContext drives parsing using a fully
configured RuleContext.

This is the advanced entry point for custom lexer integrations,
incremental systems, and complex pipelines.

The caller is responsible for constructing a valid RuleContext
and providing an initialized root node.

Typical use cases:
  - parsing directly from lexer sessions
  - multi-stage lexing pipelines
  - editor-driven incremental parsing
  - custom recovery strategies

This function guarantees:
  - transactional rule execution
  - centralized error recovery
  - consistent LST assembly
*/
func SyntaxaParserParseWithContext[TNodeKind comparable](
	parser *SyntaxaParser[TNodeKind],
	ctx *ExecRuleContext[TNodeKind],
) (*SyntaxaLSTNode[TNodeKind], *ParseTrace, error) {
	return parseWithContext(parser, ctx)
}

// -------------------------------------------------------- PRIVATE HELPERS

func parseWithContext[TNodeKind comparable](
	parser *SyntaxaParser[TNodeKind],
	ctx *ExecRuleContext[TNodeKind],
) (*SyntaxaLSTNode[TNodeKind], *ParseTrace, error) {
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
		ctx.Error.reportLexerError(0, 0, fmt.Sprintf("lexing error: %s", lexErr.Error()))
	}

	// 3. Finalize AST
	editor.setRoot(programResult.Node)
	if editor.root != nil {
		editor.ComputeSpans()
	}

	// 4. Check for EOF ONLY if the program parse succeeded
	if programResult.Succeeded {
		peeked := ctx.Token.Peek(0)
		if peeked.Token != parser.eofToken && !ctx.Error.HasCommittedSyntaxErrors() {
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

func syntaxaParserExecuteRule[TNodeKind comparable](
	parser *SyntaxaParser[TNodeKind],
	ctx *ExecRuleContext[TNodeKind],
	rule ParserRule[TNodeKind],
	mode RuleExecutionMode,
) RuleResult[TNodeKind] {
	if st := ctx.EngineStats; st != nil {
		st.RuleExecuteCalls++
		if mode == ExecutionProbe {
			st.ProbeInvocations++
		}
	}

	startSnap := ctx.save()
	startPos := startSnap.tokenIndex
	startLSTNodeCreationIdx := len(ctx.Editor.created)

	lexemePreRule := ctx.Token.Peek(0)
	lexemePreRuleRaw := ctx.Token.PeekRaw(0)

	if mode == ExecutionNormal {
		ctx.Recovery.pushRecovery(
			rule.IsRecoveryBarrier(),
			append(rule.recoveryTokens, rule.noConsumeOnRecoveryTokens...)...,
		)
		defer ctx.Recovery.popRecovery()
	}

	isProgramRule := rule.GetGrammarLabel() == parser.programRule.GetGrammarLabel()
	if mode == ExecutionNormal && isProgramRule {
		ctx.Error.BeginEntryRuleScope()
		defer ctx.Error.EndEntryRuleScope()
	}

	ctx.Error.sink.pushFrame()
	ruleResult := rule.executionFn(ctx)

	var recoveryAttempted, recovered, landedOnOurs bool
	var recoveryTokenSet []lexarch.TokenKind

	if !ruleResult.Succeeded {
		ctx.Editor.TruncateCreated(startLSTNodeCreationIdx)
		ruleResult, recoveryAttempted, recovered, landedOnOurs, recoveryTokenSet = handleFailureState(ctx, parser, rule, ruleResult, startSnap, mode, lexemePreRule)
	} else {
		ctx.Error.sink.popFrame(true)
		if err := validateRuleSuccess(parser, rule, ruleResult, startPos, ctx.save().tokenIndex, lexemePreRule); err != nil {
			panic(err)
		}
		processPostRuleHooks(parser, ctx, rule, startLSTNodeCreationIdx)
	}

	if ctx.trace != nil {
		ctx.trace.Events = append(ctx.trace.Events, ParseTraceEvent{
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

	if st := ctx.EngineStats; st != nil {
		if ruleResult.Succeeded {
			st.RuleExecuteSucceeded++
		} else {
			st.RuleExecuteFailed++
		}
		if mode == ExecutionProbe && !ruleResult.Succeeded {
			st.ProbeFailures++
		}
		if recoveryAttempted {
			st.RecoveryAttempts++
		}
		if recovered {
			st.RecoveriesSucceeded++
		}
	}

	return ruleResult
}

func handleFailureState[TNodeKind comparable](
	ctx *ExecRuleContext[TNodeKind],
	parser *SyntaxaParser[TNodeKind],
	rule ParserRule[TNodeKind],
	result RuleResult[TNodeKind],
	startSnap ParserSnapshot,
	mode RuleExecutionMode,
	lexemePreRule Lexeme,
) (RuleResult[TNodeKind], bool, bool, bool, []lexarch.TokenKind) {

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
			if st := ctx.EngineStats; st != nil {
				st.RecoverySyncRawConsumes++
			}
		}

		if landedOnOurs && rule.IsRecoveryBarrier() {
			errorNode := ctx.Editor.NewNode(parser.errorNodeKind)

			result.Succeeded = true
			result.Node = errorNode
		}
	}

	if !result.Succeeded {
		ctx.restore(startSnap)
	}

	return result, recoveryAttempted, recovered, landedOnOurs, recoveryTokenSet
}

func processPostRuleHooks[TNodeKind comparable](
	parser *SyntaxaParser[TNodeKind],
	ctx *ExecRuleContext[TNodeKind],
	rule ParserRule[TNodeKind],
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

func performRecovery[TNodeKind comparable](
	ctx *ExecRuleContext[TNodeKind],
	parser *SyntaxaParser[TNodeKind],
	eof lexarch.TokenKind,
	recoveryFromRule ParserRule[TNodeKind],
) (recovered bool, landedOnCurrentRule bool, recoveryTokenSet []lexarch.TokenKind) {
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
		if st := ctx.EngineStats; st != nil {
			st.RecoveryDiscardRawConsumes++
		}
	}
}

func validateRuleSuccess[TNodeKind comparable](
	parser *SyntaxaParser[TNodeKind],
	rule ParserRule[TNodeKind],
	result RuleResult[TNodeKind],
	startPos int,
	endPos int,
	lexemePreRule Lexeme,
) error {

	contract := rule.contract

	if rule.grammar != nil && rule.grammar.Kind == GReference {
		if targetRule, exists := parser.registry[rule.grammar.ReferenceTarget]; exists {
			contract = targetRule.contract
		}
		// If target is not in the registry (e.g. Pratt level grammar-only refs), use this rule's contract.
	}

	if endPos == startPos && contract.MustConsume && lexemePreRule.Token != parser.eofToken {
		lexemeLine, lexemeColumn := LexemeStartLineColumn(lexemePreRule)
		return fmt.Errorf(
			"parser invariant violated: non-optional rule succeeded without consuming input at cursor %d (token=%v) [%d:%d] | rule = '%s'",
			startPos,
			lexemePreRule.Token,
			lexemeLine,
			lexemeColumn,
			rule.identity.RuleName,
		)
	}

	if result.Node == nil && contract.MustReturnNode {
		lexemeLine, lexemeColumn := LexemeStartLineColumn(lexemePreRule)
		return fmt.Errorf(
			"parser invariant violated: rule returned nil node without explicit skip at cursor %d (token=%v) [%d:%d] | rule = '%s'",
			startPos,
			lexemePreRule.Token,
			lexemeLine,
			lexemeColumn,
			rule.identity.RuleName,
		)
	}

	return nil
}

func bindMergedRecoveryIntoRegistry[TNodeKind comparable](
	registry RuleRegistry[TNodeKind],
	mergedByLabel map[GrammarLabel]RecoverySpec,
) RuleRegistry[TNodeKind] {
	if len(registry) == 0 {
		return registry
	}
	out := make(RuleRegistry[TNodeKind], len(registry))
	for label, rule := range registry {
		out[label] = bindMergedRecoveryToRule(rule, mergedByLabel)
	}
	return out
}

func bindMergedRecoveryToRule[TNodeKind comparable](
	rule ParserRule[TNodeKind],
	mergedByLabel map[GrammarLabel]RecoverySpec,
) ParserRule[TNodeKind] {
	spec, exists := mergedByLabel[rule.GetGrammarLabel()]
	if !exists {
		return rule
	}
	return ParserRuleApplyRecoverySpec(rule, spec)
}
