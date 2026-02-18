package syntaxa

import (
	"cmp"
	"lexarch"
)

type tokenSet[TToken comparable] map[TToken]struct{}

/*
SelectRuleContext exposes pure, non-mutating lookahead operations.

Used exclusively for:
  - rule selection
  - branching
  - precedence decisions
  - predictive parsing

Must NEVER mutate parser or lexer state.
*/
type SelectRuleContext[TObservation cmp.Ordered, TToken, TTokenRole comparable] struct {

	/* Peek returns the lexeme at lookahead distance n (0 = current). Skips roles. */
	Peek func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole]

	/* PeekRaw returns the lexeme at lookahead distance n without skipping. */
	PeekRaw func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole]

	/* PeekRange returns upcoming lexemes without consuming input. */
	PeekRange func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole]

	/* PeekRangeRaw returns upcoming lexemes without consuming input. Skips roles. */
	PeekRangeRaw func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole]

	/* Match reports whether current token matches any provided token. */
	Match func(tokens ...TToken) bool
}

/*
ExecRuleContext exposes transactional, mutating parsing operations.

Used exclusively once a grammar rule has been selected.

Transactional invariant:

  - Rules may consume freely while attempting to match
  - If a rule fails, cursor and lexer state are restored
  - Successful rules permanently commit consumption

Rules MUST NOT manually restore state on failure.
*/
type ExecRuleContext[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
] struct {
	SelectRuleContext[TObservation, TToken, TTokenRole]

	/* ============================================================
	   Consumption
	   ============================================================ */

	/*
		Consume consumes and returns the current lexeme.

		Skippable roles are automatically ignored.
	*/
	Consume func() lexarch.Lexeme[TObservation, TToken, TTokenRole]

	/* ConsumeIf consumes current token iff it matches any provided token. */
	ConsumeIf func(tokens ...TToken) bool

	/* Expect consumes token or reports+fails (or reports+returns false). */
	Expect func(token TToken, message string) bool

	/*
		ConsumeRaw consumes and returns the current lexeme without
		skipping any token roles.
	*/
	ConsumeRaw func() lexarch.Lexeme[TObservation, TToken, TTokenRole]

	/*
		ConsumeRange returns up to `n` upcoming lexemes while
		consuming input.

		Skips roles.
	*/
	ConsumeRange func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole]

	/*
		ConsumeRangeRaw returns up to `n` upcoming lexemes while
		consuming input.
	*/
	ConsumeRangeRaw func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole]

	/*
	   Try executes fn transactionally.

	   On failure:
	     - cursor is restored automatically
	     - returns false

	   On success:
	     - state is committed
	     - returns true
	*/
	Try func(fn func() bool) bool

	/* Optional attempts fn once, never fails */
	Optional func(fn func() bool) bool

	/* ZeroOrMore repeats while fn succeeds */
	ZeroOrMore func(fn func() bool)

	/* OneOrMore repeats at least once */
	OneOrMore func(fn func() bool) bool

	/*
		ExpectOneOf consumes one of provided tokens or reports error.
	*/
	ExpectOneOf func(tokens []TToken, message string) bool

	/* ============================================================
	   Transaction control
	   ============================================================ */

	/*
		save snapshots the current cursor state.
	*/
	save func() ParserSnapshot

	/*
		restore restores a previously saved cursor state.
	*/
	restore func(ParserSnapshot)

	/* ============================================================
	   Error handling
	   ============================================================ */

	/*
		Report records a syntax error at a given source location.
	*/
	Report func(line, column int, description string)

	reportLexerError func(line, column int, description string)

	getErrors func() *SyntaxErrors[TObservation]

	/*
		CreateErrorNode constructs a structured error AST node.
	*/
	CreateErrorNode func(message string) *SyntaxaASTNode[
		TObservation, TToken, TTokenRole, TNodeKind,
	]

	/* ============================================================
	   Lexer state control
	   ============================================================ */

	/*
		SetLexerState switches the active lexer ruleset/state.
	*/
	SetLexerState func(state TLexerState)

	/* ============================================================
	   AST construction
	   ============================================================ */

	/*
		Editor provides invariant-safe AST mutation and creation.
	*/
	Editor *ASTEditor[TObservation, TToken, TTokenRole, TNodeKind]

	/* ============================================================
	   Recovery management
	   ============================================================ */

	/*
		PushRecovery installs a new local synchronization token set.
	*/
	PushRecovery func(tokens ...TToken)

	/*
		PopRecovery removes the most recent synchronization set.
	*/
	PopRecovery func()

	/*
		currentRecovery returns the active synchronization tokens.
	*/
	currentRecovery func() tokenSet[TToken]

	/*
		PushSkipRoles installs a new local skippable role set.
	*/
	PushSkipRoles func(roles ...TTokenRole)

	/*
		PopSkipRoles removes the most recent skippable role set.
	*/
	PopSkipRoles func()

	currentSkips func() tokenSet[TTokenRole]

	LastLexingError func() *lexarch.LexingError[TObservation, TToken]
}

