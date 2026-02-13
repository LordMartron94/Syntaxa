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
ParseCursor represents a snapshot of parser progress.

It is opaque by design and only meaningful to the RuleContext
implementation that created it.
*/
type ParseCursor struct {
	tokenIndex int
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

	/*
		ConsumeRaw consumes and returns the current lexeme without
		skipping any token roles.
	*/
	ConsumeRaw func() lexarch.Lexeme[TObservation, TToken, TTokenRole]

	/*
		ConsumeRange returns up to `n` upcoming lexemes while
		consuming input.
	*/
	ConsumeRange func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole]

	/* ============================================================
	   Transaction control
	   ============================================================ */

	/*
		Save snapshots the current cursor state.
	*/
	Save func() ParseCursor

	/*
		Restore restores a previously saved cursor state.
	*/
	Restore func(ParseCursor)

	/* ============================================================
	   Error handling
	   ============================================================ */

	/*
		Report records a syntax error at a given source location.
	*/
	Report func(line, column int, description string)

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
		CreateASTNode constructs a new AST node with all parser-owned
		defaults initialized.

		Rules are expected to:
		  - set NodeKind
		  - attach children or slots
		  - attach tokens
		  - optionally override spans or attributes
	*/
	CreateASTNode func() *SyntaxaASTNode[
		TObservation, TToken, TTokenRole, TNodeKind,
	]

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
		CurrentRecovery returns the active synchronization tokens.
	*/
	CurrentRecovery func() []TToken

	/*
		PushSkipRoles installs a new local skippable role set.
	*/
	PushSkipRoles func(roles ...TTokenRole)

	/*
		PopSkipRoles removes the most recent skippable role set.
	*/
	PopSkipRoles func()

	/*
		CurrentSkips returns the active skippable role set.
	*/
	CurrentSkips func() []TTokenRole
}

func (ctx *ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) selectCTX() SelectRuleContext[TObservation, TToken, TTokenRole] {
	return SelectRuleContext[TObservation, TToken, TTokenRole]{
		Peek:      ctx.Peek,
		PeekRaw:   ctx.PeekRaw,
		PeekRange: ctx.PeekRange,
		Match:     ctx.Match,
	}
}

/*
BuildExecRuleContextFromSlice constructs a RuleContext over a linear
lexeme slice.

This is suitable for simple, fully-materialized token streams.

The cursor pointer is owned by the caller and mutated as parsing
progresses.

Use cases:
  - offline parsing
  - unit tests
  - small inputs fully resident in memory
*/
func BuildExecRuleContextFromSlice[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TLexerState,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	lexemes []lexarch.Lexeme[TObservation, TToken, TTokenRole],
	errors *SyntaxErrors,
	cursor *int,
) ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	ctx := buildBaseContext[TObservation, TToken, TTokenRole, TLexerState](
		errors,
		newASTNodeFactory(parser),
	)

	// ---------------------------------------------------------
	// RAW ACCESS
	// ---------------------------------------------------------

	ctx.PeekRaw = func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		return lexemes[*cursor+n]
	}

	ctx.ConsumeRaw = func() lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		l := lexemes[*cursor]
		*cursor++
		return l
	}

	ctx.PeekRange = func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		return lexemes[*cursor : *cursor+n]
	}

	ctx.ConsumeRange = func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		l := lexemes[*cursor : *cursor+n]
		*cursor += n
		return l
	}

	// ---------------------------------------------------------
	// SKIP LOGIC
	// ---------------------------------------------------------

	isSkipped := func(role TTokenRole) bool {
		skips := ctx.CurrentSkips()
		for _, r := range skips {
			if r == role {
				return true
			}
		}
		return false
	}

	skipForward := func() {
		for {
			if *cursor >= len(lexemes) {
				return
			}
			if !isSkipped(lexemes[*cursor].Role) {
				return
			}
			*cursor++
		}
	}

	// ---------------------------------------------------------
	// LOGICAL ACCESS (AUTO-SKIPPING)
	// ---------------------------------------------------------

	ctx.Peek = func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		skipForward()
		return ctx.PeekRaw(n)
	}

	ctx.Consume = func() lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		skipForward()
		return ctx.ConsumeRaw()
	}

	ctx.Match = func(tokens ...TToken) bool {
		skipForward()

		for _, t := range tokens {
			if lexemes[*cursor].Token == t {
				*cursor++
				return true
			}
		}
		return false
	}

	// ---------------------------------------------------------
	// CURSOR CONTROL
	// ---------------------------------------------------------

	ctx.Save = func() ParseCursor {
		return ParseCursor{tokenIndex: *cursor}
	}

	ctx.Restore = func(c ParseCursor) {
		*cursor = c.tokenIndex
	}

	return ctx
}

