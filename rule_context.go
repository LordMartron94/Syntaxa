package syntaxa

import (
	"cmp"
	"fmt"
	"lexarch"
)

// =============================================================
// CORES
// =============================================================

type tokenStream[TObs cmp.Ordered, TToken comparable, TTokenRole comparable] struct {
	peekRaw    func(int) Lexeme[TObs, TToken, TTokenRole]
	consumeRaw func() Lexeme[TObs, TToken, TTokenRole]

	eofToken TToken
	skipCore *skipCore[TTokenRole]

	nextVisibleRawIndex int
	nextVisibleCached   bool
}

func (ts *tokenStream[TObs, TToken, TTokenRole]) isEOF(l Lexeme[TObs, TToken, TTokenRole]) bool {
	return l.Token == ts.eofToken
}

func (ts *tokenStream[TObs, TToken, TTokenRole]) invalidatePeekCache() {
	ts.nextVisibleCached = false
	ts.nextVisibleRawIndex = 0
}

func (ts *tokenStream[TObs, TToken, TTokenRole]) consumeRawAndInvalidate() Lexeme[TObs, TToken, TTokenRole] {
	consumed := ts.consumeRaw()
	ts.invalidatePeekCache()
	return consumed
}

func (ts *tokenStream[TObs, TToken, TTokenRole]) resolveNextVisibleRaw() (Lexeme[TObs, TToken, TTokenRole], int) {
	rawIndex := 0
	for {
		cur := ts.peekRaw(rawIndex)
		if ts.isEOF(cur) || !ts.skipCore.isSkipped(cur.Role) {
			return cur, rawIndex
		}
		rawIndex++
	}
}

func (ts *tokenStream[TObs, TToken, TTokenRole]) skipForward() {
	for {
		cur := ts.peekRaw(0)
		if ts.isEOF(cur) || !ts.skipCore.isSkipped(cur.Role) {
			ts.nextVisibleRawIndex = 0
			ts.nextVisibleCached = true
			return
		}
		ts.consumeRawAndInvalidate()
	}
}

func (ts *tokenStream[TObs, TToken, TTokenRole]) Peek(n int) Lexeme[TObs, TToken, TTokenRole] {
	if n < 0 {
		panic("Peek: n must be >= 0")
	}
	if n == 0 {
		if ts.nextVisibleCached {
			return ts.peekRaw(ts.nextVisibleRawIndex)
		}
		cur, rawIndex := ts.resolveNextVisibleRaw()
		ts.nextVisibleRawIndex = rawIndex
		ts.nextVisibleCached = true
		return cur
	}

	if !ts.nextVisibleCached {
		_, rawIndex := ts.resolveNextVisibleRaw()
		ts.nextVisibleRawIndex = rawIndex
		ts.nextVisibleCached = true
	}

	seen := 0
	i := ts.nextVisibleRawIndex

	for {
		cur := ts.peekRaw(i)
		if ts.isEOF(cur) {
			return cur
		}

		if !ts.skipCore.isSkipped(cur.Role) {
			if seen == n {
				return cur
			}
			seen++
		}
		i++
	}
}

func (ts *tokenStream[TObs, TToken, TTokenRole]) PeekRaw(n int) Lexeme[TObs, TToken, TTokenRole] {
	return ts.peekRaw(n)
}

func (ts *tokenStream[TObs, TToken, TTokenRole]) Consume() Lexeme[TObs, TToken, TTokenRole] {
	ts.skipForward()
	return ts.consumeRawAndInvalidate()
}

func (ts *tokenStream[TObs, TToken, TTokenRole]) ConsumeRaw() Lexeme[TObs, TToken, TTokenRole] {
	return ts.consumeRawAndInvalidate()
}

// -------------------------------------------------------------

type recoveryFrame struct {
	startIndex int
	endIndex   int
	barrier    bool
}

