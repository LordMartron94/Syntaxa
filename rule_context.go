package syntaxa

import (
	"fmt"
	"lexarch"
)

// =============================================================
// CORES
// =============================================================

type tokenStream struct {
	peekRaw    func(int) Lexeme
	consumeRaw func() Lexeme

	eofToken lexarch.TokenKind
	skipCore *skipCore

	nextVisibleRawIndex int
	nextVisibleCached   bool
}

func (ts *tokenStream) isEOF(l Lexeme) bool {
	return l.Token == ts.eofToken
}

func (ts *tokenStream) invalidatePeekCache() {
	ts.nextVisibleCached = false
	ts.nextVisibleRawIndex = 0
}

func (ts *tokenStream) consumeRawAndInvalidate() Lexeme {
	consumed := ts.consumeRaw()
	ts.invalidatePeekCache()
	return consumed
}

func (ts *tokenStream) resolveNextVisibleRaw() (Lexeme, int) {
	rawIndex := 0
	for {
		cur := ts.peekRaw(rawIndex)
		if ts.isEOF(cur) || !ts.skipCore.isSkipped(cur.Role) {
			return cur, rawIndex
		}
		rawIndex++
	}
}

func (ts *tokenStream) skipForward() {
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

func (ts *tokenStream) Peek(n int) Lexeme {
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

func (ts *tokenStream) PeekRaw(n int) Lexeme {
	return ts.peekRaw(n)
}

func (ts *tokenStream) Consume() Lexeme {
	ts.skipForward()
	return ts.consumeRawAndInvalidate()
}

func (ts *tokenStream) ConsumeRaw() Lexeme {
	return ts.consumeRawAndInvalidate()
}

// -------------------------------------------------------------

type recoveryFrame struct {
	startIndex int
	endIndex   int
	barrier    bool
}

type recoveryCore struct {
	ts *tokenStream

	defaultRecovery []lexarch.TokenKind
	stack           []recoveryFrame
	tokensFlight    []lexarch.TokenKind
}

func (rc *recoveryCore) setDefaultRecovery(tokens ...lexarch.TokenKind) {
	// Allocate once.
	rc.defaultRecovery = append([]lexarch.TokenKind(nil), tokens...)
}

func (rc *recoveryCore) pushRecovery(barrier bool, tokens ...lexarch.TokenKind) {
	start := len(rc.tokensFlight)
	rc.tokensFlight = append(rc.tokensFlight, tokens...)

	rc.stack = append(rc.stack, recoveryFrame{
		startIndex: start,
		endIndex:   len(rc.tokensFlight),
		barrier:    barrier,
	})
}

func (rc *recoveryCore) popRecovery() {
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
func (rc *recoveryCore) IsRecoveryToken(token lexarch.TokenKind) bool {
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
func (rc *recoveryCore) IsInAllFrames(token lexarch.TokenKind) bool {
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
func containsToken(slice []lexarch.TokenKind, token lexarch.TokenKind) bool {
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
func (rc *recoveryCore) CollectAllRecoveryTokens() []lexarch.TokenKind {
	var result []lexarch.TokenKind
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
func deduplicateTokens(tokens []lexarch.TokenKind) []lexarch.TokenKind {
	var out []lexarch.TokenKind
	for _, t := range tokens {
		if !containsToken(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// -------------------------------------------------------------

type skipCore struct {
	stack    []tokenSet
	onChange func()
}

func (sc *skipCore) PushSkipRoles(roles ...lexarch.TokenRole) {
	set := make(tokenSet)
	for _, r := range roles {
		set[r] = struct{}{}
	}
	sc.stack = append(sc.stack, set)
	if sc.onChange != nil {
		sc.onChange()
	}
}

func (sc *skipCore) PopSkipRoles() {
	if len(sc.stack) > 0 {
		sc.stack = sc.stack[:len(sc.stack)-1]
		if sc.onChange != nil {
			sc.onChange()
		}
	}
}

func (sc *skipCore) isSkipped(role lexarch.TokenRole) bool {
	if len(sc.stack) == 0 {
		return false
	}
	_, skipped := sc.stack[len(sc.stack)-1][role]
	return skipped
}

// -------------------------------------------------------------

type errorCore struct {
	sink *SyntaxErrors
	ts   *tokenStream
}

func (ec *errorCore) ReportHere(ruleName, message string) {
	current := ec.ts.Peek(0)

	ec.sink.report(SyntaxError{
		Rule:             ruleName,
		Message:          message,
		AbsolutePosition: current.Start,
		AbsoluteEnd:      current.End,
		TokenNumber:      current.TokenNumber,
	})
}

func (ec *errorCore) ReportAt(ruleName string, lexeme Lexeme, message string) {
	ec.sink.report(SyntaxError{
		Rule:             ruleName,
		Message:          message,
		AbsolutePosition: lexeme.Start,
		AbsoluteEnd:      lexeme.End,
		TokenNumber:      lexeme.TokenNumber,
	})
}

func (ec *errorCore) ReportAtEnd(ruleName string, lexeme Lexeme, message string) {
	ec.sink.report(SyntaxError{
		Rule:             ruleName,
		Message:          message,
		AbsolutePosition: lexeme.End,
		AbsoluteEnd:      lexeme.End,
		TokenNumber:      lexeme.TokenNumber,
	})
}

func (ec *errorCore) ReportAdvanced(ruleName string, startLine, startColumn, endLine, endColumn, absolutePosition, tokenNumber int, message string) {
	_ = startLine
	_ = startColumn
	_ = endLine
	_ = endColumn
	ec.sink.report(SyntaxError{
		Rule:             ruleName,
		Message:          message,
		AbsolutePosition: absolutePosition,
		AbsoluteEnd:      absolutePosition,
		TokenNumber:      tokenNumber,
	})
}

func (ec *errorCore) replaceBestErrorAt(ruleName string, lexeme Lexeme, message string) bool {
	err := SyntaxError{
		Rule:             ruleName,
		Message:          message,
		AbsolutePosition: lexeme.Start,
		AbsoluteEnd:      lexeme.End,
		TokenNumber:      lexeme.TokenNumber,
	}

	return ec.sink.replaceBest(err)
}

func (ec *errorCore) reportLexerError(line, column int, description string) {
	ec.sink.report(SyntaxError{
		Message:          description,
		StartLine:        line,
		StartColumn:      column,
		EndLine:          line,
		EndColumn:        column,
		AbsolutePosition: 0,
		AbsoluteEnd:      0,
		ProducedByLexer:  true,
	})
}

type tokenSet map[lexarch.TokenRole]struct{}

/*
ExecRuleContext exposes transactional, mutating parsing operations.
*/
type ExecRuleContext[TNodeKind comparable] struct {
	/* The Token endpoint provides access to the lexer token stream. */
	Token *tokenStream

	/* The Recovery endpoint provides access to syntax error recovery functionality. */
	Recovery *recoveryCore

	/* The Skip endpoint provides access to skip token functionality. */
	Skip *skipCore

	/* The Error endpoint provides access to syntax error reporting functionality. */
	Error *errorCore

	/* ExecuteRule routes a rule through the parser for invariant detection. */
	ExecuteRule func(rule ParserRule[TNodeKind], mode RuleExecutionMode) RuleResult[TNodeKind]

	ExecuteReference func(targetRule GrammarLabel, mode RuleExecutionMode) RuleResult[TNodeKind]

	// Specific LST/State helpers
	Editor *LSTEditor[TNodeKind]

	/* SetLexerState alters the lexer state for languages with multiple lexer states. */
	SetLexerState func(string)

	Select       *SelectRuleContext
	Finalization *FinalizationCtx[TNodeKind]

	lastLexingError func() error
	createErrorNode func(string) *SyntaxaLSTNode[TNodeKind]

	/*
		GetLastConsumedLexeme returns the last consumed lexeme and true if any token has been consumed.
		Used to report "expected X" at the end of the previous token (e.g. missing semicolon after "lspec").
		May be nil when the token source does not support it.
	*/
	GetLastConsumedLexeme func() (Lexeme, bool)

	GetAnalysis func() *GrammarAnalysis

	/*
		EngineStats receives optional parse-engine counters when the caller passes a non-nil
		pointer into BuildExecRuleContext* (same lifecycle as parse).
	*/
	EngineStats *ParseEngineStats

	trace *ParseTrace

	save    func() ParserSnapshot
	restore func(snapshot ParserSnapshot)

	resultsScratch      []RuleResult[TNodeKind]
	resultsScratchMarks []int
}

func ExecRuleContextAcquireResultsScratch[TNodeKind comparable](
	ctx *ExecRuleContext[TNodeKind],
	size int,
) []RuleResult[TNodeKind] {
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

		next := make([]RuleResult[TNodeKind], len(ctx.resultsScratch), nextCapacity)
		copy(next, ctx.resultsScratch)
		ctx.resultsScratch = next
	}

	ctx.resultsScratch = ctx.resultsScratch[:end]
	return ctx.resultsScratch[start:end]
}

func ExecRuleContextReleaseResultsScratch[TNodeKind comparable](
	ctx *ExecRuleContext[TNodeKind],
) {
	if len(ctx.resultsScratchMarks) == 0 {
		panic("ExecRuleContextReleaseResultsScratch: release without acquire")
	}

	last := len(ctx.resultsScratchMarks) - 1
	start := ctx.resultsScratchMarks[last]
	ctx.resultsScratchMarks = ctx.resultsScratchMarks[:last]

	var zero RuleResult[TNodeKind]
	for i := start; i < len(ctx.resultsScratch); i++ {
		ctx.resultsScratch[i] = zero
	}

	ctx.resultsScratch = ctx.resultsScratch[:start]
}

type SelectRuleContext struct {
	ts *tokenStream
}

func (s *SelectRuleContext) Peek(n int) Lexeme {
	return s.ts.Peek(n)
}

func (s *SelectRuleContext) PeekRaw(n int) Lexeme {
	return s.ts.PeekRaw(n)
}

type FinalizationCtx[TNodeKind comparable] struct {
	editor *LSTEditor[TNodeKind]
}

/* SetAttribute sets a named attribute for the node, overriding any attribute with the same name if existent.*/
func (f *FinalizationCtx[TNodeKind]) SetAttribute(node *SyntaxaLSTNode[TNodeKind], attributeName string, value any) {
	f.editor.SetAttribute(node, attributeName, value)
}

/* DeleteAttribute deletes a named attribute for the node.*/
func (f *FinalizationCtx[TNodeKind]) DeleteAttribute(node *SyntaxaLSTNode[TNodeKind], attributeName string) {
	f.editor.DeleteAttribute(node, attributeName)
}

// =============================================================
// BUILDERS
// =============================================================

type rawSource struct {
	peek    func(int) Lexeme
	consume func() Lexeme
}

func BuildExecRuleContextFromSlice[TNodeKind comparable](
	parser *SyntaxaParser[TNodeKind],
	lexemes []Lexeme,
	errors *SyntaxErrors,
	cursor *int,
	streamStats *ParseStreamStats,
	engineStats *ParseEngineStats,
) *ExecRuleContext[TNodeKind] {
	eofLex := Lexeme{Token: parser.eofToken}

	src := rawSource{
		peek: func(n int) Lexeme {
			idx := *cursor + n
			if idx >= len(lexemes) {
				return eofLex
			}
			return lexemes[idx]
		},
		consume: func() Lexeme {
			if *cursor >= len(lexemes) {
				return eofLex
			}
			l := lexemes[*cursor]
			*cursor++
			return l
		},
	}

	save := func() ParserSnapshot {
		return ParserSnapshot{tokenIndex: *cursor}
	}

	restore := func(c ParserSnapshot) {
		*cursor = c.tokenIndex
	}

	ctx := buildBaseContext(parser, errors, src, save, restore, streamStats, engineStats)

	// Context specific overrides
	ctx.lastLexingError = func() error { return nil }
	ctx.SetLexerState = func(s string) {}
	ctx.GetLastConsumedLexeme = func() (Lexeme, bool) {
		if *cursor > 0 {
			return lexemes[*cursor-1], true
		}
		var z Lexeme
		return z, false
	}

	return ctx
}

func BuildExecRuleContextFromLexerSession[TNodeKind comparable](
	parser *SyntaxaParser[TNodeKind],
	lexer *lexarch.Lexer,
	session *lexarch.LexingSession,
	source string,
	errors *SyntaxErrors,
	streamStats *ParseStreamStats,
	engineStats *ParseEngineStats,
) *ExecRuleContext[TNodeKind] {
	var lastConsumed Lexeme
	var hasConsumed bool
	eofLex := Lexeme{Token: parser.eofToken}
	out := lexarch.LexingSessionNextResultCreate()
	nextTokenNumber := 0

	resolveLexeme := func(tokenNumber int) Lexeme {
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

		return LexemeFromToken(out.Token, source, 4, tokenNumber)
	}

	src := rawSource{
		peek: func(n int) Lexeme {
			lexarch.LexingSessionPeek(session, out, n+1)
			return resolveLexeme(nextTokenNumber + n)
		},
		consume: func() Lexeme {
			lexarch.LexingSessionConsume(session, out)
			l := resolveLexeme(nextTokenNumber)
			nextTokenNumber++
			lastConsumed = l
			hasConsumed = true
			return l
		},
	}

	save := func() ParserSnapshot {
		snap := lexarch.LexingSessionSnapshotCreate(session)
		return ParserSnapshot{tokenIndex: nextTokenNumber, lexerSnap: snap}
	}

	restore := func(c ParserSnapshot) {
		lexarch.LexingSessionSnapshotRestore(session, c.lexerSnap)
		nextTokenNumber = c.tokenIndex
	}

	ctx := buildBaseContext(parser, errors, src, save, restore, streamStats, engineStats)

	ctx.lastLexingError = func() error { return out.LexingError }
	ctx.SetLexerState = func(state string) { lexarch.LexingSessionSet(session, state) }
	ctx.GetLastConsumedLexeme = func() (Lexeme, bool) {
		return lastConsumed, hasConsumed
	}

	return ctx
}

func buildBaseContext[TNodeKind comparable](
	parser *SyntaxaParser[TNodeKind],
	errors *SyntaxErrors,
	src rawSource,
	saveFn func() ParserSnapshot,
	restoreFn func(ParserSnapshot),
	streamStats *ParseStreamStats,
	engineStats *ParseEngineStats,
) *ExecRuleContext[TNodeKind] {

	peekRaw := src.peek
	consumeRaw := src.consume
	if streamStats != nil {
		origPeek := peekRaw
		origConsume := consumeRaw
		peekRaw = func(n int) Lexeme {
			streamStats.RawPeekCalls++
			return origPeek(n)
		}
		consumeRaw = func() Lexeme {
			streamStats.RawConsumeCalls++
			return origConsume()
		}
	}

	// Initialize Cores
	sCore := &skipCore{stack: make([]tokenSet, 0)}
	if len(parser.defaultSkipRoles) > 0 {
		sCore.PushSkipRoles(parser.defaultSkipRoles...)
	}

	tStream := &tokenStream{
		peekRaw:    peekRaw,
		consumeRaw: consumeRaw,
		skipCore:   sCore,
		eofToken:   parser.eofToken,
	}
	sCore.onChange = tStream.invalidatePeekCache

	rCore := &recoveryCore{
		ts: tStream,
	}
	rCore.setDefaultRecovery(parser.eofToken)
	eCore := &errorCore{sink: errors, ts: tStream}

	editor := &LSTEditor[TNodeKind]{
		created:          make([]*SyntaxaLSTNode[TNodeKind], 0),
		nodeFree:         make([]*SyntaxaLSTNode[TNodeKind], 0, parser.nodePoolPrefill),
		nodeGrow:         parser.nodePoolGrow,
		parseEngineStats: engineStats,
	}
	for i := 0; i < parser.nodePoolPrefill; i++ {
		editor.nodeFree = append(editor.nodeFree, &SyntaxaLSTNode[TNodeKind]{})
	}

	selectCtx := &SelectRuleContext{
		ts: tStream,
	}

	finalCtx := &FinalizationCtx[TNodeKind]{
		editor: editor,
	}

	var cachedAnalysis *GrammarAnalysis
	if parser.getAnalysis != nil {
		cachedAnalysis = parser.getAnalysis()
	}

	// Assemble Context
	ctx := ExecRuleContext[TNodeKind]{
		Token:    tStream,
		Recovery: rCore,
		Skip:     sCore,
		Error:    eCore,
		Editor:   editor,
		createErrorNode: func(message string) *SyntaxaLSTNode[TNodeKind] {
			n := editor.NewNode(parser.errorNodeKind)
			editor.SetAttribute(n, "error", message)
			return n
		},
		save: func() ParserSnapshot {
			snapshot := saveFn()
			snapshot.nextVisibleRawIndex = tStream.nextVisibleRawIndex
			snapshot.nextVisibleCached = tStream.nextVisibleCached
			return snapshot
		},
		restore: func(snapshot ParserSnapshot) {
			restoreFn(snapshot)
			tStream.nextVisibleRawIndex = snapshot.nextVisibleRawIndex
			tStream.nextVisibleCached = snapshot.nextVisibleCached
		},
		Select:       selectCtx,
		Finalization: finalCtx,
		GetAnalysis: func() *GrammarAnalysis {
			return cachedAnalysis
		},
		EngineStats: engineStats,
	}

	var trace *ParseTrace
	if parser.debugTrace {
		trace = &ParseTrace{
			Events: make([]ParseTraceEvent, 0),
		}
		ctx.trace = trace
	}

	ctxPtr := &ctx

	ctxPtr.ExecuteRule = func(
		rule ParserRule[TNodeKind],
		mode RuleExecutionMode,
	) RuleResult[TNodeKind] {
		return syntaxaParserExecuteRule(parser, ctxPtr, rule, mode)
	}

	ctxPtr.ExecuteReference = func(targetRule GrammarLabel, mode RuleExecutionMode) RuleResult[TNodeKind] {
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
