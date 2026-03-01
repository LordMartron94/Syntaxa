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
SyntaxaParser is a grammar-agnostic parsing engine.

It imposes no parsing paradigm (LL, LR, Pratt, PEG, etc.).

Responsibilities:
  - transactional rule execution
  - centralized error recovery
  - LST assembly
*/
type SyntaxaParser[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable] struct {
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
}

/*
SyntaxaParserCreate constructs a new parser instance.

nodePostProcessor is optional and allowed to be nil.
*/
func SyntaxaParserCreate[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable](
	programRule ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	tokenFormatter func(token TToken) string,
	observationFormatter lexarch.ObservationFormatter[TObservation],
	nodePostProcessor NodePostProcessor[TObservation, TToken, TTokenRole, TNodeKind],
	eofToken TToken,
	rootNodeKind, errorNodeKind TNodeKind,
	freezeAfterParse bool,
) *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState] {
	return &SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]{
		programRule:          programRule,
		tokenFormatter:       tokenFormatter,
		observationFormatter: observationFormatter,
		postProcessor:        nodePostProcessor,
		eofToken:             eofToken,
		rootNodeKind:         rootNodeKind,
		errorNodeKind:        errorNodeKind,
		freezeAfterParse:     freezeAfterParse,
		defaultSkipRoles:     nil,
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

	editor.begin() // No defer end here because end resets the editor.

	programResult := syntaxaParserExecuteRule(parser, ctx, parser.programRule, ExecutionNormal)

	if lexErr := ctx.lastLexingError(); lexErr != nil {
		ctx.Error.reportLexerError(lexErr.StartLine, lexErr.StartColumn, fmt.Sprintf("lexing error: %s", lexErr.Error()))
	}

	editor.setRoot(programResult.Node)
	editor.ComputeSpans()

	peeked := ctx.Token.Peek(0)
	if peeked.Token != parser.eofToken {
		ctx.Error.ReportAt(
			"SYNTAXA ENGINE",
			peeked,
			fmt.Sprintf("unexpected %v, expected end of file", peeked.Token),
		)
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

	// fmt.Printf("starting exec rule: %s\n", rule.name)
	// defer fmt.Printf("end exec rule: %s\n", rule.name)

	// Recovery scope for this construct
	if mode == ExecutionNormal {
		ctx.Recovery.pushRecovery(rule.recoveryTokens...)
		defer ctx.Recovery.popRecovery()
	}

	// === Error frame for THIS construct ===
	ctx.Error.sink.pushFrame()

	ruleResult := rule.executionFn(ctx)

	endPos := ctx.save().tokenIndex
	success := ruleResult.Succeeded

	if ctx.trace != nil {
		ctx.trace.Events = append(ctx.trace.Events, ParseTraceEvent[TToken]{
			Cursor:        startPos,
			RawToken:      lexemePreRuleRaw.Token,
			LogicalToken:  lexemePreRule.Token,
			RuleSucceeded: success,
			Consumed:      endPos != startPos,
			NodeReturned:  ruleResult.Node != nil,
			RuleName:      rule.identity.RuleName,
		})
	}

	// ============================================================
	// FAILURE PATH
	// ============================================================
	if !success {
		ctx.restore(startSnap)
		ctx.Editor.created = ctx.Editor.created[:startLSTNodeCreationIdx]

		if mode == ExecutionNormal {
			if ruleResult.Kind == FailureError {
				if bestPos, ok := ctx.Error.sink.currentBestPosition(); ok && bestPos == startPos {
					ctx.Error.replaceBestErrorAt(
						string(rule.GetName()),
						lexemePreRule,
						fmt.Sprintf(
							"unexpected %v, expected %s",
							lexemePreRule.Token,
							rule.identity.ExpectedLabel,
						),
					)
				}

				ctx.Error.sink.popFrame(true)

				// ------------------------------------------------
				// Recovery should NOT produce competing errors
				// ------------------------------------------------
				ctx.Error.sink.pushFrame()
				recovered := performRecovery(ctx, parser.eofToken, rule)
				ctx.Error.sink.popFrame(false)

				if recovered {
					ctx.Token.ConsumeRaw()
				}

			} else {
				ctx.Error.sink.popFrame(false)
			}

		} else {
			ctx.Error.sink.popFrame(false)
		}

		return ruleResult
	}

	// ============================================================
	// SUCCESS PATH
	// ============================================================

	ctx.Error.sink.popFrame(true)

	if err := validateRuleSuccess(
		parser,
		rule,
		ruleResult,
		startPos,
		endPos,
		lexemePreRule,
	); err != nil {
		panic(err)
	}

	newNodes := ctx.Editor.created[startLSTNodeCreationIdx:]

	if parser.postProcessor != nil {
		for _, node := range newNodes {
			if !node.postProcessed {
				parser.postProcessor(node, ctx.Finalization, rule.identity)
				node.postProcessed = true
			}
		}
	}

	return ruleResult
}

func performRecovery[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	ctx *ExecRuleContext[TObs, TToken, TTokenRole, TLexerState, TKind],
	eof TToken,
	recoveryFromRule ParserRule[TObs, TToken, TTokenRole, TLexerState, TKind],
) bool {
	syncSet := ctx.Recovery.currentRecovery()

	// fmt.Printf("recovering from rule %s with set: %v\n", recoveryFromRule.GetName(), syncSet)

	for {
		cur := ctx.Token.PeekRaw(0)

		if cur.Token == eof {
			return false
		}

		if _, ok := syncSet[cur.Token]; ok {
			return true
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
