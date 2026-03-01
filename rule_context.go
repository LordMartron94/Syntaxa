package syntaxa

import (
	"cmp"
	"lexarch"
)

// =============================================================
// CORES
// =============================================================

type tokenStream[TObs cmp.Ordered, TToken comparable, TTokenRole comparable] struct {
	peekRaw    func(int) lexarch.Lexeme[TObs, TToken, TTokenRole]
	consumeRaw func() lexarch.Lexeme[TObs, TToken, TTokenRole]

	eofToken TToken
	skipCore *skipCore[TTokenRole]
}

func (ts *tokenStream[TObs, TToken, TTokenRole]) isEOF(l lexarch.Lexeme[TObs, TToken, TTokenRole]) bool {
	return l.Token == ts.eofToken
}

func (ts *tokenStream[TObs, TToken, TTokenRole]) skipForward() {
	for {
		cur := ts.peekRaw(0)
		if ts.isEOF(cur) || !ts.skipCore.isSkipped(cur.Role) {
			return
		}
		ts.consumeRaw()
	}
}

func (ts *tokenStream[TObs, TToken, TTokenRole]) Peek(n int) lexarch.Lexeme[TObs, TToken, TTokenRole] {
	if n < 0 {
		panic("Peek: n must be >= 0")
	}

	seen := 0
	i := 0

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

func (ts *tokenStream[TObs, TToken, TTokenRole]) PeekRaw(n int) lexarch.Lexeme[TObs, TToken, TTokenRole] {
	return ts.peekRaw(n)
}

func (ts *tokenStream[TObs, TToken, TTokenRole]) Consume() lexarch.Lexeme[TObs, TToken, TTokenRole] {
	ts.skipForward()
	return ts.consumeRaw()
}

func (ts *tokenStream[TObs, TToken, TTokenRole]) ConsumeRaw() lexarch.Lexeme[TObs, TToken, TTokenRole] {
	return ts.consumeRaw()
}

// -------------------------------------------------------------

type recoveryCore[TObservation cmp.Ordered, TToken, TTokenRole comparable] struct {
	ts *tokenStream[TObservation, TToken, TTokenRole]

	defaultRecovery tokenSet[TToken]
	stack           []tokenSet[TToken]
}

func (rc *recoveryCore[_, TToken, _]) setDefaultRecovery(tokens ...TToken) {
	set := make(tokenSet[TToken])
	for _, r := range tokens {
		set[r] = struct{}{}
	}
	rc.defaultRecovery = set
}

func (rc *recoveryCore[_, TToken, _]) pushRecovery(tokens ...TToken) {
	set := make(tokenSet[TToken])
	for _, r := range tokens {
		set[r] = struct{}{}
	}
	rc.stack = append(rc.stack, set)
}

func (rc *recoveryCore[_, TToken, _]) popRecovery() {
	if len(rc.stack) > 0 {
		rc.stack = rc.stack[:len(rc.stack)-1]
	}
}

func (rc *recoveryCore[_, TToken, _]) currentRecovery() tokenSet[TToken] {
	merged := make(tokenSet[TToken])

	for t := range rc.defaultRecovery {
		merged[t] = struct{}{}
	}

	for _, frame := range rc.stack {
		for t := range frame {
			merged[t] = struct{}{}
		}
	}

	return merged
}

// -------------------------------------------------------------

type skipCore[TTokenRole comparable] struct {
	stack []tokenSet[TTokenRole]
}

func (sc *skipCore[TTokenRole]) PushSkipRoles(roles ...TTokenRole) {
	set := make(tokenSet[TTokenRole])
	for _, r := range roles {
		set[r] = struct{}{}
	}
	sc.stack = append(sc.stack, set)
}

func (sc *skipCore[TTokenRole]) PopSkipRoles() {
	if len(sc.stack) > 0 {
		sc.stack = sc.stack[:len(sc.stack)-1]
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

func (ec *errorCore[TObs, TToken, TTokenRole]) ReportAt(ruleName string, lexeme lexarch.Lexeme[TObs, TToken, TTokenRole], message string) {
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

func (ec *errorCore[TObs, TToken, TTokenRole]) ReportAtEnd(ruleName string, lexeme lexarch.Lexeme[TObs, TToken, TTokenRole], message string) {
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

func (ec *errorCore[TObs, TToken, TTokenRole]) replaceBestErrorAt(ruleName string, lexeme lexarch.Lexeme[TObs, TToken, TTokenRole], message string) bool {
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

	// Specific LST/State helpers
	Editor *LSTEditor[TObservation, TToken, TTokenRole, TNodeKind]

	/* SetLexerState alters the lexer state for languages with multiple lexer states. */
	SetLexerState func(TLexerState)

	Select       *SelectRuleContext[TObservation, TToken, TTokenRole]
	Finalization *FinalizationCtx[TObservation, TToken, TTokenRole, TNodeKind]

	lastLexingError func() *lexarch.LexingError[TObservation, TToken]
	createErrorNode func(string) *SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind]

	trace *ParseTrace[TToken]

	save    func() ParserSnapshot[TObservation, TLexerState]
	restore func(snapshot ParserSnapshot[TObservation, TLexerState])
}

type SelectRuleContext[TObservation cmp.Ordered, TToken, TTokenRole comparable] struct {
	ts *tokenStream[TObservation, TToken, TTokenRole]
}

func (s *SelectRuleContext[TObservation, TToken, TTokenRole]) Peek(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
	return s.ts.Peek(n)
}

func (s *SelectRuleContext[TObservation, TToken, TTokenRole]) PeekRaw(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
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
	peek    func(int) lexarch.Lexeme[TObs, TToken, TTokenRole]
	consume func() lexarch.Lexeme[TObs, TToken, TTokenRole]
}

func BuildExecRuleContextFromSlice[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	lexemes []lexarch.Lexeme[TObservation, TToken, TTokenRole],
	errors *SyntaxErrors[TObservation],
	cursor *int,
) *ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	eofLex := lexarch.Lexeme[TObservation, TToken, TTokenRole]{Token: parser.eofToken}

	src := rawSource[TObservation, TToken, TTokenRole]{
		peek: func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
			idx := *cursor + n
			if idx >= len(lexemes) {
				return eofLex
			}
			return lexemes[idx]
		},
		consume: func() lexarch.Lexeme[TObservation, TToken, TTokenRole] {
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

	ctx := buildBaseContext(parser, errors, src, save, restore)

	// Context specific overrides
	ctx.lastLexingError = func() *lexarch.LexingError[TObservation, TToken] { return nil }
	ctx.SetLexerState = func(s TLexerState) {}

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
	lexer *lexarch.Lexer[TObservation, TState, TToken, TTokenRole],
	session *lexarch.LexerSession[TObservation, TState, TToken],
	errors *SyntaxErrors[TObservation],
) *ExecRuleContext[TObservation, TToken, TTokenRole, TState, TNodeKind] {
	src := rawSource[TObservation, TToken, TTokenRole]{
		peek: func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
			return lexarch.LexerPeek(lexer, session, n)
		},
		consume: func() lexarch.Lexeme[TObservation, TToken, TTokenRole] { return lexarch.LexerConsume(lexer, session) },
	}

	save := func() ParserSnapshot[TObservation, TState] {
		snap := session.Snapshot()
		return ParserSnapshot[TObservation, TState]{tokenIndex: snap.Position, lexerSnap: snap}
	}

	restore := func(c ParserSnapshot[TObservation, TState]) { session.RestoreSnapshot(c.lexerSnap) }

	ctx := buildBaseContext(parser, errors, src, save, restore)

	ctx.lastLexingError = func() *lexarch.LexingError[TObservation, TToken] { return session.GetLastError() }
	ctx.SetLexerState = func(state TState) { lexarch.LexerSessionSetState(session, state) }

	return ctx
}

func BuildExecRuleContextFromStreamingSession[
	TObservation cmp.Ordered,
	TState comparable,
	TToken comparable,
	TTokenRole comparable,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TState],
	lexer *lexarch.Lexer[TObservation, TState, TToken, TTokenRole],
	session *lexarch.StreamingLexerSession[TObservation, TState, TToken],
	errors *SyntaxErrors[TObservation],
) *ExecRuleContext[TObservation, TToken, TTokenRole, TState, TNodeKind] {
	src := rawSource[TObservation, TToken, TTokenRole]{
		peek: func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
			return lexarch.LexerPeekStreaming(lexer, session, n)
		},
		consume: func() lexarch.Lexeme[TObservation, TToken, TTokenRole] {
			return lexarch.LexerConsumeStreaming(lexer, session)
		},
	}

	save := func() ParserSnapshot[TObservation, TState] {
		snap := session.Snapshot()
		return ParserSnapshot[TObservation, TState]{tokenIndex: snap.AbsPos, streamingSnap: snap}
	}

	restore := func(c ParserSnapshot[TObservation, TState]) { session.RestoreSnapshot(c.streamingSnap) }

	ctx := buildBaseContext(parser, errors, src, save, restore)

	ctx.lastLexingError = func() *lexarch.LexingError[TObservation, TToken] { return session.GetLastError() }
	ctx.SetLexerState = func(state TState) { lexarch.StreamingLexerSessionSetState(session, state) }

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
) *ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	// Initialize Cores
	sCore := &skipCore[TTokenRole]{stack: make([]tokenSet[TTokenRole], 0)}
	if len(parser.defaultSkipRoles) > 0 {
		sCore.PushSkipRoles(parser.defaultSkipRoles...)
	}

	tStream := &tokenStream[TObservation, TToken, TTokenRole]{
		peekRaw:    src.peek,
		consumeRaw: src.consume,
		skipCore:   sCore,
		eofToken:   parser.eofToken,
	}

	rCore := &recoveryCore[TObservation, TToken, TTokenRole]{
		ts: tStream,
	}
	rCore.setDefaultRecovery(parser.eofToken)
	eCore := &errorCore[TObservation, TToken, TTokenRole]{sink: errors, ts: tStream}

	editor := &LSTEditor[TObservation, TToken, TTokenRole, TNodeKind]{
		created: make([]*SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind], 0),
	}

	selectCtx := &SelectRuleContext[TObservation, TToken, TTokenRole]{
		ts: tStream,
	}

	finalCtx := &FinalizationCtx[TObservation, TToken, TTokenRole, TNodeKind]{
		editor: editor,
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
		save:         saveFn,
		restore:      restoreFn,
		Select:       selectCtx,
		Finalization: finalCtx,
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

	return ctxPtr
}