func (ctx *ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) selectCTX() SelectRuleContext[TObservation, TToken, TTokenRole] {
	return SelectRuleContext[TObservation, TToken, TTokenRole]{
		Peek:         ctx.Peek,
		PeekRaw:      ctx.PeekRaw,
		PeekRange:    ctx.PeekRange,
		PeekRangeRaw: ctx.PeekRangeRaw,
		Match:        ctx.Match,
	}
}

// =============================================================
// RAW SOURCE CORE
// =============================================================

type rawSource[TObs cmp.Ordered, TToken, TTokenRole comparable] struct {
	peek    func(int) lexarch.Lexeme[TObs, TToken, TTokenRole]
	consume func() lexarch.Lexeme[TObs, TToken, TTokenRole]
}

func attachRawSource[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	ctx *ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	src rawSource[TObservation, TToken, TTokenRole],
) {
	// ------------------------------------------------------------
	// Primitive access
	// ------------------------------------------------------------

	ctx.PeekRaw = src.peek
	ctx.ConsumeRaw = src.consume

	// ------------------------------------------------------------
	// Derived range helpers
	// ------------------------------------------------------------

	ctx.PeekRangeRaw = func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		if n <= 0 {
			return nil
		}

		out := make([]lexarch.Lexeme[TObservation, TToken, TTokenRole], 0, n)

		for i := 0; i < n; i++ {
			out = append(out, src.peek(i))
		}

		return out
	}

	ctx.ConsumeRangeRaw = func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		if n <= 0 {
			return nil
		}

		out := make([]lexarch.Lexeme[TObservation, TToken, TTokenRole], 0, n)

		for i := 0; i < n; i++ {
			out = append(out, src.consume())
		}

		return out
	}
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
	ctx := buildBaseContext(parser, errors)

	eofLex := lexarch.Lexeme[TObservation, TToken, TTokenRole]{
		Token: parser.eofToken,
	}

	attachRawSource(&ctx, rawSource[TObservation, TToken, TTokenRole]{
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
	})

	ctx.save = func() ParserSnapshot {
		return ParserSnapshot{tokenIndex: *cursor}
	}

	ctx.restore = func(c ParserSnapshot) {
		*cursor = c.tokenIndex
	}

	ctx.LastLexingError = func() *lexarch.LexingError[TObservation, TToken] {
		return nil
	}

	finalizeExecContext(&ctx, parser.eofToken, parser.defaultSkipRoles)
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
	ctx := buildBaseContext(parser, errors)

	attachRawSource(&ctx, rawSource[TObservation, TToken, TTokenRole]{
		peek: func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
			return lexarch.LexerPeek(lexer, session, n)
		},
		consume: func() lexarch.Lexeme[TObservation, TToken, TTokenRole] {
			return lexarch.LexerConsume(lexer, session)
		},
	})

	ctx.save = func() ParserSnapshot {
		snap := session.Snapshot()
		return ParserSnapshot{
			tokenIndex: snap.Position,
			aux:        snap,
		}
	}

	ctx.restore = func(c ParserSnapshot) {
		snap, ok := c.aux.(lexarch.LexerSessionSnapshot[TState])
		if !ok {
			panic("ParserSnapshot.aux: invalid lexer snapshot")
		}
		session.RestoreSnapshot(snap)
	}

	ctx.SetLexerState = func(state TState) {
		lexarch.LexerSessionSetState(session, state)
	}

	ctx.LastLexingError = func() *lexarch.LexingError[TObservation, TToken] {
		return session.GetLastError()
	}

	finalizeExecContext(&ctx, parser.eofToken, parser.defaultSkipRoles)
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
	ctx := buildBaseContext(parser, errors)

	attachRawSource(&ctx, rawSource[TObservation, TToken, TTokenRole]{
		peek: func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
			return lexarch.LexerPeekStreaming(lexer, session, n)
		},
		consume: func() lexarch.Lexeme[TObservation, TToken, TTokenRole] {
			return lexarch.LexerConsumeStreaming(lexer, session)
		},
	})

	ctx.save = func() ParserSnapshot {
		snap := session.Snapshot()
		return ParserSnapshot{
			tokenIndex: snap.AbsPos,
			aux:        snap,
		}
	}

	ctx.restore = func(c ParserSnapshot) {
		snap, ok := c.aux.(lexarch.StreamingLexerSessionSnapshot[TObservation, TState])
		if !ok {
			panic("ParserSnapshot.aux: invalid streaming snapshot")
		}
		session.RestoreSnapshot(snap)
	}

	ctx.SetLexerState = func(state TState) {
		lexarch.StreamingLexerSessionSetState(session, state)
	}

	ctx.LastLexingError = func() *lexarch.LexingError[TObservation, TToken] {
		return session.GetLastError()
	}

	finalizeExecContext(&ctx, parser.eofToken, parser.defaultSkipRoles)
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
) ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	recoveryStack := make([]tokenSet[TToken], 0)
	skipTokensStack := make([]tokenSet[TTokenRole], 0)

	editor := &ASTEditor[TObservation, TToken, TTokenRole, TNodeKind]{}
	editor.begin()

	return ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{

		// ====================================================
		// Error reporting
		// ====================================================

		Report: func(line, column int, description string) {
			errors.report(SyntaxError[TObservation]{
				Message: description,
				Line:    line,
				Column:  column,
			})
		},

		reportLexerError: func(line, column int, description string) {
			errors.report(SyntaxError[TObservation]{
				Message:         description,
				Line:            line,
				Column:          column,
				ProducedByLexer: true,
			})
		},

		getErrors: func() *SyntaxErrors[TObservation] {
			return errors
		},

		// ====================================================
		// AST system
		// ====================================================

		Editor: editor,

		CreateErrorNode: func(message string) *SyntaxaASTNode[
			TObservation, TToken, TTokenRole, TNodeKind,
		] {
			n := editor.NewNode(parser.errorNodeKind)
			editor.SetAttribute(n, "error", message)
			return n
		},

		// ====================================================
		// Recovery stack
		// ====================================================

		PushRecovery: func(tokens ...TToken) {
			set := make(tokenSet[TToken])
			for _, r := range tokens {
				set[r] = struct{}{}
			}
			recoveryStack = append(recoveryStack, set)
		},

		PopRecovery: func() {
			if len(recoveryStack) > 0 {
				recoveryStack = recoveryStack[:len(recoveryStack)-1]
			}
		},

		currentRecovery: func() tokenSet[TToken] {
			if len(recoveryStack) == 0 {
				return nil
			}
			return recoveryStack[len(recoveryStack)-1]
		},

		// ====================================================
		// Skip-role stack
		// ====================================================

		PushSkipRoles: func(roles ...TTokenRole) {
			set := make(tokenSet[TTokenRole])
			for _, r := range roles {
				set[r] = struct{}{}
			}
			skipTokensStack = append(skipTokensStack, set)
		},

		PopSkipRoles: func() {
			if len(skipTokensStack) > 0 {
				skipTokensStack = skipTokensStack[:len(skipTokensStack)-1]
			}
		},

		currentSkips: func() tokenSet[TTokenRole] {
			if len(skipTokensStack) == 0 {
				return nil
			}
			return skipTokensStack[len(skipTokensStack)-1]
		},
	}
}

