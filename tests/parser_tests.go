package tests

import (
	"fmt"
	ftesting "foundation/testing"
	"testing"

	"autarch/pattern"
	"lexarch"
	"memcore"
	"memforge"

	"syntaxa"
	"syntaxa/pratt"
	"syntaxa/rd"
)

// ============================================================
// Lexer state
// ============================================================

type LexerState int

const (
	NormalState LexerState = iota + 1
)

// ============================================================
// Token roles
// ============================================================

type TokenRole int

const (
	TriviaRole TokenRole = iota + 1
	StructuralRole
)

// ============================================================
// Tokens
// ============================================================

type TestToken int

const (
	WhitespaceTok TestToken = iota + 1
	NumberTok
	IdentTok

	PlusTok
	StarTok
	LParenTok
	RParenTok
	SemicolonTok

	ErrorTok
	EOFToken
)

// ============================================================
// AST kinds
// ============================================================

type NodeKind int

const (
	RootNode NodeKind = iota + 1
	StmtNode
	BinaryExpr
	NumberExpr
	IdentExpr
	CallExpr

	ErrorNode
)

// ============================================================
// Lexer construction
// ============================================================

func buildTestLexer() (*lexarch.Lexer[rune, LexerState, TestToken, TokenRole], memcore.MarkRaw) {

	alloc := memforge.DynamicLinearAllocatorCreateFunction(
		uint64(memcore.KiloByte),
		func(c, n uint64) uint64 { return max(c*2, n) },
	)

	rules := lexarch.LexingRulesetCreate[rune, TestToken, TokenRole](
		lexarch.TokenResolutionStepPriority[TestToken],
	)

	number := pattern.Digit.Plus()
	ident := pattern.Lower.Plus()

	ws := pattern.AnyOf(
		pattern.Literal(' '),
		pattern.Literal('\n'),
		pattern.Literal('\t'),
	).Plus()

	rules.WithRule(ws, WhitespaceTok, TriviaRole)
	rules.WithRule(number, NumberTok, StructuralRole)
	rules.WithRule(ident, IdentTok, StructuralRole)

	rules.WithRule(pattern.Literal('+'), PlusTok, StructuralRole)
	rules.WithRule(pattern.Literal('*'), StarTok, StructuralRole)
	rules.WithRule(pattern.Literal('('), LParenTok, StructuralRole)
	rules.WithRule(pattern.Literal(')'), RParenTok, StructuralRole)
	rules.WithRule(pattern.Literal(';'), SemicolonTok, StructuralRole)

	lexer := lexarch.LexerCreate(
		map[LexerState]lexarch.LexingRuleset[rune, TestToken, TokenRole]{
			NormalState: *rules,
		},
		ErrorTok,
		EOFToken,
		func(sz, align uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(alloc, sz, align)
		},
		memcore.MegaByte,
		lexarch.RuneFormatterDefault(),
	)

	return lexer, alloc
}

// ============================================================
// Pratt setup (rewritten to use Editor)
// ============================================================

func buildPratt() *pratt.PrattParser[rune, TestToken, TokenRole, NodeKind, LexerState] {

	p := pratt.PrattParserCreate[rune, TestToken, TokenRole, NodeKind, LexerState]()

	// ---------------- PREFIX: Number ----------------

	p.RegisterPrefix(NumberTok, func(
		ctx syntaxa.ExecRuleContext[rune, TestToken, TokenRole, LexerState, NodeKind],
	) *syntaxa.SyntaxaASTNode[rune, TestToken, TokenRole, NodeKind] {

		lex := ctx.Consume()

		node := ctx.Editor.NewNode(NumberExpr)
		ctx.Editor.AddToken(node, lex)

		return node
	})

	// ---------------- PREFIX: Identifier ----------------

	p.RegisterPrefix(IdentTok, func(
		ctx syntaxa.ExecRuleContext[rune, TestToken, TokenRole, LexerState, NodeKind],
	) *syntaxa.SyntaxaASTNode[rune, TestToken, TokenRole, NodeKind] {

		lex := ctx.Consume()

		node := ctx.Editor.NewNode(IdentExpr)
		ctx.Editor.AddToken(node, lex)

		return node
	})

	// ---------------- PREFIX: Parenthesized ----------------

	p.RegisterPrefix(LParenTok, func(
		ctx syntaxa.ExecRuleContext[rune, TestToken, TokenRole, LexerState, NodeKind],
	) *syntaxa.SyntaxaASTNode[rune, TestToken, TokenRole, NodeKind] {

		ctx.Consume()
		expr := p.ParseExpr(ctx, 0)
		ctx.ConsumeIf(RParenTok)
		return expr
	})

	// ---------------- INFIX: Binary ----------------

	parseBinary := func(
		ctx syntaxa.ExecRuleContext[rune, TestToken, TokenRole, LexerState, NodeKind],
		left *syntaxa.SyntaxaASTNode[rune, TestToken, TokenRole, NodeKind],
		rbp int,
	) *syntaxa.SyntaxaASTNode[rune, TestToken, TokenRole, NodeKind] {

		op := ctx.Consume()
		right := p.ParseExpr(ctx, rbp)

		node := ctx.Editor.NewNode(BinaryExpr)
		ctx.Editor.AttachChild(node, left)
		ctx.Editor.AttachChild(node, right)
		ctx.Editor.AddToken(node, op)

		return node
	}

	p.RegisterInfix(PlusTok, 10, pratt.Left, parseBinary)
	p.RegisterInfix(StarTok, 20, pratt.Left, parseBinary)

	// ---------------- POSTFIX: Call ----------------

	p.RegisterPostfix(LParenTok, 30, func(
		ctx syntaxa.ExecRuleContext[rune, TestToken, TokenRole, LexerState, NodeKind],
		left *syntaxa.SyntaxaASTNode[rune, TestToken, TokenRole, NodeKind],
	) *syntaxa.SyntaxaASTNode[rune, TestToken, TokenRole, NodeKind] {

		ctx.Consume()
		ctx.ConsumeIf(RParenTok)

		node := ctx.Editor.NewNode(CallExpr)
		ctx.Editor.AttachChild(node, left)

		return node
	})

	return p
}