type recoveryCore[TObservation cmp.Ordered, TToken comparable, TTokenRole comparable] struct {
	ts *tokenStream[TObservation, TToken, TTokenRole]

	defaultRecovery []TToken
	stack           []recoveryFrame
	tokensFlight    []TToken
}

func (rc *recoveryCore[_, TToken, _]) setDefaultRecovery(tokens ...TToken) {
	// Allocate once.
	rc.defaultRecovery = append([]TToken(nil), tokens...)
}

func (rc *recoveryCore[_, TToken, _]) pushRecovery(barrier bool, tokens ...TToken) {
	start := len(rc.tokensFlight)
	rc.tokensFlight = append(rc.tokensFlight, tokens...)

	rc.stack = append(rc.stack, recoveryFrame{
		startIndex: start,
		endIndex:   len(rc.tokensFlight),
		barrier:    barrier,
	})
}

func (rc *recoveryCore[_, TToken, _]) popRecovery() {
	if len(rc.stack) == 0 {
		return
	}

	top := rc.stack[len(rc.stack)-1]
	// Truncate the flight buffer to instantly discard this frame's tokens
	rc.tokensFlight = rc.tokensFlight[:top.startIndex]
	rc.stack = rc.stack[:len(rc.stack)-1]
}

/*
IsRecoveryToken queries the active scopes directly.
Zero allocations. Zero map hashing. Returns immediately on match.
*/
func (rc *recoveryCore[_, TToken, _]) IsRecoveryToken(token TToken) bool {
	if len(rc.stack) > 0 {
		top := rc.stack[len(rc.stack)-1]
		if containsToken(rc.tokensFlight[top.startIndex:top.endIndex], token) {
			return true
		}
		if top.barrier {
			return false
		}
	}

	return containsToken(rc.defaultRecovery, token)
}

/*
IsInAllFrames answers the question without building a merged set.
Used by performRecovery to determine if a token is safe to consume.
Zero allocations.
*/
func (rc *recoveryCore[_, TToken, _]) IsInAllFrames(token TToken) bool {
	if containsToken(rc.defaultRecovery, token) {
		return true
	}

	for i := len(rc.stack) - 1; i >= 0; i-- {
		frame := rc.stack[i]
		if containsToken(rc.tokensFlight[frame.startIndex:frame.endIndex], token) {
			return true
		}
		if frame.barrier {
			break
		}
	}

	return false
}

// Helper method keeping functions small and single-responsibility.
func containsToken[TToken comparable](slice []TToken, token TToken) bool {
	for _, t := range slice {
		if t == token {
			return true
		}
	}
	return false
}

/*
CollectAllRecoveryTokens extracts all active recovery tokens into a flat slice.
This allocates memory and should ONLY be called on the cold path (when an error has occurred and recovery is actually executing).
*/
func (rc *recoveryCore[_, TToken, _]) CollectAllRecoveryTokens() []TToken {
	var result []TToken
	result = append(result, rc.defaultRecovery...)

	for i := len(rc.stack) - 1; i >= 0; i-- {
		frame := rc.stack[i]
		result = append(result, rc.tokensFlight[frame.startIndex:frame.endIndex]...)
		if frame.barrier {
			break
		}
	}

	return deduplicateTokens(result)
}