/*
BuildExecRuleContextFromLexerSession adapts a lexarch LexerSession
into a RuleContext.

This allows grammar rules to drive token consumption directly
from a stateful DFA-based lexer.

Use cases:
  - context-sensitive lexing
  - language modes (strings, comments, templates)
  - high-performance parsing
*/
func BuildExecRuleContextFromLexerSession[
	TObservation cmp.Ordered,
	TState comparable,
	TToken comparable,
	TTokenRole comparable,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TState],
	lexer *lexarch.Lexer[TObservation, TState, TToken, TTokenRole],
	session *lexarch.LexerSession[TObservation, TState],
	errors *SyntaxErrors,
) ExecRuleContext[TObservation, TToken, TTokenRole, TState, TNodeKind] {

	ctx := buildBaseContext[TObservation, TToken, TTokenRole, TState](
		errors,
		newASTNodeFactory(parser),
	)

	// ---------------------------------------------------------
	// RAW ACCESS
	// ---------------------------------------------------------

	ctx.PeekRaw = func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		lex, err := lexarch.LexerPeek(lexer, session, n)
		if err != nil {
			panic(err)
		}
		return lex
	}

	ctx.ConsumeRaw = func() lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		lex, err := lexarch.LexerConsume(lexer, session)
		if err != nil {
			panic(err)
		}
		return lex
	}

	ctx.PeekRange = func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		lex, err := lexarch.LexerPeekRange(lexer, session, n)
		if err != nil {
			panic(err)
		}
		return lex
	}

	ctx.ConsumeRange = func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		lex, err := lexarch.LexerConsumeRange(lexer, session, n)
		if err != nil {
			panic(err)
		}
		return lex
	}

	// ---------------------------------------------------------
	// SKIP LOGIC
	// ---------------------------------------------------------

	isSkipped := func(role TTokenRole) bool {
		skips := ctx.CurrentSkips()
		for _, r := range skips {
			if r == role {
				return true
			}
		}
		return false
	}

	skipForward := func() {
		for {
			lex := ctx.PeekRaw(0)
			if !isSkipped(lex.Role) {
				return
			}
			ctx.ConsumeRaw()
		}
	}

	// ---------------------------------------------------------
	// LOGICAL ACCESS
	// ---------------------------------------------------------

	ctx.Peek = func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		skipForward()
		return ctx.PeekRaw(n)
	}

	ctx.Consume = func() lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		skipForward()
		return ctx.ConsumeRaw()
	}

	ctx.Match = func(tokens ...TToken) bool {
		skipForward()

		lex := ctx.PeekRaw(0)
		for _, t := range tokens {
			if lex.Token == t {
				ctx.ConsumeRaw()
				return true
			}
		}
		return false
	}

	// ---------------------------------------------------------
	// CURSOR CONTROL
	// ---------------------------------------------------------

	ctx.Save = func() ParseCursor {
		return ParseCursor{tokenIndex: session.Position()}
	}

	ctx.Restore = func(c ParseCursor) {
		session.SetPosition(c.tokenIndex)
	}

	ctx.SetLexerState = func(state TState) {
		lexarch.LexerSessionSetState(session, state)
	}

	return ctx
}

