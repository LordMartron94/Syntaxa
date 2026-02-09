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
RuleContext exposes the minimal, parser-agnostic capabilities
required by grammar rules.

The context abstracts the source of tokens (slice, lexer session,
streaming lexer, etc.) and enforces transactional parsing.

Transactional invariant:

  - Rules may consume freely while attempting to match
  - If a rule fails, the parser restores cursor and lexer state
  - Successful rules permanently commit consumption

Rules MUST NOT manually restore state on failure.
*/
type RuleContext[TObservation cmp.Ordered, TToken, TLexerState, TNodeKind comparable] struct {

	/* Peek returns the lexeme at lookahead distance n (0 = current). */
	Peek func(n int) lexarch.Lexeme[TObservation, TToken]

	/* Consume consumes and returns the current lexeme. */
	Consume func() lexarch.Lexeme[TObservation, TToken]

	/* Match consumes the current token if it matches one of the provided tokens. */
	Match func(tokens ...TToken) bool

	/* Save snapshots the current cursor state. */
	Save func() ParseCursor

	/* Restore restores a previously saved cursor state. */
	Restore func(ParseCursor)

	/* Report records a syntax error at a given source location. */
	Report func(line, column int, description string)

	/* SetLexerState switches the active lexer ruleset/state. */
	SetLexerState func(state TLexerState)

	/*
		CreateASTNode constructs a new AST node with all parser-owned
		defaults initialized.

		Rules are expected to:
		  - set NodeKind
		  - attach children / slots
		  - attach tokens
		  - optionally override spans or attributes
	*/
	CreateASTNode func() *SyntaxaASTNode[TObservation, TToken, TNodeKind]
}

/*
BuildRuleContextFromSlice constructs a RuleContext over a linear
lexeme slice.

This is suitable for simple, fully-materialized token streams.

The cursor pointer is owned by the caller and mutated as parsing
progresses.

Use cases:
  - offline parsing
  - unit tests
  - small inputs fully resident in memory
*/
func BuildRuleContextFromSlice[TObservation cmp.Ordered, TToken, TLexerState, TNodeKind comparable](
	parser *SyntaxaParser[TObservation, TToken, TNodeKind, TLexerState],
	lexemes []lexarch.Lexeme[TObservation, TToken],
	errors *SyntaxErrors,
	cursor *int,
) RuleContext[TObservation, TToken, TLexerState, TNodeKind] {

	ctx := buildBaseContext[TObservation, TToken, TLexerState](
		errors,
		newASTNodeFactory(parser),
	)

	ctx.Peek = func(n int) lexarch.Lexeme[TObservation, TToken] {
		return lexemes[*cursor+n]
	}

	ctx.Consume = func() lexarch.Lexeme[TObservation, TToken] {
		l := lexemes[*cursor]
		*cursor++
		return l
	}

	ctx.Match = func(tokens ...TToken) bool {
		for _, t := range tokens {
			if lexemes[*cursor].Token == t {
				*cursor++
				return true
			}
		}
		return false
	}

	ctx.Save = func() ParseCursor {
		return ParseCursor{tokenIndex: *cursor}
	}

	ctx.Restore = func(c ParseCursor) {
		*cursor = c.tokenIndex
	}

	return ctx
}