// deduplicateTokens uses a simple linear scan. Since recovery sets are typically
// very small (< 10 items), this avoids map hashing overhead and allocations.
func deduplicateTokens[TToken comparable](tokens []TToken) []TToken {
	var out []TToken
	for _, t := range tokens {
		if !containsToken(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// -------------------------------------------------------------

type skipCore[TTokenRole comparable] struct {
	stack    []tokenSet[TTokenRole]
	onChange func()
}

func (sc *skipCore[TTokenRole]) PushSkipRoles(roles ...TTokenRole) {
	set := make(tokenSet[TTokenRole])
	for _, r := range roles {
		set[r] = struct{}{}
	}
	sc.stack = append(sc.stack, set)
	if sc.onChange != nil {
		sc.onChange()
	}
}

func (sc *skipCore[TTokenRole]) PopSkipRoles() {
	if len(sc.stack) > 0 {
		sc.stack = sc.stack[:len(sc.stack)-1]
		if sc.onChange != nil {
			sc.onChange()
		}
	}
}

func (sc *skipCore[TTokenRole]) isSkipped(role TTokenRole) bool {
	if len(sc.stack) == 0 {
		return false
	}
	_, skipped := sc.stack[len(sc.stack)-1][role]
	return skipped
}

// -------------------------------------------------------------

type errorCore[TObs cmp.Ordered, TToken, TTokenRole comparable] struct {
	sink *SyntaxErrors[TObs]
	ts   *tokenStream[TObs, TToken, TTokenRole]
}

func (ec *errorCore[TObs, _, _]) ReportHere(ruleName, message string) {
	current := ec.ts.Peek(0)

	ec.sink.report(SyntaxError[TObs]{
		Rule:             ruleName,
		Message:          message,
		StartLine:        current.StartLine,
		StartColumn:      current.StartColumn,
		EndLine:          current.EndLine,
		EndColumn:        current.EndColumn,
		AbsolutePosition: current.Start,
		TokenNumber:      current.TokenNumber,
	})
}

func (ec *errorCore[TObs, TToken, TTokenRole]) ReportAt(ruleName string, lexeme Lexeme[TObs, TToken, TTokenRole], message string) {
	ec.sink.report(SyntaxError[TObs]{
		Rule:             ruleName,
		Message:          message,
		StartLine:        lexeme.StartLine,
		StartColumn:      lexeme.StartColumn,
		EndLine:          lexeme.EndLine,
		EndColumn:        lexeme.EndColumn,
		AbsolutePosition: lexeme.Start,
		TokenNumber:      lexeme.TokenNumber,
	})
}

func (ec *errorCore[TObs, TToken, TTokenRole]) ReportAtEnd(ruleName string, lexeme Lexeme[TObs, TToken, TTokenRole], message string) {
	ec.sink.report(SyntaxError[TObs]{
		Rule:             ruleName,
		Message:          message,
		StartLine:        lexeme.EndLine,
		StartColumn:      lexeme.EndColumn,
		EndLine:          lexeme.EndLine,
		EndColumn:        lexeme.EndColumn,
		AbsolutePosition: lexeme.Start,
		TokenNumber:      lexeme.TokenNumber,
	})
}

func (ec *errorCore[TObs, TToken, TTokenRole]) ReportAdvanced(ruleName string, startLine, startColumn, endLine, endColumn, absolutePosition, tokenNumber int, message string) {
	ec.sink.report(SyntaxError[TObs]{
		Rule:             ruleName,
		Message:          message,
		StartLine:        startLine,
		StartColumn:      startColumn,
		EndLine:          endLine,
		EndColumn:        endColumn,
		AbsolutePosition: absolutePosition,
		TokenNumber:      tokenNumber,
	})
}

func (ec *errorCore[TObs, TToken, TTokenRole]) replaceBestErrorAt(ruleName string, lexeme Lexeme[TObs, TToken, TTokenRole], message string) bool {
	err := SyntaxError[TObs]{
		Rule:             ruleName,
		Message:          message,
		StartLine:        lexeme.StartLine,
		StartColumn:      lexeme.StartColumn,
		EndLine:          lexeme.EndLine,
		EndColumn:        lexeme.EndColumn,
		AbsolutePosition: lexeme.Start,
		TokenNumber:      lexeme.TokenNumber,
	}

	return ec.sink.replaceBest(err)
}

func (ec *errorCore[TObs, _, _]) reportLexerError(line, column int, description string) {
	ec.sink.report(SyntaxError[TObs]{
		Message:         description,
		StartLine:       line,
		StartColumn:     column,
		EndLine:         line,
		EndColumn:       column,
		ProducedByLexer: true,
	})
}

type tokenSet[TToken comparable] map[TToken]struct{}

/*
ExecRuleContext exposes transactional, mutating parsing operations.
*/
type ExecRuleContext[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
] struct {
	/* The Token endpoint provides access to the lexer token stream. */
	Token *tokenStream[TObservation, TToken, TTokenRole]

	/* The Recovery endpoint provides access to syntax error recovery functionality. */
	Recovery *recoveryCore[TObservation, TToken, TTokenRole]

	/* The Skip endpoint provides access to skip token functionality. */
	Skip *skipCore[TTokenRole]

	/* The Error endpoint provides access to syntax error reporting functionality. */
	Error *errorCore[TObservation, TToken, TTokenRole]

	/* ExecuteRule routes a rule through the parser for invariant detection. */
	ExecuteRule func(rule ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind], mode RuleExecutionMode) RuleResult[TObservation, TToken, TTokenRole, TNodeKind]

	ExecuteReference func(targetRule GrammarLabel, mode RuleExecutionMode) RuleResult[TObservation, TToken, TTokenRole, TNodeKind]

	// Specific LST/State helpers
	Editor *LSTEditor[TObservation, TToken, TTokenRole, TNodeKind]

	/* SetLexerState alters the lexer state for languages with multiple lexer states. */
	SetLexerState func(TLexerState)

	Select       *SelectRuleContext[TObservation, TToken, TTokenRole]
	Finalization *FinalizationCtx[TObservation, TToken, TTokenRole, TNodeKind]

	lastLexingError func() error
	createErrorNode func(string) *SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind]

	/*
		GetLastConsumedLexeme returns the last consumed lexeme and true if any token has been consumed.
		Used to report "expected X" at the end of the previous token (e.g. missing semicolon after "lspec").
		May be nil when the token source does not support it.
	*/
	GetLastConsumedLexeme func() (Lexeme[TObservation, TToken, TTokenRole], bool)

	GetAnalysis func() *GrammarAnalysis[TToken]

	/*
		EngineStats receives optional parse-engine counters when the caller passes a non-nil
		pointer into BuildExecRuleContext* (same lifecycle as parse).
	*/
	EngineStats *ParseEngineStats

	trace *ParseTrace[TToken]

	save    func() ParserSnapshot[TObservation, TLexerState]
	restore func(snapshot ParserSnapshot[TObservation, TLexerState])

	resultsScratch      []RuleResult[TObservation, TToken, TTokenRole, TNodeKind]
	resultsScratchMarks []int
}

func ExecRuleContextAcquireResultsScratch[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	ctx *ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	size int,
) []RuleResult[TObservation, TToken, TTokenRole, TNodeKind] {
	if size < 0 {
		panic("ExecRuleContextAcquireResultsScratch: size must be >= 0")
	}

	start := len(ctx.resultsScratch)
	end := start + size

	ctx.resultsScratchMarks = append(ctx.resultsScratchMarks, start)

	if cap(ctx.resultsScratch) < end {
		nextCapacity := end
		if nextCapacity < 2*cap(ctx.resultsScratch) {
			nextCapacity = 2 * cap(ctx.resultsScratch)
		}

		next := make([]RuleResult[TObservation, TToken, TTokenRole, TNodeKind], len(ctx.resultsScratch), nextCapacity)
		copy(next, ctx.resultsScratch)
		ctx.resultsScratch = next
	}

	ctx.resultsScratch = ctx.resultsScratch[:end]
	return ctx.resultsScratch[start:end]
}

func ExecRuleContextReleaseResultsScratch[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	ctx *ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) {
	if len(ctx.resultsScratchMarks) == 0 {
		panic("ExecRuleContextReleaseResultsScratch: release without acquire")
	}

	last := len(ctx.resultsScratchMarks) - 1
	start := ctx.resultsScratchMarks[last]
	ctx.resultsScratchMarks = ctx.resultsScratchMarks[:last]

	var zero RuleResult[TObservation, TToken, TTokenRole, TNodeKind]
	for i := start; i < len(ctx.resultsScratch); i++ {
		ctx.resultsScratch[i] = zero
	}

	ctx.resultsScratch = ctx.resultsScratch[:start]
}

type SelectRuleContext[TObservation cmp.Ordered, TToken, TTokenRole comparable] struct {
	ts *tokenStream[TObservation, TToken, TTokenRole]
}

func (s *SelectRuleContext[TObservation, TToken, TTokenRole]) Peek(n int) Lexeme[TObservation, TToken, TTokenRole] {
	return s.ts.Peek(n)
}

func (s *SelectRuleContext[TObservation, TToken, TTokenRole]) PeekRaw(n int) Lexeme[TObservation, TToken, TTokenRole] {
	return s.ts.PeekRaw(n)
}

type FinalizationCtx[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind comparable] struct {
	editor *LSTEditor[TObservation, TToken, TTokenRole, TNodeKind]
}

/* SetAttribute sets a named attribute for the node, overriding any attribute with the same name if existent.*/
func (f *FinalizationCtx[TObservation, TToken, TTokenRole, TNodeKind]) SetAttribute(node *SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind], attributeName string, value any) {
	f.editor.SetAttribute(node, attributeName, value)
}

