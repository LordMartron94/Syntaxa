# Syntaxa

Generic, grammar-agnostic parsing engine for building ASTs from token streams with transactional execution, error recovery, and grammar introspection.

## Overview

Syntaxa provides a parsing runtime that executes **parser rules** over a token stream and builds an **abstract syntax tree (AST)**. It does not impose a specific parsing paradigm (LL, LR, PEG, etc.); instead, you supply rules built from a small **grammar IR** (Token, Concat, Choice, Repeat, Optional, Nest) and the engine handles transactional execution, rollback on failure, centralized error reporting, and recovery at configurable sync points.

Key features:

- **Generic type support**: Observation type, token type, token role, lexer state, and AST node kind are all type parameters.
- **Rule factory**: The `syntaxa/rule` package provides composable builders (Expect, Sequence, Block, Optional, NOrMore, Nest, Root, List) so most grammars can be expressed without touching the low-level IR.
- **Transactional execution**: Rule failure rolls back token consumption and AST mutations; only committed successes affect state.
- **Execution modes**: Normal (committed, report errors) vs Probe (speculative, no diagnostics) for optionals and choice.
- **Recovery**: Rules declare recovery tokens; on FailureError the engine consumes until a sync token (e.g. `}`, `)`) before continuing.
- **Grammar package**: Root grammar can be turned into a `GrammarPackage` with nullable/first/follow analysis for tooling and diagnostics.
- **Multiple input sources**: Slice of lexemes, lexarch `LexerSession`, or lexarch `StreamingLexerSession` via `BuildExecRuleContext*` and `SyntaxaParserParseWithContext`.

## Design Philosophy

- **Data and execution separate**: Rules are values; the parser is a function that takes a rule and a context. No methods on rules for core logic (except where required by interfaces).
- **Core vs factory**: The `syntaxa` package holds the engine, grammar IR, AST, and context types. The `syntaxa/rule` package is a preset factory for common patterns; advanced users can build rules manually with `ParserRuleCreate` and the grammar IR.
- **Explicit contracts**: Each rule has a `RuleContract` (MustConsume, MustReturnNode). The engine validates these on success and panics on violation.
- **Recovery as sync sets**: Recovery is defined per rule as a set of tokens; the engine does not interpret grammar structure for recovery beyond using those sets.

## Performance Characteristics

- **Hot path**: Rule execution is function calls and slice/index operations; no reflection. Token stream is either a slice index (slice context) or lexer calls (session/streaming).
- **Rollback**: Implemented by snapshot/restore of token position and trimming the AST editor’s created list; no per-node copy.
- **Grammar analysis**: Nullable/first/follow are computed once when building a `GrammarPackage`; fixed-point iteration over the grammar tree.
- **Allocations**: AST nodes and rule results are allocated during parsing; rule construction (factory or manual) allocates grammar nodes and closures.

## Integration

```text
syntaxa
├── syntaxa/rule   (rule factory: RuleBuilder, Token, Rule endpoints)
├── lexarch        (Lexeme, LexerSession, StreamingLexerSession for token input)
└── (caller)       (BuildExecRuleContextFromSlice / FromLexerSession / FromStreamingSession,
                    SyntaxaParserParseWithContext)
```

- **lexarch**: Supplies token streams (slice of lexemes or live lexer sessions). Syntaxa does not lex; it only consumes tokens.
- **syntaxa/rule**: Depends on `syntaxa` (ParserRule, Grammar, ExecRuleContext, RuleResult) and `lexarch` (Lexeme). Rules built there are executed by the syntaxa parser.

## Packages

### `syntaxa` (core)

- **Grammar IR**: `Grammar[TToken]` with kinds GToken, GConcat, GChoice, GRepeat, GOptional, GEpsilon, GNest. Constructors: `Token`, `Concat`, `Choice`, `Repeat`, `Optional`, `ZeroOrMore`, `OneOrMore`, `Nest`, etc.
- **Rules**: `ParserRule` (identity, executor, contract, recovery tokens, grammar). Created with `ParserRuleCreate`. Executed by the engine; never called directly by the user.
- **Context**: `ExecRuleContext` exposes `Token` (stream), `Recovery`, `Skip`, `Error`, `ExecuteRule`, `Editor`, `SetLexerState`, `Select`, `Finalization`. Built via `BuildExecRuleContextFromSlice`, `BuildExecRuleContextFromLexerSession`, or `BuildExecRuleContextFromStreamingSession`.
- **Parser**: `SyntaxaParser` holds the program rule, post-processor, EOF token, root/error node kinds, and options. `SyntaxaParserCreate` builds it; `SyntaxaParserParseWithContext(parser, ctx)` runs the parse.
- **AST**: `SyntaxaASTNode` (kind, parent, children, tokens, attributes, span). `ASTEditor` is the only way to create/mutate nodes during parsing (`NewNode`, `NewTransientNode`, `AttachChild`, `Detach`, etc.).
- **Grammar package**: `Grammar.ProducePackage(name, version)` returns `GrammarPackage` (entry rule, rules map, tokens, nests, analysis). Analysis contains nullable, first, and follow sets keyed by `NodeKey` (from `NodePath`).

### `syntaxa/rule` (rule factory)

