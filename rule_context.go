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

func (ts *tokenStream[TObs, TToken, TTokenRole]) Match(tokens ...TToken) bool {
	cur := ts.Peek(0)
	for _, t := range tokens {
		if cur.Token == t {
			return true
		}
	}
	return false
}

func (ts *tokenStream[TObs, TToken, TTokenRole]) ConsumeIf(tokens ...TToken) bool {
	ts.skipForward()
	cur := ts.peekRaw(0)

	for _, t := range tokens {
		if cur.Token == t {
			ts.consumeRaw()
			return true
		}
	}
	return false
}

func (ts *tokenStream[TObs, TToken, TTokenRole]) Expect(token TToken, message string) (ok bool, line int, col int) {
	ts.skipForward()
	cur := ts.peekRaw(0)

	if cur.Token == token {
		ts.consumeRaw()
		return true, cur.StartLine, cur.StartColumn
	}

	return false, cur.StartLine, cur.StartColumn
}

func (ts *tokenStream[TObs, TToken, TTokenRole]) ExpectOneOf(tokens []TToken, message string) (ok bool, line int, col int) {
	ts.skipForward()
	cur := ts.peekRaw(0)

	for _, t := range tokens {
		if cur.Token == t {
			ts.consumeRaw()
			return true, cur.StartLine, cur.StartColumn
		}
	}

	return false, cur.StartLine, cur.StartColumn
}

// -------------------------------------------------------------

type transactionCore[TObs cmp.Ordered, TLexerState comparable] struct {
	save    func() ParserSnapshot[TObs, TLexerState]
	restore func(ParserSnapshot[TObs, TLexerState])
	errors  *errorCore[TObs]
}

func (tc *transactionCore[TObs, TLexerState]) Try(fn func() bool) bool {
	snap := tc.save()
	tc.errors.sink.PushFrame()
	ok := fn()
	tc.errors.sink.PopFrame(ok)

	if ok {
		return true
	}
	tc.restore(snap)
	return false
}

func (tc *transactionCore[TObs, TLexerState]) Optional(fn func() bool) bool {
	tc.Try(fn)
	return true
}

func (tc *transactionCore[TObs, TLexerState]) ZeroOrMore(fn func() bool) {
	for {
		snap := tc.save()
		if !fn() {
			tc.restore(snap)
			return
		}
		// Safety check to prevent infinite loops on zero-length matches
		if tc.save().tokenIndex == snap.tokenIndex {
			panic("ZeroOrMore: rule succeeded without consuming input")
		}
	}
}

func (tc *transactionCore[TObs, TLexerState]) OneOrMore(fn func() bool) bool {
	if !tc.Try(fn) {
		return false
	}
	for tc.Try(fn) {
	}
	return true
}

// -------------------------------------------------------------

type recoveryCore[TToken comparable] struct {
	stack []tokenSet[TToken]
}

func (rc *recoveryCore[TToken]) PushRecovery(tokens ...TToken) {
	set := make(tokenSet[TToken])
	for _, r := range tokens {
		set[r] = struct{}{}
	}
	rc.stack = append(rc.stack, set)
}

func (rc *recoveryCore[TToken]) PopRecovery() {
	if len(rc.stack) > 0 {
		rc.stack = rc.stack[:len(rc.stack)-1]
	}
}

func (rc *recoveryCore[TToken]) currentRecovery() tokenSet[TToken] {
	if len(rc.stack) == 0 {
		return nil
	}
	return rc.stack[len(rc.stack)-1]
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

type errorCore[TObs cmp.Ordered] struct {
	sink *SyntaxErrors[TObs]
}

func (ec *errorCore[TObs]) Report(line, column int, description string) {
	ec.sink.report(SyntaxError[TObs]{
		Message: description,
		Line:    line,
		Column:  column,
	})
}

func (ec *errorCore[TObs]) reportLexerError(line, column int, description string) {
	ec.sink.report(SyntaxError[TObs]{
		Message:         description,
		Line:            line,
		Column:          column,
		ProducedByLexer: true,
	})
}

type tokenSet[TToken comparable] map[TToken]struct{}

// =============================================================
// CONTEXTS
// =============================================================

/*
SelectRuleContext exposes pure, non-mutating lookahead operations.
*/
type SelectRuleContext[TObservation cmp.Ordered, TToken, TTokenRole comparable] struct {
	stream *tokenStream[TObservation, TToken, TTokenRole]
}

func (ctx SelectRuleContext[TObservation, TToken, TTokenRole]) Peek(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
	return ctx.stream.Peek(n)
}

func (ctx SelectRuleContext[TObservation, TToken, TTokenRole]) PeekRaw(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
	return ctx.stream.PeekRaw(n)
}

func (ctx SelectRuleContext[TObservation, TToken, TTokenRole]) Match(tokens ...TToken) bool {
	return ctx.stream.Match(tokens...)
}

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
	SelectRuleContext[TObservation, TToken, TTokenRole]

	/* The Token endpoint provides access to the lexer token stream. */
	Token *tokenStream[TObservation, TToken, TTokenRole]

	/* The Transaction endpoint provides access to compositional functions (i.e., Try). */
	Transaction *transactionCore[TObservation, TLexerState]

	/* The Recovery endpoint provides access to syntax error recovery functionality. */
	Recovery *recoveryCore[TToken]

	/* The Skip endpoint provides access to skip token functionality. */
	Skip *skipCore[TTokenRole]

	/* The Error endpoint provides access to syntax error reporting functionality. */
	Error *errorCore[TObservation]

	// Specific AST/State helpers
	Editor *ASTEditor[TObservation, TToken, TTokenRole, TNodeKind]

	/* SetLexerState alters the lexer state for languages with multiple lexer states. */
	SetLexerState func(TLexerState)

	lastLexingError func() *lexarch.LexingError[TObservation, TToken]
	createErrorNode func(string) *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind]
}

func (ctx *ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) selectCTX() SelectRuleContext[TObservation, TToken, TTokenRole] {
	return ctx.SelectRuleContext
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
) ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
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
) ExecRuleContext[TObservation, TToken, TTokenRole, TState, TNodeKind] {
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
) ExecRuleContext[TObservation, TToken, TTokenRole, TState, TNodeKind] {
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
) ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	// Initialize Cores
	sCore := &skipCore[TTokenRole]{stack: make([]tokenSet[TTokenRole], 0)}
	if len(parser.defaultSkipRoles) > 0 {
		sCore.PushSkipRoles(parser.defaultSkipRoles...)
	}

	rCore := &recoveryCore[TToken]{stack: make([]tokenSet[TToken], 0)}
	eCore := &errorCore[TObservation]{sink: errors}

	tStream := &tokenStream[TObservation, TToken, TTokenRole]{
		peekRaw:    src.peek,
		consumeRaw: src.consume,
		skipCore:   sCore,
		eofToken:   parser.eofToken,
	}

	txCore := &transactionCore[TObservation, TLexerState]{
		save:    saveFn,
		restore: restoreFn,
		errors:  eCore,
	}

	editor := &ASTEditor[TObservation, TToken, TTokenRole, TNodeKind]{}
	editor.begin()

	// Assemble Context
	return ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{
		SelectRuleContext: SelectRuleContext[TObservation, TToken, TTokenRole]{stream: tStream},
		Token:             tStream,
		Transaction:       txCore,
		Recovery:          rCore,
		Skip:              sCore,
		Error:             eCore,
		Editor:            editor,
		createErrorNode: func(message string) *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind] {
			n := editor.NewNode(parser.errorNodeKind)
			editor.SetAttribute(n, "error", message)
			return n
		},
	}
}
