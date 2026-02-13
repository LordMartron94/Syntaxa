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
)

// ============================================================
// Build lexer
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
	)

	return lexer, alloc
}

// ============================================================
// Pratt setup
// ============================================================

func buildPratt() *pratt.PrattParser[rune, TestToken, TokenRole, NodeKind, LexerState] {

	p := pratt.PrattParserCreate[rune, TestToken, TokenRole, NodeKind, LexerState]()

	p.RegisterPrefix(NumberTok, func(
		ctx syntaxa.ExecRuleContext[rune, TestToken, TokenRole, LexerState, NodeKind],
	) *syntaxa.SyntaxaASTNode[rune, TestToken, TokenRole, NodeKind] {

		lex := ctx.Consume()
		n := ctx.CreateASTNode()
		n.NodeKind = NumberExpr
		n.Tokens = append(n.Tokens, lex)
		return n
	})

	p.RegisterPrefix(IdentTok, func(
		ctx syntaxa.ExecRuleContext[rune, TestToken, TokenRole, LexerState, NodeKind],
	) *syntaxa.SyntaxaASTNode[rune, TestToken, TokenRole, NodeKind] {

		lex := ctx.Consume()
		n := ctx.CreateASTNode()
		n.NodeKind = IdentExpr
		n.Tokens = append(n.Tokens, lex)
		return n
	})

	p.RegisterPrefix(LParenTok, func(
		ctx syntaxa.ExecRuleContext[rune, TestToken, TokenRole, LexerState, NodeKind],
	) *syntaxa.SyntaxaASTNode[rune, TestToken, TokenRole, NodeKind] {

		ctx.Consume()
		e := p.ParseExpr(ctx, 0)
		ctx.Match(RParenTok)
		return e
	})

	parseBinary := func(
		ctx syntaxa.ExecRuleContext[rune, TestToken, TokenRole, LexerState, NodeKind],
		left *syntaxa.SyntaxaASTNode[rune, TestToken, TokenRole, NodeKind],
		rbp int,
	) *syntaxa.SyntaxaASTNode[rune, TestToken, TokenRole, NodeKind] {

		op := ctx.Consume()
		right := p.ParseExpr(ctx, rbp)

		n := ctx.CreateASTNode()
		n.NodeKind = BinaryExpr
		n.Children = []*syntaxa.SyntaxaASTNode[rune, TestToken, TokenRole, NodeKind]{left, right}
		n.Tokens = append(n.Tokens, op)

		left.Parent = n
		right.Parent = n
		return n
	}

	p.RegisterInfix(PlusTok, 10, pratt.Left, parseBinary)
	p.RegisterInfix(StarTok, 20, pratt.Left, parseBinary)

	p.RegisterPostfix(LParenTok, 30, func(
		ctx syntaxa.ExecRuleContext[rune, TestToken, TokenRole, LexerState, NodeKind],
		left *syntaxa.SyntaxaASTNode[rune, TestToken, TokenRole, NodeKind],
	) *syntaxa.SyntaxaASTNode[rune, TestToken, TokenRole, NodeKind] {

		ctx.Consume()
		ctx.Match(RParenTok)

		n := ctx.CreateASTNode()
		n.NodeKind = CallExpr
		n.Children = []*syntaxa.SyntaxaASTNode[rune, TestToken, TokenRole, NodeKind]{left}
		left.Parent = n
		return n
	})

	return p
}

// ============================================================
// Grammar
// ============================================================

func buildGrammar(
	p *pratt.PrattParser[rune, TestToken, TokenRole, NodeKind, LexerState],
) syntaxa.RuleSelector[rune, TestToken, TokenRole, LexerState, NodeKind] {

	expr := func(
		ctx syntaxa.ExecRuleContext[rune, TestToken, TokenRole, LexerState, NodeKind],
	) (*syntaxa.SyntaxaASTNode[rune, TestToken, TokenRole, NodeKind], bool) {

		n := p.ParseExpr(ctx, 0)
		return n, n != nil
	}

	stmt := rd.Sequence(
		expr,
		rd.TokenMatch[rune, TestToken, TokenRole, LexerState, NodeKind](SemicolonTok, StmtNode),
	)

	return func(ctx syntaxa.SelectRuleContext[rune, TestToken, TokenRole]) syntaxa.ParserRule[rune, TestToken, TokenRole, LexerState, NodeKind] {
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
		RootNode,
	)

	// ---------------- CLASSIC PATH ----------------

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

	root := &syntaxa.SyntaxaASTNode[rune, TestToken, TokenRole, NodeKind]{
		NodeKind: RootNode,
	}

	syntaxa.SyntaxaParserParseWithContext(
		parser,
		ctx,
		root,
		EOFToken,
	)

	validateAST(t, root, errors)

	// ---------------- STREAMING PATH ----------------

	pos := 0
	producer := func(dst []rune) (int, bool, error) {

		if pos >= len(source) {
			return 0, true, nil
		}

		n := min(len(dst), len(source)-pos)
		copy(dst, source[pos:pos+n])
		pos += n

		return n, pos >= len(source), nil
	}

	stream := lexarch.StreamingLexerSessionCreate(
		NormalState,
		producer,
		lexarch.NewlineDetectorRune(),
		2,
		64,
	)

	errors2 := &syntaxa.SyntaxErrors{}

	ctx2 := syntaxa.BuildExecRuleContextFromStreamingSession(
		parser,
		lexer,
		stream,
		errors2,
	)

	ctx2.PushSkipRoles(TriviaRole)

	root2 := &syntaxa.SyntaxaASTNode[rune, TestToken, TokenRole, NodeKind]{
		NodeKind: RootNode,
	}

	syntaxa.SyntaxaParserParseWithContext(
		parser,
		ctx2,
		root2,
		EOFToken,
	)

	validateAST(t, root2, errors2)
}

// ============================================================
// AST assertions
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

	ftesting.Assert(
		len(root.Children) == 1,
		"expected single statement",
		"single statement ok",
		t,
	)

	stmt := root.Children[0]
	expr := stmt.Children[0]

	ftesting.Assert(
		expr.NodeKind == BinaryExpr,
		"expected + at root",
		"binary root ok",
		t,
	)

	left := expr.Children[0]
	right := expr.Children[1]

	ftesting.Assert(
		left.NodeKind == CallExpr,
		"expected call on left",
		"call ok",
		t,
	)

	ftesting.Assert(
		right.NodeKind == BinaryExpr,
		"expected multiplication on right",
		"precedence ok",
		t,
	)
}