/*
BuildExecRuleContextFromStreamingSession adapts a streaming lexer
into a RuleContext.

This enables online parsing over large or unbounded inputs.

Use cases:
  - network protocols
  - large files
  - incremental feeds
*/
func BuildExecRuleContextFromStreamingSession[
	TObservation cmp.Ordered,
	TState comparable,
	TToken comparable,
	TTokenRole comparable,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TState],
	lexer *lexarch.Lexer[TObservation, TState, TToken, TTokenRole],
	session *lexarch.StreamingLexerSession[TObservation, TState],
	errors *SyntaxErrors,
) ExecRuleContext[TObservation, TToken, TTokenRole, TState, TNodeKind] {

	ctx := buildBaseContext[TObservation, TToken, TTokenRole, TState](
		errors,
		newASTNodeFactory(parser),
	)

	// ---------------------------------------------------------
	// RAW ACCESS
	// ---------------------------------------------------------

	ctx.PeekRaw = func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		lex, err := lexarch.LexerPeekStreaming(lexer, session, n)
		if err != nil {
			panic(err)
		}
		return lex
	}

	ctx.ConsumeRaw = func() lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		lex, err := lexarch.LexerConsumeStreaming(lexer, session)
		if err != nil {
			panic(err)
		}
		return lex
	}

	ctx.PeekRange = func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		lex, err := lexarch.LexerPeekRangeStreaming(lexer, session, n)
		if err != nil {
			panic(err)
		}
		return lex
	}

	ctx.ConsumeRange = func(n int) []lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		lex, err := lexarch.LexerConsumeRangeStreaming(lexer, session, n)
		if err != nil {
			panic(err)
		}
		return lex
	}

	// ---------------------------------------------------------
	// SKIP LOGIC
	// ---------------------------------------------------------

	isSkipped := func(role TTokenRole) bool {
		skips := ctx.CurrentSkips()
		for _, r := range skips {
			if r == role {
				return true
			}
		}
		return false
	}

	skipForward := func() {
		for {
			lex := ctx.PeekRaw(0)
			if !isSkipped(lex.Role) {
				return
			}
			ctx.ConsumeRaw()
		}
	}

	// ---------------------------------------------------------
	// LOGICAL ACCESS (AUTO-SKIPPING)
	// ---------------------------------------------------------

	ctx.Peek = func(n int) lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		skipForward()
		return ctx.PeekRaw(n)
	}

	ctx.Consume = func() lexarch.Lexeme[TObservation, TToken, TTokenRole] {
		skipForward()
		return ctx.ConsumeRaw()
	}

	ctx.Match = func(tokens ...TToken) bool {
		skipForward()

		lex := ctx.PeekRaw(0)
		for _, t := range tokens {
			if lex.Token == t {
				ctx.ConsumeRaw()
				return true
			}
		}
		return false
	}

	// ---------------------------------------------------------
	// CURSOR CONTROL
	// ---------------------------------------------------------

	ctx.Save = func() ParseCursor {
		return ParseCursor{tokenIndex: session.AbsPosition()}
	}

	ctx.Restore = func(c ParseCursor) {
		session.RestoreAbsolute(c.tokenIndex)
	}

	ctx.SetLexerState = func(state TState) {
		lexarch.StreamingLexerSessionSetState(session, state)
	}

	return ctx
}

// =============================================================
// AST NODE
// =============================================================

/*
SyntaxaASTNode represents a generic abstract syntax tree node.

It is a purely structural intermediate representation (IR).
All semantic meaning is defined externally via node kinds and
attached metadata.

The node is designed to support:
  - precise source mapping
  - incremental parsing and diffing
  - semantic annotation passes
  - format-preserving transformations
  - efficient tree navigation
*/
type SyntaxaASTNode[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind comparable] struct {

	/* Stable unique identifier for this node instance. */
	ID uint64

	/* Client-defined syntactic category of the node. */
	NodeKind TNodeKind

	// ---------------------------------------------------------
	// SOURCE MAPPING
	// ---------------------------------------------------------

	Start       int
	End         int
	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int

	// ---------------------------------------------------------
	// STRUCTURAL RELATIONSHIPS
	// ---------------------------------------------------------

	Parent   *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind]
	Children []*SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind]
	Slots    map[string]*SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind]

	// ---------------------------------------------------------
	// TOKEN PRESERVATION
	// ---------------------------------------------------------

	Tokens []lexarch.Lexeme[TObservation, TToken, TTokenRole]

	// ---------------------------------------------------------
	// METADATA
	// ---------------------------------------------------------

	Attributes map[string]any

	// ---------------------------------------------------------
	// INCREMENTAL SYSTEMS
	// ---------------------------------------------------------

	Revision uint64
}

// =============================================================
// RULES
// =============================================================

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
) (*SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind], bool)

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
// ERRORS
// =============================================================

/*
SyntaxError represents a single syntax error.
*/
type SyntaxError struct {
	Message string
	Line    int
	Column  int
}