- **RuleBuilder**: Entry point. `RuleBuilderCreate(tokenFormatter)` returns a builder with `Token` and `Rule` endpoints. Both share the same type parameters; rules from either can be composed.
- **Token endpoint**: `Expect`, `ExpectVirtual`, `ExpectOneOf`, `List` (open/element/separator/close, trailing mode, empty-list option).
- **Rule endpoint**: `Optional`, `OptionalPrefix`, `OptionalWhen`, `Required`, `Root`, `Sequence`, `Block`, `NOrMore`, `ZeroOrMore`, `OneOrMore`, `TransparentNOrMore`, `TransparentZeroOrMore`, `Nest`, `TransparentNest`, `RecoverSync`.
- **Types**: `Rule` and `Result` are aliases for `syntaxa.ParserRule` and `syntaxa.RuleResult`. `Lexeme` is an alias for `lexarch.Lexeme`.
- **TrailingSeparatorMode**: `TrailingForbidden`, `TrailingOptional`, `TrailingRequired` for list rules.

**Example (rule factory + parse from slice):**

```go
package main

import (
	"fmt"
	"lexarch"
	"syntaxa"
	"syntaxa/rule"
)

func main() {
	// 1) Define token and node kinds
	type TokenKind int
	const (
		TokLParen TokenKind = iota
		TokRParen
		TokComma
		TokNumber
	)
	type NodeKind int
	const (
		NodeList NodeKind = iota
		NodeItem
	)

	// 2) Rule builder and root rule
	tokenFmt := func(t TokenKind) string { return fmt.Sprintf("%d", t) }
	rb := rule.RuleBuilderCreate[rune, TokenKind, int, int, NodeKind](tokenFmt)

	item := rb.Token.Expect("ITEM", NodeItem, TokNumber)
	list := rb.Token.List(
		"LIST", TokLParen, TokNumber, TokComma, TokRParen,
		NodeList, NodeItem, true, rule.TrailingOptional,
	)
	root := rb.Rule.Root("ROOT", NodeList, false, list)

	// 3) Parser and context from a slice of lexemes
	eofTok := TokenKind(-1)
	parser := syntaxa.SyntaxaParserCreate(
		root, tokenFmt, lexarch.RuneFormatterDefault(), nil,
		eofTok, NodeList, NodeKind(-1), true,
	)

	lexemes := []lexarch.Lexeme[rune, TokenKind, int]{ /* ... */ }
	var cursor int
	errors := &syntaxa.SyntaxErrors[rune]{}
	ctx := syntaxa.BuildExecRuleContextFromSlice(parser, lexemes, errors, &cursor)

	rootNode, trace, err := syntaxa.SyntaxaParserParseWithContext(parser, ctx)
	if err != nil {
		fmt.Println(err)
		return
	}
	_ = rootNode
	_ = trace
}
```

**Example (grammar IR only, no factory):**

```go
// Build a rule that matches token T and wraps it in a node
grammar := syntaxa.Token("ID", myToken)
identity := syntaxa.RuleIdentity{
	RuleName:      "ExpectT",
	GrammarID:     "ID",
	ExpectedLabel: tokenFormatter(myToken),
}
exec := func(ctx *syntaxa.ExecRuleContext[...]) syntaxa.RuleResult[...] {
	peek := ctx.Token.Peek(0)
	if peek.Token != myToken {
		ctx.Error.ReportAt(...)
		return syntaxa.RuleResult[...]{Succeeded: false, Kind: syntaxa.FailureError}
	}
	lex := ctx.Token.Consume()
	node := ctx.Editor.NewNode(nodeKind)
	ctx.Editor.AddToken(node, lex)
	return syntaxa.RuleResult[...]{Node: node, Succeeded: true}
}
rule := syntaxa.ParserRuleCreate(identity, exec, syntaxa.RuleContract{MustConsume: true, MustReturnNode: true}, nil, grammar)
```

## Use Cases

- **Compilers and interpreters**: Parse source into an AST with clear node kinds and source spans.
- **DSLs and config parsers**: Use the rule factory for lists, optionals, and nesting without writing recursive-descent by hand.
- **Editors and IDEs**: Integrate with lexarch for streaming or slice-based input; use grammar package for nullable/first/follow in tooling.
- **Structured data parsers**: Combine token expectations and sequences for formats that are not fully context-free (e.g. indentation-sensitive) by driving the parser from a custom context.

## Safety Guidelines

1. **Rule contracts**: If a rule declares `MustConsume` or `MustReturnNode`, the engine panics on success when the contract is violated. Ensure your rule bodies match the contract you pass to `ParserRuleCreate` or the factory.
2. **Context lifetime**: Do not use an `ExecRuleContext` after the parse that built it; the editor and token stream are tied to that parse.
3. **Recovery tokens**: Recovery tokens should be tokens that appear at structural boundaries (e.g. closing delimiter). The engine consumes until it sees one of them; ensure they are correct for your grammar.
4. **Lexeme lifetime**: With slice-based context, `Lexeme.Raw` is a slice into the caller’s buffer; keep that buffer valid for the duration of the parse and any AST use that touches token text.
5. **Skip roles**: If you use `SetDefaultSkips`, skipped tokens are not visible to `Peek`/`Consume` in the logical stream; they are still consumed internally. Use for whitespace/comments.
6. **Probe vs normal**: Rules run in ExecutionProbe do not report errors and do not run recovery; use for optional or choice branches. First successful branch in a sequence commits; later failures are syntax errors (ExecutionNormal).

## Implementation Notes

- **Failure kinds**: `FailureNoMatch` means the production did not match (e.g. wrong token); no diagnostic is required. `FailureError` means a syntax error was detected; the engine may run recovery and then continue.
- **Fragments**: A rule can return a result with `IsFragment: true`. The factory uses this for `TransparentNOrMore` / `TransparentZeroOrMore` so that the caller (e.g. Sequence) unpacks the temporary container’s children instead of attaching the container itself.
- **Context boundary**: Grammar nodes created by `Rule.Root` (and similar) are marked as context boundaries so that `ProducePackage` collects them as named rules and analysis uses them for first/follow context.