/* DeleteAttribute deletes a named attribute for the node.*/
func (f *FinalizationCtx[TObservation, TToken, TTokenRole, TNodeKind]) DeleteAttribute(node *SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind], attributeName string) {
	f.editor.DeleteAttribute(node, attributeName)
}

// =============================================================
// BUILDERS
// =============================================================

type rawSource[TObs cmp.Ordered, TToken, TTokenRole comparable] struct {
	peek    func(int) Lexeme[TObs, TToken, TTokenRole]
	consume func() Lexeme[TObs, TToken, TTokenRole]
}

func BuildExecRuleContextFromSlice[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	lexemes []Lexeme[TObservation, TToken, TTokenRole],
	errors *SyntaxErrors[TObservation],
	cursor *int,
	streamStats *ParseStreamStats,
	engineStats *ParseEngineStats,
) *ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	eofLex := Lexeme[TObservation, TToken, TTokenRole]{Token: parser.eofToken}

	src := rawSource[TObservation, TToken, TTokenRole]{
		peek: func(n int) Lexeme[TObservation, TToken, TTokenRole] {
			idx := *cursor + n
			if idx >= len(lexemes) {
				return eofLex
			}
			return lexemes[idx]
		},
		consume: func() Lexeme[TObservation, TToken, TTokenRole] {
			if *cursor >= len(lexemes) {
				return eofLex
			}
			l := lexemes[*cursor]
			*cursor++
			return l
		},
	}

	save := func() ParserSnapshot[TObservation, TLexerState] {
		return ParserSnapshot[TObservation, TLexerState]{tokenIndex: *cursor}
	}

	restore := func(c ParserSnapshot[TObservation, TLexerState]) {
		*cursor = c.tokenIndex
	}

	ctx := buildBaseContext(parser, errors, src, save, restore, streamStats, engineStats)

	// Context specific overrides
	ctx.lastLexingError = func() error { return nil }
	ctx.SetLexerState = func(s TLexerState) {}
	ctx.GetLastConsumedLexeme = func() (Lexeme[TObservation, TToken, TTokenRole], bool) {
		if *cursor > 0 {
			return lexemes[*cursor-1], true
		}
		var z Lexeme[TObservation, TToken, TTokenRole]
		return z, false
	}

	return ctx
}

