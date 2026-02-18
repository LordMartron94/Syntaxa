package syntaxa

import (
	"cmp"
	"fmt"
	"lexarch"
)

// =============================================================
// PARSING CURSOR
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
	RuleSelected  bool
	RuleSucceeded bool
	Consumed      bool
	TopLevel      bool
	NodeReturned  bool
	RootRevision  uint64
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
	selectRule RuleSelector[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]

	defaultSkipRoles []TTokenRole

	tokenFormatter       func(token TToken) string
	observationFormatter lexarch.ObservationFormatter[TObservation]

	eofToken TToken

	rootNodeKind  TNodeKind
	errorNodeKind TNodeKind

	freezeAfterParse bool
	debugTrace       bool
	lastTrace        *ParseTrace[TToken]
}

/*
SyntaxaParserCreate constructs a new parser instance.
*/
func SyntaxaParserCreate[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable](
	selectRule RuleSelector[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	tokenFormatter func(token TToken) string,
	observationFormatter lexarch.ObservationFormatter[TObservation],
	eofToken TToken,
	rootNodeKind, errorNodeKind TNodeKind,
	freezeAfterParse bool,
) *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState] {
	return &SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]{
		selectRule:           selectRule,
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

func (p *SyntaxaParser[_, TToken, _, _, _]) LastTrace() *ParseTrace[TToken] {
	if p.lastTrace == nil {
		return nil
	}
	return p.lastTrace
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
	ctx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	root *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
) (*ParseTrace[TToken], error) {
	return parseWithContext(parser, ctx, root)
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
	ctx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	root *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
) (*ParseTrace[TToken], error) {

	editor := ctx.Editor
	if parser.freezeAfterParse {
		defer editor.Freeze()
	}

	var trace *ParseTrace[TToken]
	if parser.debugTrace {
		trace = &ParseTrace[TToken]{}
	}

	var lastCursor int
	var sawNonEOFRaw bool

	for {

		raw := ctx.PeekRaw(0)
		current := ctx.Peek(0)

		if raw.Token != parser.eofToken {
			sawNonEOFRaw = true
		}

		// ───── Phase 1: lexer failure ─────

		if lexErr := ctx.lastLexingError(); lexErr != nil {
			ctx.Error.reportLexerError(
				lexErr.StartLine,
				lexErr.StartColumn,
				lexErr.Error(),
			)
			break
		}

		// ───── Phase 2: EOF ─────

		if current.Token == parser.eofToken {
			break
		}

		startSnap := ctx.Transaction.save()
		startPos := startSnap.tokenIndex

		rule := parser.selectRule(ctx.selectCTX())

		event := ParseTraceEvent[TToken]{
			Cursor:       startPos,
			RawToken:     raw.Token,
			LogicalToken: current.Token,
			RuleSelected: rule != nil,
			RootRevision: root.revision,
		}

		// ───── Phase 3: no rule → error + recovery ─────

		if rule == nil {

			ctx.Error.Report(
				current.StartLine,
				current.StartColumn,
				"unexpected token",
			)

			errNode := ctx.createErrorNode("unexpected token")
			errNode.tokens = append(errNode.tokens, current)
			editor.AttachChild(root, errNode)

			if trace != nil {
				trace.Events = append(trace.Events, event)
			}

			if recoverWithContext(ctx, current, parser.eofToken) {
				if startPos == lastCursor {
					ctx.Token.Consume()
				}
				lastCursor = startPos
				continue
			}

			break
		}

		// ───── Phase 4: execute rule ─────

		result, ok := rule(ctx)
		endPos := ctx.Transaction.save().tokenIndex

		if trace != nil {
			event.RuleSucceeded = ok
			event.Consumed = endPos != startPos
			event.TopLevel = result.TopLevel
			event.NodeReturned = result.Node != nil
			trace.Events = append(trace.Events, event)
		}

		if !ok {
			ctx.Transaction.restore(startSnap)

			if recoverWithContext(ctx, current, parser.eofToken) {
				if startPos == lastCursor {
					ctx.Token.Consume()
				}
				lastCursor = startPos
				continue
			}

			break
		}

		// ───── Phase 5: invariants ─────

		if err := validateRuleSuccess[TObservation, TToken, TTokenRole, TLexerState](
			result,
			startPos,
			endPos,
			current,
		); err != nil {
			return trace, err
		}

		if result.TopLevel {
			editor.AttachChild(root, result.Node)
		}

		lastCursor = -1
	}

	if err := validateFinalAST(root, ctx.Error.sink, sawNonEOFRaw); err != nil {
		return trace, err
	}

	return trace, nil
}

func recoverWithContext[
	TObs cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TKind comparable,
](
	ctx ExecRuleContext[TObs, TToken, TTokenRole, TLexerState, TKind],
	current lexarch.Lexeme[TObs, TToken, TTokenRole],
	eof TToken,
) bool {
	sync := ctx.Recovery.currentRecovery()

	if len(sync) == 0 {
		ctx.Token.Consume()
		return current.Token != eof
	}

	for current.Token != eof {
		if _, ok := sync[current.Token]; ok {
			return true
		}
		ctx.Token.Consume()
		current = ctx.PeekRaw(0)
	}

	return false
}

func validateRuleSuccess[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole comparable,
	TLexerState,
	TNodeKind comparable,
](
	result RuleResult[TObservation, TToken, TTokenRole, TNodeKind],
	startPos int,
	endPos int,
	current lexarch.Lexeme[TObservation, TToken, TTokenRole],
) error {

	if endPos == startPos {
		return fmt.Errorf(
			"parser invariant violated: rule succeeded without consuming input at cursor %d (token=%v)",
			startPos,
			current.Token,
		)
	}

	if result.TopLevel && result.Node == nil {
		return fmt.Errorf(
			"parser invariant violated: TopLevel rule returned nil node at cursor %d",
			startPos,
		)
	}

	return nil
}

func validateFinalAST[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole comparable,
	TNodeKind comparable,
](
	root *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
	errors *SyntaxErrors[TObservation],
	sawNonEOFRaw bool,
) error {

	if sawNonEOFRaw && len(root.children) == 0 && !errors.HasErrors() {
		return fmt.Errorf(
			"parser produced empty AST despite non-empty input",
		)
	}
	return nil
}
