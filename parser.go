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
type ParserSnapshot struct {
	tokenIndex int
	aux        any
}

func (p *ParserSnapshot) Index() int {
	return p.tokenIndex
}

// =============================================================
// RULES
// =============================================================

/* RuleResult encapsulates the return value of a parser rule. */
type RuleResult[TObs cmp.Ordered, TToken, TTokenRole, TKind comparable] struct {
	Node     *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]
	TopLevel bool
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

func recoverWithContext[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable](
	ctx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	current lexarch.Lexeme[TObservation, TToken, TTokenRole],
	eofToken TToken,
) bool {

	sync := ctx.currentRecovery()
	if len(sync) == 0 {
		ctx.Consume()
		return current.Token != eofToken
	}

	for {
		if current.Token == eofToken {
			return false
		}

		_, synced := sync[current.Token]
		if synced {
			return true
		}

		ctx.Consume()
		current = ctx.PeekRaw(0)
	}
}

type parseLoopState struct {
	lastCursor   int
	sawNonEOFRaw bool
}

func parseWithContext[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	execCtx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	root *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
) (*ParseTrace[TToken], error) {

	editor := execCtx.Editor
	if parser.freezeAfterParse {
		defer editor.Freeze()
	}

	trace := initTrace[TToken](parser.debugTrace)

	state := parseLoopState{}

	for {
		step, done, err := parseIteration(parser, execCtx, root, trace, &state)
		if err != nil {
			return trace, err
		}
		if done {
			break
		}
		state.lastCursor = step
	}

	errors := execCtx.getErrors()

	if err := validateFinalAST(root, errors, state.sawNonEOFRaw); err != nil {
		return trace, err
	}

	return trace, nil
}

func initTrace[TToken any](enabled bool) *ParseTrace[TToken] {
	if !enabled {
		return nil
	}
	return &ParseTrace[TToken]{}
}

func parseIteration[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	execCtx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	root *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
	trace *ParseTrace[TToken],
	state *parseLoopState,
) (cursor int, done bool, err error) {

	rawCurrent, current := peekTokens(execCtx)

	if rawCurrent.Token != parser.eofToken {
		state.sawNonEOFRaw = true
	}

	if handled, done := handleLexingError(execCtx); handled {
		return -1, done, nil
	}

	if current.Token == parser.eofToken {
		return -1, true, nil
	}

	startSnap := execCtx.save()
	startPos := startSnap.tokenIndex

	rule := parser.selectRule(execCtx.selectCTX())

	event := createTraceEvent[TObservation, TToken, TTokenRole, TNodeKind](startPos, rawCurrent, current, rule != nil, root.revision)

	if rule == nil {
		handleNoRule(parser, execCtx, root, trace, event, current, state)
		return startPos, false, nil
	}

	result, endPos, ok := executeRule(execCtx, rule)

	updateTrace(&event, result, ok, startPos, endPos)
	appendTrace(trace, event)

	if !ok {
		execCtx.restore(startSnap)
		handleRecovery(execCtx, current, parser.eofToken, startPos, state)
		return startPos, false, nil
	}

	if err := validateRuleSuccess[TObservation, TToken, TTokenRole, TLexerState](result, startPos, endPos, current); err != nil {
		return -1, true, err
	}

	attachIfTopLevel(execCtx.Editor, root, result)

	state.lastCursor = -1
	return endPos, false, nil
}

func peekTokens[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole comparable,
	TLexerState,
	TNodeKind comparable,
](
	execCtx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) (
	raw lexarch.Lexeme[TObservation, TToken, TTokenRole],
	logical lexarch.Lexeme[TObservation, TToken, TTokenRole],
) {
	raw = execCtx.PeekRaw(0)
	logical = execCtx.Peek(0)
	return
}

func handleLexingError[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole comparable,
	TLexerState,
	TNodeKind comparable,
](
	execCtx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) (handled bool, done bool) {

	if lexErr := execCtx.LastLexingError(); lexErr != nil {
		execCtx.reportLexerError(
			lexErr.StartLine,
			lexErr.StartColumn,
			lexErr.Error(),
		)
		return true, true
	}

	return false, false
}

func executeRule[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole comparable,
	TLexerState,
	TNodeKind comparable,
](
	execCtx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	rule ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) (
	result RuleResult[TObservation, TToken, TTokenRole, TNodeKind],
	endPos int,
	ok bool,
) {
	result, ok = rule(execCtx)
	endPos = execCtx.save().tokenIndex
	return
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

func attachIfTopLevel[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole comparable,
	TNodeKind comparable,
](
	editor *ASTEditor[TObservation, TToken, TTokenRole, TNodeKind],
	root *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
	result RuleResult[TObservation, TToken, TTokenRole, TNodeKind],
) {
	if result.TopLevel {
		editor.AttachChild(root, result.Node)
	}
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

func createTraceEvent[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole comparable,
	TNodeKind comparable,
](
	cursor int,
	raw lexarch.Lexeme[TObservation, TToken, TTokenRole],
	logical lexarch.Lexeme[TObservation, TToken, TTokenRole],
	ruleSelected bool,
	rootRevision uint64,
) ParseTraceEvent[TToken] {
	return ParseTraceEvent[TToken]{
		Cursor:       cursor,
		RawToken:     raw.Token,
		LogicalToken: logical.Token,
		RuleSelected: ruleSelected,
		RootRevision: rootRevision,
	}
}

func handleNoRule[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole comparable,
	TLexerState,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	execCtx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	root *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
	trace *ParseTrace[TToken],
	event ParseTraceEvent[TToken],
	current lexarch.Lexeme[TObservation, TToken, TTokenRole],
	state *parseLoopState,
) {

	execCtx.Report(
		current.StartLine,
		current.StartColumn,
		"unexpected token",
	)

	errNode := execCtx.CreateErrorNode("unexpected token")
	errNode.tokens = append(errNode.tokens, current)
	execCtx.Editor.AttachChild(root, errNode)

	appendTrace(trace, event)

	handleRecovery(
		execCtx,
		current,
		parser.eofToken,
		event.Cursor,
		state,
	)
}

func updateTrace[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole comparable,
	TNodeKind comparable,
](
	event *ParseTraceEvent[TToken],
	result RuleResult[TObservation, TToken, TTokenRole, TNodeKind],
	ok bool,
	startPos int,
	endPos int,
) {
	event.RuleSucceeded = ok
	event.Consumed = endPos != startPos
	event.TopLevel = result.TopLevel
	event.NodeReturned = result.Node != nil
}

func appendTrace[TToken any](
	trace *ParseTrace[TToken],
	event ParseTraceEvent[TToken],
) {
	if trace != nil {
		trace.Events = append(trace.Events, event)
	}
}

func handleRecovery[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole comparable,
	TLexerState,
	TNodeKind comparable,
](
	execCtx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	current lexarch.Lexeme[TObservation, TToken, TTokenRole],
	eofToken TToken,
	startPos int,
	state *parseLoopState,
) {
	if !recoverWithContext(execCtx, current, eofToken) {
		return
	}

	if startPos == state.lastCursor {
		execCtx.Consume()
	}

	state.lastCursor = startPos
}