func BuildExecRuleContextFromLexerSession[
	TObservation cmp.Ordered,
	TState comparable,
	TToken comparable,
	TTokenRole comparable,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TState],
	lexer *lexarch.Lexer,
	session *lexarch.LexingSession,
	source string,
	errors *SyntaxErrors[TObservation],
	streamStats *ParseStreamStats,
	engineStats *ParseEngineStats,
) *ExecRuleContext[TObservation, TToken, TTokenRole, TState, TNodeKind] {
	var lastConsumed Lexeme[TObservation, TToken, TTokenRole]
	var hasConsumed bool
	eofLex := Lexeme[TObservation, TToken, TTokenRole]{Token: parser.eofToken}
	out := lexarch.LexingSessionNextResultCreate()
	nextTokenNumber := 0

	resolveLexeme := func(tokenNumber int) Lexeme[TObservation, TToken, TTokenRole] {
		// When lexer reports a runtime/validation error, present EOF to parser logic.
		// The concrete error is still available via ctx.lastLexingError().
		if out.LexingError != nil || out.Token == nil {
			return eofLex
		}

		if out.Token.Kind == lexarch.TokenKindEOF {
			return eofLex
		}

		if out.Token.Kind == lexarch.TokenKindError {
			return eofLex
		}

		return LexemeFromToken[TObservation, TToken, TTokenRole](out.Token, source, 4, tokenNumber)
	}

	src := rawSource[TObservation, TToken, TTokenRole]{
		peek: func(n int) Lexeme[TObservation, TToken, TTokenRole] {
			lexarch.LexingSessionPeek(session, out, n+1)
			return resolveLexeme(nextTokenNumber + n)
		},
		consume: func() Lexeme[TObservation, TToken, TTokenRole] {
			lexarch.LexingSessionConsume(session, out)
			l := resolveLexeme(nextTokenNumber)
			nextTokenNumber++
			lastConsumed = l
			hasConsumed = true
			return l
		},
	}

	save := func() ParserSnapshot[TObservation, TState] {
		snap := lexarch.LexingSessionSnapshotCreate(session)
		return ParserSnapshot[TObservation, TState]{tokenIndex: nextTokenNumber, lexerSnap: snap}
	}

	restore := func(c ParserSnapshot[TObservation, TState]) {
		lexarch.LexingSessionSnapshotRestore(session, c.lexerSnap)
		nextTokenNumber = c.tokenIndex
	}

	ctx := buildBaseContext(parser, errors, src, save, restore, streamStats, engineStats)

	ctx.lastLexingError = func() error { return out.LexingError }
	ctx.SetLexerState = func(state TState) { lexarch.LexingSessionSet(session, fmt.Sprintf("%v", state)) }
	ctx.GetLastConsumedLexeme = func() (Lexeme[TObservation, TToken, TTokenRole], bool) {
		return lastConsumed, hasConsumed
	}

	return ctx
}