func finalizeExecContext[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	ctx *ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	eofToken TToken,
	defaultSkipRoles []TTokenRole,
) {

	isEOF := func(l lexarch.Lexeme[TObservation, TToken, TTokenRole]) bool {
		return l.Token == eofToken
	}

	isSkipped := func(role TTokenRole) bool {
		_, skipped := ctx.currentSkips()[role]
		return skipped
	}

	// ----------------------------------------------------
	// Pure skip-aware peek (NON MUTATING)
	// ----------------------------------------------------

	ctx.Peek = func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		if n < 0 {
			panic("Peek: n must be >= 0")
		}

		seen := 0
		i := 0

		for {
			cur := ctx.PeekRaw(i)

			if isEOF(cur) {
				return cur
			}

			if !isSkipped(cur.Role) {
				if seen == n {
					return cur
				}
				seen++
			}

			i++
		}
	}

	ctx.PeekRange = func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		if n <= 0 {
			return nil
		}

		out := make([]lexarch.Lexeme[TObservation, TToken, TTokenRole], 0, n)

		i := 0
		for len(out) < n {
			cur := ctx.PeekRaw(i)

			if isEOF(cur) {
				break
			}

			if !isSkipped(cur.Role) {
				out = append(out, cur)
			}

			i++
		}

		return out
	}

	// ----------------------------------------------------
	// Actual consumption (ONLY place that mutates)
	// ----------------------------------------------------

	skipForward := func() {
		for {
			cur := ctx.PeekRaw(0)

			if isEOF(cur) || !isSkipped(cur.Role) {
				return
			}

			ctx.ConsumeRaw()
		}
	}

	ctx.Consume = func() lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		skipForward()
		return ctx.ConsumeRaw()
	}

	ctx.ConsumeRange = func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		if n <= 0 {
			return nil
		}

		out := make([]lexarch.Lexeme[TObservation, TToken, TTokenRole], 0, n)

		for len(out) < n {
			cur := ctx.PeekRaw(0)

			if isEOF(cur) {
				break
			}

			if !isSkipped(cur.Role) {
				out = append(out, cur)
			}

			ctx.ConsumeRaw()
		}

		return out
	}

	// ----------------------------------------------------
	// Helpers
	// ----------------------------------------------------

	ctx.ConsumeIf = func(tokens ...TToken) bool {
		skipForward()
		cur := ctx.PeekRaw(0)

		for _, t := range tokens {
			if cur.Token == t {
				ctx.ConsumeRaw()
				return true
			}
		}
		return false
	}

	ctx.Expect = func(token TToken, message string) bool {
		skipForward()
		cur := ctx.PeekRaw(0)

		if cur.Token == token {
			ctx.ConsumeRaw()
			return true
		}

		ctx.Report(cur.StartLine, cur.StartColumn, message)
		return false
	}

	ctx.Match = func(tokens ...TToken) bool {
		cur := ctx.Peek(0)
		for _, t := range tokens {
			if cur.Token == t {
				return true
			}
		}
		return false
	}

	// ----------------------------------------------------
	// Transactional helpers
	// ----------------------------------------------------

	ctx.Try = func(fn func() bool) bool {
		errors := ctx.getErrors()
		snap := ctx.save()

		errors.PushFrame()
		ok := fn()
		errors.PopFrame(ok)

		if ok {
			return true
		}

		ctx.restore(snap)
		return false
	}

	ctx.Optional = func(fn func() bool) bool {
		ctx.Try(fn)
		return true
	}

	ctx.ZeroOrMore = func(fn func() bool) {
		for {
			snap := ctx.save()
			if !fn() {
				ctx.restore(snap)
				return
			}
			if ctx.save().tokenIndex == snap.tokenIndex {
				panic("ZeroOrMore: rule succeeded without consuming input")
			}
		}
	}

	ctx.OneOrMore = func(fn func() bool) bool {
		if !ctx.Try(fn) {
			return false
		}
		for ctx.Try(fn) {
		}
		return true
	}

	ctx.ExpectOneOf = func(tokens []TToken, message string) bool {
		skipForward()
		cur := ctx.PeekRaw(0)

		for _, t := range tokens {
			if cur.Token == t {
				ctx.ConsumeRaw()
				return true
			}
		}

		ctx.Report(cur.StartLine, cur.StartColumn, message)
		return false
	}

	if len(defaultSkipRoles) > 0 {
		ctx.PushSkipRoles(defaultSkipRoles...)
	}
}
