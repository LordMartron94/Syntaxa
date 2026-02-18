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
// RULE CONTEXT
// =============================================================

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

type tokenSet[TToken comparable] map[TToken]struct{}

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