func buildBaseContext[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	errors *SyntaxErrors[TObservation],
	src rawSource[TObservation, TToken, TTokenRole],
	saveFn func() ParserSnapshot[TObservation, TLexerState],
	restoreFn func(ParserSnapshot[TObservation, TLexerState]),
	streamStats *ParseStreamStats,
	engineStats *ParseEngineStats,
) *ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	peekRaw := src.peek
	consumeRaw := src.consume
	if streamStats != nil {
		origPeek := peekRaw
		origConsume := consumeRaw
		peekRaw = func(n int) Lexeme[TObservation, TToken, TTokenRole] {
			streamStats.RawPeekCalls++
			return origPeek(n)
		}
		consumeRaw = func() Lexeme[TObservation, TToken, TTokenRole] {
			streamStats.RawConsumeCalls++
			return origConsume()
		}
	}

	// Initialize Cores
	sCore := &skipCore[TTokenRole]{stack: make([]tokenSet[TTokenRole], 0)}
	if len(parser.defaultSkipRoles) > 0 {
		sCore.PushSkipRoles(parser.defaultSkipRoles...)
	}

	tStream := &tokenStream[TObservation, TToken, TTokenRole]{
		peekRaw:    peekRaw,
		consumeRaw: consumeRaw,
		skipCore:   sCore,
		eofToken:   parser.eofToken,
	}
	sCore.onChange = tStream.invalidatePeekCache

	rCore := &recoveryCore[TObservation, TToken, TTokenRole]{
		ts: tStream,
	}
	rCore.setDefaultRecovery(parser.eofToken)
	eCore := &errorCore[TObservation, TToken, TTokenRole]{sink: errors, ts: tStream}

	editor := &LSTEditor[TObservation, TToken, TTokenRole, TNodeKind]{
		created:          make([]*SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind], 0),
		nodeFree:         make([]*SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind], 0, parser.nodePoolPrefill),
		nodeGrow:         parser.nodePoolGrow,
		parseEngineStats: engineStats,
	}
	for i := 0; i < parser.nodePoolPrefill; i++ {
		editor.nodeFree = append(editor.nodeFree, &SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind]{})
	}

	selectCtx := &SelectRuleContext[TObservation, TToken, TTokenRole]{
		ts: tStream,
	}

	finalCtx := &FinalizationCtx[TObservation, TToken, TTokenRole, TNodeKind]{
		editor: editor,
	}

	var cachedAnalysis *GrammarAnalysis[TToken]
	if parser.getAnalysis != nil {
		cachedAnalysis = parser.getAnalysis()
	}

	// Assemble Context
	ctx := ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{
		Token:    tStream,
		Recovery: rCore,
		Skip:     sCore,
		Error:    eCore,
		Editor:   editor,
		createErrorNode: func(message string) *SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind] {
			n := editor.NewNode(parser.errorNodeKind)
			editor.SetAttribute(n, "error", message)
			return n
		},
		save: func() ParserSnapshot[TObservation, TLexerState] {
			snapshot := saveFn()
			snapshot.nextVisibleRawIndex = tStream.nextVisibleRawIndex
			snapshot.nextVisibleCached = tStream.nextVisibleCached
			return snapshot
		},
		restore: func(snapshot ParserSnapshot[TObservation, TLexerState]) {
			restoreFn(snapshot)
			tStream.nextVisibleRawIndex = snapshot.nextVisibleRawIndex
			tStream.nextVisibleCached = snapshot.nextVisibleCached
		},
		Select:       selectCtx,
		Finalization: finalCtx,
		GetAnalysis: func() *GrammarAnalysis[TToken] {
			return cachedAnalysis
		},
		EngineStats: engineStats,
	}

	var trace *ParseTrace[TToken]
	if parser.debugTrace {
		trace = &ParseTrace[TToken]{
			Events: make([]ParseTraceEvent[TToken], 0),
		}
		ctx.trace = trace
	}

	ctxPtr := &ctx

	ctxPtr.ExecuteRule = func(
		rule ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
		mode RuleExecutionMode,
	) RuleResult[TObservation, TToken, TTokenRole, TNodeKind] {
		return syntaxaParserExecuteRule(parser, ctxPtr, rule, mode)
	}

	ctxPtr.ExecuteReference = func(targetRule GrammarLabel, mode RuleExecutionMode) RuleResult[TObservation, TToken, TTokenRole, TNodeKind] {
		if st := ctxPtr.EngineStats; st != nil {
			st.ReferenceDirectCalls++
		}

		resolvedRule, ok := parser.registry[targetRule]
		if !ok {
			panic(fmt.Errorf("runtime engine error: unresolved target rule '%s'", targetRule))
		}

		// Deliberately bypass syntaxaParserExecuteRule.
		// No snapshots, no error frames, no recovery pushing.
		// The proxy's syntaxaParserExecuteRule has already inherited
		// and hoisted all necessary state for this target.
		return resolvedRule.executionFn(ctxPtr)
	}

	return ctxPtr
}