// ============================================================
// Grammar
// ============================================================

func buildGrammar(
	p *pratt.PrattParser[rune, TestToken, TokenRole, NodeKind, LexerState],
) syntaxa.RuleSelector[rune, TestToken, TokenRole, LexerState, NodeKind] {

	// ---------------- Expression (inner production) ----------------

	expr := func(
		ctx syntaxa.ExecRuleContext[rune, TestToken, TokenRole, LexerState, NodeKind],
	) (syntaxa.RuleResult[rune, TestToken, TokenRole, NodeKind], bool) {

		n := p.ParseExpr(ctx, 0)
		if n == nil {
			return rd.NoNode[rune, TestToken, TokenRole, NodeKind](), false
		}
		return rd.NodeResult(n), true
	}

	// ---------------- Statement (grammar root production) ----------------

	stmt := rd.TopLevel(
		rd.SequenceAs(
			StmtNode,
			expr,
			rd.Tok[rune, TestToken, TokenRole, LexerState, NodeKind](SemicolonTok),
		),
	)

	return func(
		ctx syntaxa.SelectRuleContext[rune, TestToken, TokenRole],
	) syntaxa.ParserRule[rune, TestToken, TokenRole, LexerState, NodeKind] {
		return stmt
	}
}

// ============================================================
// Integration test
// ============================================================

func TestSyntaxaIntegration(t *testing.T) {

	lexer, alloc := buildTestLexer()
	defer memforge.DynamicLinearAllocatorDestroy(alloc)
	defer lexarch.LexerClose(lexer)

	source := []rune("foo() + 2 * 3;")

	pr := buildPratt()
	selector := buildGrammar(pr)

	parser := syntaxa.SyntaxaParserCreate(
		selector,
		nil,
		nil,
		EOFToken,
		RootNode,
		ErrorNode,
		true,
	)

	session := lexarch.LexerSessionCreate(
		NormalState,
		source,
		lexarch.NewlineDetectorRune(),
	)

	errors := &syntaxa.SyntaxErrors{}

	ctx := syntaxa.BuildExecRuleContextFromLexerSession(
		parser,
		lexer,
		session,
		errors,
	)

	ctx.PushSkipRoles(TriviaRole)

	root := ctx.Editor.NewNode(RootNode)

	syntaxa.SyntaxaParserParseWithContext(
		parser,
		ctx,
		root,
	)

	dump := root.DebugDump(syntaxa.ASTDebugFormatter[
		rune, TestToken, TokenRole, NodeKind,
	]{
		FormatKind: func(k NodeKind) string {
			switch k {
			case RootNode:
				return "Root"
			case StmtNode:
				return "Stmt"
			case BinaryExpr:
				return "BinaryExpr"
			case NumberExpr:
				return "Number"
			case IdentExpr:
				return "Ident"
			case CallExpr:
				return "Call"
			case ErrorNode:
				return "Error"
			default:
				return fmt.Sprintf("Kind(%d)", k)
			}
		},

		FormatToken: func(l lexarch.Lexeme[rune, TestToken, TokenRole]) string {
			return string(l.Raw)
		},

		FormatAttribute: func(k string, v any) string {
			return fmt.Sprintf("%s=%v", k, v)
		},

		/* ───── visual options ───── */

		ShowTokens:     true,
		ShowAttributes: true,

		ShowByteSpan: true,
		ShowLineSpan: true,

		ShowNodeID:   true,
		ShowRevision: false,

		SlotPrefix: "@",

		/* ───── color hooks (disabled in tests) ───── */

		ColorKind:      nil,
		ColorToken:     nil,
		ColorSpan:      nil,
		ColorAttribute: nil,
	})

	fmt.Println("\n===== AST DEBUG DUMP =====")
	fmt.Println(dump)
	fmt.Println("=========================")

	validateAST(t, root, errors)
}

// ============================================================
// AST Assertions (SAFE ACCESSORS ONLY)
// ============================================================

func validateAST(
	t *testing.T,
	root *syntaxa.SyntaxaASTNode[rune, TestToken, TokenRole, NodeKind],
	errs *syntaxa.SyntaxErrors,
) {

	ftesting.Assert(
		!errs.HasErrors(),
		fmt.Sprintf("syntax errors: %+v", errs.Errors),
		"no syntax errors",
		t,
	)

	children := root.Children()

	ftesting.Assert(
		len(children) == 1,
		"expected single statement",
		"single statement ok",
		t,
	)

	stmt := children[0]
	stmtChildren := stmt.Children()

	expr := stmtChildren[0]

	ftesting.Assert(
		expr.Kind() == BinaryExpr,
		"expected + at root",
		"binary root ok",
		t,
	)

	exprChildren := expr.Children()

	left := exprChildren[0]
	right := exprChildren[1]

	ftesting.Assert(
		left.Kind() == CallExpr,
		"expected call on left",
		"call ok",
		t,
	)

	ftesting.Assert(
		right.Kind() == BinaryExpr,
		"expected multiplication on right",
		"precedence ok",
		t,
	)
}