func (e SyntaxError) Format() string {
	return fmt.Sprintf("syntax error at %d:%d: %s", e.Line, e.Column, e.Message)
}

/*
SyntaxErrors aggregates syntax errors produced during parsing.
*/
type SyntaxErrors struct {
	Errors []SyntaxError
}

func (s *SyntaxErrors) HasErrors() bool {
	return len(s.Errors) > 0
}

// =============================================================
// PARSER
// =============================================================

/*
SyntaxaParser is a grammar-agnostic parsing engine.

It imposes no parsing paradigm (LL, LR, Pratt, PEG, etc.).

Responsibilities:
  - transactional rule execution
  - centralized error recovery
  - AST assembly
*/
type SyntaxaParser[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable] struct {
	selectRule   RuleSelector[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
	rootNodeKind TNodeKind

	nodeID uint64
}

/*
SyntaxaParserCreate constructs a new parser instance.
*/
func SyntaxaParserCreate[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable](
	selectRule RuleSelector[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	rootNodeKind TNodeKind,
) *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState] {
	return &SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]{
		selectRule:   selectRule,
		rootNodeKind: rootNodeKind,
	}
}

func (p *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]) nextNodeID() uint64 {
	p.nodeID++
	return p.nodeID
}

/*
SyntaxaParserParseASTSimple constructs an AST from a linear
sequence of lexemes.

This is intended for non-streaming scenarios.

Advanced integrations should build custom RuleContexts and call
SyntaxaParserParseWithContext directly.
*/
func SyntaxaParserParseASTSimple[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	lexemes []lexarch.Lexeme[TObservation, TToken, TTokenRole],
	eofToken TToken,
) (*SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind], *SyntaxErrors) {

	root := &SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind]{
		NodeKind: parser.rootNodeKind,
		Children: make([]*SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind], 0),
	}

	errors := &SyntaxErrors{Errors: make([]SyntaxError, 0)}

	cursor := 0

	ctx := BuildExecRuleContextFromSlice(
		parser,
		lexemes,
		errors,
		&cursor,
	)

	parseWithContext(
		parser,
		ctx,
		root,
		eofToken,
	)

	return root, errors
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
func SyntaxaParserParseWithContext[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	ctx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	root *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
	eofToken TToken,
) {
	parseWithContext(parser, ctx, root, eofToken)
}

// =============================================================
// AST NODE FACTORY (PRIVATE, SHARED)
// =============================================================

func newASTNodeFactory[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
) func() *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind] {

	return func() *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind] {
		return &SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind]{
			ID:         parser.nextNodeID(),
			Children:   make([]*SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind], 0),
			Slots:      make(map[string]*SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind]),
			Tokens:     make([]lexarch.Lexeme[TObservation, TToken, TTokenRole], 0),
			Attributes: make(map[string]any),
			Revision:   0,

			Start:       -1,
			End:         -1,
			StartLine:   -1,
			StartColumn: -1,
			EndLine:     -1,
			EndColumn:   -1,
		}

	}
}

// =============================================================
// CONTEXT BUILDERS (SHARED CORE)
// =============================================================

func buildBaseContext[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable](
	errors *SyntaxErrors,
	createNode func() *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
) ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	recoveryStack := make([][]TToken, 0)
	skipTokensStack := make([][]TTokenRole, 0)

	return ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{

		Report: func(line, column int, description string) {
			errors.Errors = append(errors.Errors, SyntaxError{
				Message: description,
				Line:    line,
				Column:  column,
			})
		},

		CreateASTNode: createNode,

		CreateErrorNode: func(message string) *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind] {
			n := createNode()
			n.Attributes["error"] = message
			return n
		},

		PushRecovery: func(tokens ...TToken) {
			cp := make([]TToken, len(tokens))
			copy(cp, tokens)
			recoveryStack = append(recoveryStack, cp)
		},

		PopRecovery: func() {
			if len(recoveryStack) > 0 {
				recoveryStack = recoveryStack[:len(recoveryStack)-1]
			}
		},

		CurrentRecovery: func() []TToken {
			if len(recoveryStack) == 0 {
				return nil
			}
			return recoveryStack[len(recoveryStack)-1]
		},

		PushSkipRoles: func(roles ...TTokenRole) {
			cp := make([]TTokenRole, len(roles))
			copy(cp, roles)
			skipTokensStack = append(skipTokensStack, cp)
		},

		PopSkipRoles: func() {
			if len(skipTokensStack) > 0 {
				skipTokensStack = skipTokensStack[:len(skipTokensStack)-1]
			}
		},

		CurrentSkips: func() []TTokenRole {
			if len(skipTokensStack) == 0 {
				return nil
			}
			return skipTokensStack[len(skipTokensStack)-1]
		},
	}
}