/*
BuildRuleContextFromLexerSession adapts a lexarch LexerSession
into a RuleContext.

This allows grammar rules to drive token consumption directly
from a stateful DFA-based lexer.

Use cases:
  - context-sensitive lexing
  - language modes (strings, comments, templates)
  - high-performance parsing
*/
func BuildRuleContextFromLexerSession[
	TObservation cmp.Ordered,
	TState comparable,
	TToken comparable,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TNodeKind, TState],
	lexer *lexarch.Lexer[TObservation, TState, TToken],
	session *lexarch.LexerSession[TObservation, TState],
	errors *SyntaxErrors,
) RuleContext[TObservation, TToken, TState, TNodeKind] {

	ctx := buildBaseContext[TObservation, TToken, TState](
		errors,
		newASTNodeFactory(parser),
	)

	ctx.Peek = func(n int) lexarch.Lexeme[TObservation, TToken] {
		lex, err := lexarch.LexerPeek(lexer, session, n)
		if err != nil {
			panic(err)
		}
		return lex
	}

	ctx.Consume = func() lexarch.Lexeme[TObservation, TToken] {
		lex, err := lexarch.LexerConsume(lexer, session)
		if err != nil {
			panic(err)
		}
		return lex
	}

	ctx.Match = func(tokens ...TToken) bool {
		lex, err := lexarch.LexerPeek(lexer, session, 0)
		if err != nil {
			panic(err)
		}

		for _, t := range tokens {
			if lex.Token == t {
				_, _ = lexarch.LexerConsume(lexer, session)
				return true
			}
		}
		return false
	}

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
BuildRuleContextFromStreamingSession adapts a streaming lexer
into a RuleContext.

This enables online parsing over large or unbounded inputs.

Use cases:
  - network protocols
  - large files
  - incremental feeds
*/
func BuildRuleContextFromStreamingSession[
	TObservation cmp.Ordered,
	TState comparable,
	TToken comparable,
	TNodeKind comparable,
](
	parser *SyntaxaParser[TObservation, TToken, TNodeKind, TState],
	lexer *lexarch.Lexer[TObservation, TState, TToken],
	session *lexarch.StreamingLexerSession[TObservation, TState],
	errors *SyntaxErrors,
) RuleContext[TObservation, TToken, TState, TNodeKind] {

	ctx := buildBaseContext[TObservation, TToken, TState](
		errors,
		newASTNodeFactory(parser),
	)

	ctx.Peek = func(n int) lexarch.Lexeme[TObservation, TToken] {
		lex, err := lexarch.LexerPeekStreaming(lexer, session, n)
		if err != nil {
			panic(err)
		}
		return lex
	}

	ctx.Consume = func() lexarch.Lexeme[TObservation, TToken] {
		lex, err := lexarch.LexerConsumeStreaming(lexer, session)
		if err != nil {
			panic(err)
		}
		return lex
	}

	ctx.Match = func(tokens ...TToken) bool {
		lex, err := lexarch.LexerPeekStreaming(lexer, session, 0)
		if err != nil {
			panic(err)
		}

		for _, t := range tokens {
			if lex.Token == t {
				_, _ = lexarch.LexerConsumeStreaming(lexer, session)
				return true
			}
		}
		return false
	}

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
type SyntaxaASTNode[TObservation cmp.Ordered, TToken, TNodeKind comparable] struct {

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

	Parent   *SyntaxaASTNode[TObservation, TToken, TNodeKind]
	Children []*SyntaxaASTNode[TObservation, TToken, TNodeKind]
	Slots    map[string]*SyntaxaASTNode[TObservation, TToken, TNodeKind]

	// ---------------------------------------------------------
	// TOKEN PRESERVATION
	// ---------------------------------------------------------

	Tokens []lexarch.Lexeme[TObservation, TToken]

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
type ParserRule[TObservation cmp.Ordered, TToken, TNodeKind, TLexerState comparable] func(
	ctx RuleContext[TObservation, TToken, TLexerState, TNodeKind],
) (*SyntaxaASTNode[TObservation, TToken, TNodeKind], bool)

/*
RuleSelector is client-owned logic that determines which rule
should be attempted at the current position.

Returning nil indicates that no rule applies and triggers recovery.
*/
type RuleSelector[TObservation cmp.Ordered, TToken, TNodeKind, TLexerState comparable] func(
	ctx RuleContext[TObservation, TToken, TLexerState, TNodeKind],
) ParserRule[TObservation, TToken, TNodeKind, TLexerState]

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
type SyntaxaParser[TObservation cmp.Ordered, TToken, TNodeKind, TLexerState comparable] struct {
	selectRule   RuleSelector[TObservation, TToken, TNodeKind, TLexerState]
	syncTokens   []TToken
	rootNodeKind TNodeKind

	nodeID uint64
}

/*
SyntaxaParserCreate constructs a new parser instance.
*/
func SyntaxaParserCreate[TObservation cmp.Ordered, TToken, TNodeKind, TLexerState comparable](
	selectRule RuleSelector[TObservation, TToken, TNodeKind, TLexerState],
	syncTokens []TToken,
	rootNodeKind TNodeKind,
) *SyntaxaParser[TObservation, TToken, TNodeKind, TLexerState] {
	return &SyntaxaParser[TObservation, TToken, TNodeKind, TLexerState]{
		selectRule:   selectRule,
		syncTokens:   syncTokens,
		rootNodeKind: rootNodeKind,
	}
}

func (p *SyntaxaParser[TObservation, TToken, TNodeKind, TLexerState]) nextNodeID() uint64 {
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
func SyntaxaParserParseASTSimple[TObservation cmp.Ordered, TToken, TNodeKind, TLexerState comparable](
	parser *SyntaxaParser[TObservation, TToken, TNodeKind, TLexerState],
	lexemes []lexarch.Lexeme[TObservation, TToken],
	eofToken TToken,
) (*SyntaxaASTNode[TObservation, TToken, TNodeKind], *SyntaxErrors) {

	root := &SyntaxaASTNode[TObservation, TToken, TNodeKind]{
		NodeKind: parser.rootNodeKind,
		Children: make([]*SyntaxaASTNode[TObservation, TToken, TNodeKind], 0),
	}

	errors := &SyntaxErrors{Errors: make([]SyntaxError, 0)}

	cursor := 0

	ctx := BuildRuleContextFromSlice(
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
func SyntaxaParserParseWithContext[TObservation cmp.Ordered, TToken, TNodeKind, TLexerState comparable](
	parser *SyntaxaParser[TObservation, TToken, TNodeKind, TLexerState],
	ctx RuleContext[TObservation, TToken, TLexerState, TNodeKind],
	root *SyntaxaASTNode[TObservation, TToken, TNodeKind],
	eofToken TToken,
) {
	parseWithContext(parser, ctx, root, eofToken)
}

// =============================================================
// AST NODE FACTORY (PRIVATE, SHARED)
// =============================================================

func newASTNodeFactory[TObservation cmp.Ordered, TToken, TNodeKind, TLexerState comparable](
	parser *SyntaxaParser[TObservation, TToken, TNodeKind, TLexerState],
) func() *SyntaxaASTNode[TObservation, TToken, TNodeKind] {

	return func() *SyntaxaASTNode[TObservation, TToken, TNodeKind] {
		return &SyntaxaASTNode[TObservation, TToken, TNodeKind]{
			ID:         parser.nextNodeID(),
			Children:   make([]*SyntaxaASTNode[TObservation, TToken, TNodeKind], 0),
			Slots:      make(map[string]*SyntaxaASTNode[TObservation, TToken, TNodeKind]),
			Tokens:     make([]lexarch.Lexeme[TObservation, TToken], 0),
			Attributes: make(map[string]any),
			Revision:   0,
		}
	}
}

// =============================================================
// CONTEXT BUILDERS (SHARED CORE)
// =============================================================

func buildBaseContext[TObservation cmp.Ordered, TToken, TLexerState, TNodeKind comparable](
	errors *SyntaxErrors,
	createNode func() *SyntaxaASTNode[TObservation, TToken, TNodeKind],
) RuleContext[TObservation, TToken, TLexerState, TNodeKind] {

	return RuleContext[TObservation, TToken, TLexerState, TNodeKind]{
		Report: func(line, column int, description string) {
			errors.Errors = append(errors.Errors, SyntaxError{
				Message: description,
				Line:    line,
				Column:  column,
			})
		},
		CreateASTNode: createNode,
	}
}

func recoverUntilSync[TObservation cmp.Ordered, TToken, TLexerState, TNodeKind comparable](
	ctx RuleContext[TObservation, TToken, TLexerState, TNodeKind],
	current lexarch.Lexeme[TObservation, TToken],
	syncTokens []TToken,
	eofToken TToken,
) bool {

	for {
		if current.Token == eofToken {
			return false
		}

		for _, t := range syncTokens {
			if current.Token == t {
				return true
			}
		}

		ctx.Consume()
		current = ctx.Peek(0)
	}
}

func parseWithContext[TObservation cmp.Ordered, TToken, TNodeKind, TLexerState comparable](
	parser *SyntaxaParser[TObservation, TToken, TNodeKind, TLexerState],
	ctx RuleContext[TObservation, TToken, TLexerState, TNodeKind],
	root *SyntaxaASTNode[TObservation, TToken, TNodeKind],
	eofToken TToken,
) {

	inErrorMode := false

	for {
		current := ctx.Peek(0)

		if current.Token == eofToken {
			return
		}

		rule := parser.selectRule(ctx)

		if rule == nil {
			if !inErrorMode {
				ctx.Report(
					current.StartLine,
					current.StartColumn,
					fmt.Sprintf("unexpected token %v", current.Token),
				)
				inErrorMode = true
			}

			if recoverUntilSync(
				ctx,
				current,
				parser.syncTokens,
				eofToken,
			) {
				inErrorMode = false
			}

			continue
		}

		snapshot := ctx.Save()

		node, ok := rule(ctx)
		if !ok {
			ctx.Restore(snapshot)
			continue
		}

		node.Parent = root

		if node.Start == 0 && len(node.Tokens) > 0 {
			node.Start = node.Tokens[0].Start
			node.End = node.Tokens[len(node.Tokens)-1].End
		}

		root.Children = append(root.Children, node)
	}
}
