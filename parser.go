package syntaxa

import (
	"cmp"
	"fmt"
	"lexarch"
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
	RuleName      string
}

type ParseTrace[TToken any] struct {
	Events []ParseTraceEvent[TToken]
}

type ParseResult[TObs cmp.Ordered, TToken, TTokenRole, TKind comparable] struct {
	Root   *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]
	Errors *SyntaxErrors[TObs]
	Trace  *ParseTrace[TToken]
}

/*
SyntaxaParser is a grammar-agnostic parsing engine.

It imposes no parsing paradigm (LL, LR, Pratt, PEG, etc.).

Responsibilities:
  - transactional rule execution
  - centralized error recovery
  - AST assembly
*/
type SyntaxaParser[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable] struct {
	programRule ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]

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
*/
func SyntaxaParserCreate[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable](
	programRule ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	tokenFormatter func(token TToken) string,
	observationFormatter lexarch.ObservationFormatter[TObservation],
	eofToken TToken,
	rootNodeKind, errorNodeKind TNodeKind,
	freezeAfterParse bool,
) *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState] {
	return &SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]{
		programRule:          programRule,
		tokenFormatter:       tokenFormatter,
		observationFormatter: observationFormatter,
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
  - consistent AST assembly
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
) (*SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind], *ParseTrace[TToken], error) {
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
) (*SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind], *ParseTrace[TToken], error) {
	editor := ctx.Editor

	if parser.freezeAfterParse {
		defer editor.Freeze()
	}

	editor.begin()
	defer editor.end()

	programResult := syntaxaParserExecuteRule(parser, ctx, parser.programRule, ExecutionNormal)

	if lexErr := ctx.lastLexingError(); lexErr != nil {
		ctx.Error.reportLexerError(lexErr.StartLine, lexErr.StartColumn, fmt.Sprintf("lexing error: %s", lexErr.Error()))
	}

	editor.setRoot(programResult.Node)
	editor.ComputeSpans()

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

	lexemePreRule := ctx.Token.Peek(0)
	lexemePreRuleRaw := ctx.Token.PeekRaw(0)

	if mode == ExecutionNormal {
		ctx.Recovery.pushRecovery(rule.recoveryTokens...)
		defer ctx.Recovery.popRecovery()
	}

	ctx.Error.sink.pushFrame()

	ruleResult := rule.executionFn(ctx)

	endPos := ctx.save().tokenIndex
	success := ruleResult.Succeeded

	ctx.trace.Events = append(ctx.trace.Events, ParseTraceEvent[TToken]{
		Cursor:        startPos,
		RawToken:      lexemePreRuleRaw.Token,
		LogicalToken:  lexemePreRule.Token,
		RuleSucceeded: success,
		Consumed:      endPos != startPos,
		NodeReturned:  ruleResult.Node != nil,
		RuleName:      rule.name,
	})

	if !success {
		ctx.restore(startSnap)

		if mode == ExecutionNormal {
			if bestPos, ok := ctx.Error.sink.currentBestPosition(); ok && bestPos == startPos {
				ctx.Error.replaceBestErrorAt(
					lexemePreRule,
					fmt.Sprintf(
						"unexpected %v, expected %s",
						lexemePreRule.Token,
						rule.expectedLabel,
					),
				)
			}

			ctx.Error.sink.popFrame(true)

			recovered := performRecovery(ctx, parser.eofToken)
			if recovered {
				ctx.Token.ConsumeRaw()
			}
		} else {
			ctx.Error.sink.popFrame(false)
		}

		return ruleResult
	}

	ctx.Error.sink.popFrame(true)

	if err := validateRuleSuccess(rule, ruleResult, startPos, endPos, lexemePreRule); err != nil {
		panic(err)
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
) bool {
	syncSet := ctx.Recovery.currentRecovery()

	// fmt.Printf("recovering with set: %v\n", syncSet)

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
	rule ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	result RuleResult[TObservation, TToken, TTokenRole, TNodeKind],
	startPos int,
	endPos int,
	lexemePreRule lexarch.Lexeme[TObservation, TToken, TTokenRole],
) error {
	contract := rule.contract

	if endPos == startPos && contract.MustConsume {
		return fmt.Errorf(
			"parser invariant violated: non-optional rule succeeded without consuming input at cursor %d (token=%v) [%d:%d] | rule = '%s'",
			startPos,
			lexemePreRule.Token,
			lexemePreRule.StartLine,
			lexemePreRule.StartColumn,
			rule.name,
		)
	}

	if result.Node == nil && contract.MustReturnNode {
		return fmt.Errorf(
			"parser invariant violated: rule returned nil node without explicit skip at cursor %d (token=%v) [%d:%d] | rule = '%s'",
			startPos,
			lexemePreRule.Token,
			lexemePreRule.StartLine,
			lexemePreRule.StartColumn,
			rule.name,
		)
	}

	return nil
}