func recoverWithContext[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable](
	ctx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	current lexarch.Lexeme[TObservation, TToken, TTokenRole],
	eofToken TToken,
) bool {

	sync := ctx.CurrentRecovery()
	if len(sync) == 0 {
		ctx.Consume()
		return current.Token != eofToken
	}

	for {
		if current.Token == eofToken {
			return false
		}

		for _, t := range sync {
			if current.Token == t {
				return true
			}
		}

		ctx.Consume()
		current = ctx.Peek(0)
	}
}

func parseWithContext[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable](
	parser *SyntaxaParser[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	execCtx ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	root *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
	eofToken TToken,
) {
	selectCtx := execCtx.selectCTX()
	defer finalizeASTSpans(root)

	lastCursor := -1

	for {
		current := execCtx.Peek(0)

		if current.Token == eofToken {
			return
		}

		start := execCtx.Save().tokenIndex

		rule := parser.selectRule(selectCtx)

		if rule == nil {

			execCtx.Report(
				current.StartLine,
				current.StartColumn,
				fmt.Sprintf("unexpected token %v", current.Token),
			)

			errNode := execCtx.CreateErrorNode("unexpected token")
			errNode.Tokens = append(errNode.Tokens, current)
			errNode.Parent = root
			root.Children = append(root.Children, errNode)

			if !recoverWithContext(execCtx, current, eofToken) {
				return
			}

			if start == lastCursor {
				execCtx.Consume()
			}

			lastCursor = start
			continue
		}

		snapshot := execCtx.Save()

		node, ok := rule(execCtx)

		if !ok {
			execCtx.Restore(snapshot)

			if !recoverWithContext(execCtx, current, eofToken) {
				return
			}

			if snapshot.tokenIndex == lastCursor {
				execCtx.Consume()
			}

			lastCursor = snapshot.tokenIndex
			continue
		}

		node.Parent = root
		root.Children = append(root.Children, node)
		lastCursor = -1
	}
}

func finalizeASTSpans[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind comparable](
	node *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
) {
	if node == nil {
		return
	}

	// First finalize children so their spans become available.
	for _, c := range node.Children {
		finalizeASTSpans(c)
	}
	for _, c := range node.Slots {
		finalizeASTSpans(c)
	}

	// If already explicitly set, preserve.
	if node.Start >= 0 && node.End >= 0 {
		return
	}

	// Prefer direct tokens.
	if len(node.Tokens) > 0 {
		first := node.Tokens[0]
		last := node.Tokens[len(node.Tokens)-1]

		node.Start = first.Start
		node.End = last.End

		node.StartLine = first.StartLine
		node.StartColumn = first.StartColumn
		node.EndLine = last.EndLine
		node.EndColumn = last.EndColumn

		return
	}

	// Otherwise infer from children+slots.
	minStart := -1
	maxEnd := -1

	minLine := -1
	minCol := -1
	maxLine := -1
	maxCol := -1

	consider := func(c *SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind]) {
		if c == nil || c.Start < 0 || c.End < 0 {
			return
		}

		if minStart < 0 || c.Start < minStart {
			minStart = c.Start
			minLine = c.StartLine
			minCol = c.StartColumn
		}

		if maxEnd < 0 || c.End > maxEnd {
			maxEnd = c.End
			maxLine = c.EndLine
			maxCol = c.EndColumn
		}
	}

	for _, c := range node.Children {
		consider(c)
	}
	for _, c := range node.Slots {
		consider(c)
	}

	if minStart >= 0 && maxEnd >= 0 {
		node.Start = minStart
		node.End = maxEnd

		node.StartLine = minLine
		node.StartColumn = minCol
		node.EndLine = maxLine
		node.EndColumn = maxCol
	}
}
