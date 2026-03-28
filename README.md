# Syntaxa

Generic parsing engine for building lossless syntax trees (LSTs) from token streams with transactional execution, error recovery, and grammar introspection. The core is **highly flexible but not paradigm-agnostic**: it uses **top-down recursive parsing**, so depending on rule factories it can handle PEG, LL(k), Pratt, and similar approaches, but it does **not** support LR or other bottom-up paradigms.

## Overview

Syntaxa provides a parsing runtime that executes **parser rules** over a token stream and builds a **lossless syntax tree (LST)**. You supply rules built from a small **grammar IR** (Token, Concat, Choice, Repeat, Optional, Nest); the engine is top-down recursive—with appropriate rule factories it can express PEG-, LL(k)-, or Pratt-style parsing, but not LR or other bottom-up paradigms. The engine handles transactional execution, rollback on failure, centralized error reporting, and recovery at configurable sync points.

**LST (lossless syntax tree)**: The tree shape is driven by semantic node creation; tokens and spans are preserved for tooling and diffing. The tree is grammar-informed but must not be "grammar-shaped"—no nodes that exist only due to combinators (use transparent rules for structural grouping). See **Node Creation Policy** below.

Key features:

- **Generic type support**: Observation type, token type, token role, lexer state, and LST node kind are all type parameters.
- **Rule factory**: The `syntaxa/rule` package provides composable builders (Expect, Sequence, Block, Optional, NOrMore, Nest, Root, List) so most grammars can be expressed without touching the low-level IR.
- **Transactional execution**: Rule failure rolls back token consumption and LST mutations; only committed successes affect state.
- **Execution modes**: Normal (committed, report errors) vs Probe (speculative, no diagnostics) for optionals and choice.
- **Recovery**: Rules declare recovery tokens; on FailureError the engine consumes until a sync token (e.g. `}`, `)`) before continuing.
- **Grammar package**: Root grammar is turned into a `GrammarPackage` (rules map, tokens, nests, path/label maps). The core CFG and nullable/first/follow analysis are not stored on the package; they are produced on demand via `syntaxa/lowering` (see below).
- **Multiple input sources**: Slice of lexemes, lexarch `LexerSession`, or lexarch `StreamingLexerSession` via `BuildExecRuleContext*` and `SyntaxaParserParseWithContext`.

## Design Philosophy

- **Data and execution separate**: Rules are values; the parser is a function that takes a rule and a context. No methods on rules for core logic (except where required by interfaces).
- **Core vs factory**: The `syntaxa` package holds the engine, grammar IR, LST, and context types. The `syntaxa/rule` package is a preset factory for common patterns; advanced users can build rules manually with `ParserRuleCreate` and the grammar IR.
- **Explicit contracts**: Each rule has a `RuleContract` (MustConsume, MustReturnNode). The engine validates these on success and panics on violation.
- **Recovery as sync sets**: Recovery is defined per rule as a set of tokens; the engine does not interpret grammar structure for recovery beyond using those sets.

## Performance Characteristics

- **Hot path**: Rule execution is function calls and slice/index operations; no reflection. Token stream is either a slice index (slice context) or lexer calls (session/streaming).
- **Rollback**: Implemented by snapshot/restore of token position and trimming the LST editor’s created list; no per-node copy.
- **Grammar analysis**: Nullable/first/follow are computed on demand via `syntaxa/lowering.GetAnalysis(pkg)`; the core package does not store them.
- **Allocations**: LST nodes and rule results are allocated during parsing; rule construction (factory or manual) allocates grammar nodes and closures.

## Integration

```text
syntaxa
├── syntaxa/rule   (rule factory: RuleBuilder, Token, Rule, Pratt endpoints)
├── lexarch        (Lexeme, LexerSession, StreamingLexerSession for token input)
└── (caller)       (BuildExecRuleContextFromSlice / FromLexerSession / FromStreamingSession,
                    SyntaxaParserParseWithContext)
```

- **lexarch**: Supplies token streams (slice of lexemes or live lexer sessions). Syntaxa does not lex; it only consumes tokens.
- **syntaxa/rule**: Depends on `syntaxa` (ParserRule, Grammar, ExecRuleContext, RuleResult) and `lexarch` (Lexeme). Rules built there are executed by the syntaxa parser.

## Packages

### `syntaxa` (core)

- **Grammar IR**: `Grammar[TToken, TNodeKind]` with kinds GToken, GConcat, GChoice, GRepeat, GOptional, GEpsilon, GNest. Optional `OutputNodeKind` for the LST node kind produced when the grammar is the root of a rule. Constructors: `Token`, `Concat`, `Choice`, `Repeat`, `Optional`, `ZeroOrMore`, `OneOrMore`, `Nest`, etc.
- **Rules**: `ParserRule` (identity, executor, contract, recovery tokens, grammar). Created with `ParserRuleCreate`. Executed by the engine; never called directly by the user.
- **Context**: `ExecRuleContext` exposes `Token` (stream), `Recovery`, `Skip`, `Error`, `ExecuteRule`, `Editor`, `SetLexerState`, `Select`, `Finalization`. Built via `BuildExecRuleContextFromSlice`, `BuildExecRuleContextFromLexerSession`, or `BuildExecRuleContextFromStreamingSession`.
- **Parser**: `SyntaxaParser` is built from a grammar package (with entry rule set). `SyntaxaParserCreate(grammarPackage, ..., getAnalysis)` takes a `GrammarPackage` and an optional `getAnalysis` (e.g. `lowering.GetAnalysis(grammarPackage)`) for nullable/first/follow when using Predict or Pratt; pass nil otherwise. `SyntaxaParserParseWithContext(parser, ctx)` runs the parse. For LST allocation tuning, `SetNodePoolPrefill(n)` pre-allocates reusable node buffers per context, and `SetNodePoolGrowFn(func(currentCap, needed int) int)` customizes growth when the pool runs empty (same shape as memforge `GrowthStrategy`: current free-list slice capacity, minimum required length after grow; return new target capacity, `>= needed`).
- **LST**: `SyntaxaLSTNode` (kind, parent, children, tokens, attributes, span). `LSTEditor` is the only way to create/mutate nodes during parsing (`NewNode`, `NewTransientNode`, `AttachChild`, `Detach`, etc.).
- **Grammar package**: `ProducePackage(rootGrammar, name, version, entryRule)` builds a `GrammarPackage` (entry rule ID, rules map, tokens, nests, path/label maps, and optionally the entry `ParserRule`). It does **not** store the core CFG (pattern/Contexta IR) or nullable/first/follow analysis; those are produced on demand via `syntaxa/lowering` (ToPatternGrammar, GetAnalysis, CompileEngine). Pass a non-nil `entryRule` when the package will be used to create a parser; pass nil for analysis-only use. Duplicate rule-root GrammarIDs panic.
- **Grammar traversal**: `Walk`, `WalkPre`, `WalkPost`, `WalkBreadth` for strategy-based walks; `GrammarWalkPreWithContext` for pre-order with inherited context (e.g. sync tokens, repeat nesting).

### `syntaxa/lowering`

On-demand lowering of a `GrammarPackage` into other representations. The core package does not produce or store these; call these when you need them.

- **ToPatternGrammar(root, additionalRules, rules)** → `(*pattern.Grammar, ruleNameToNodeKey, ruleNameToRecovery)`. Converts the syntaxa grammar tree into the Contexta/pattern CFG. Use for CFG debug dumps or as input to PDA compilation.
- **GetAnalysis(pkg)** → `*GrammarAnalysis`. Computes nullable, first, and follow sets keyed by `NodeKey`. Use when creating a parser (pass as `getAnalysis` to `SyntaxaParserCreate`) or when dumping analysis in the grammar package debugger.
- **CompileEngine(pkg, allocFn, config)** → `(*PDAEngine, error)`. Compiles the package into a DPDA or NPDA; uses `ToPatternGrammar` internally. Types `PDAEngine` and `NPDAConfig` (and presets `NPDAConfigSmall`, `NPDAConfigMedium`, `NPDAConfigLarge`) live in this package.
- **BuildStateGraph(pkg, tokenFormatter)** → `(*StateGraph, error)`. Generic state graph for editor backends (e.g. Editor IR). Types `StateGraph`, `Context`, `ContextMeta`, `Transition`, and `StackOp` are documented in the package.

### `syntaxa/rule` (rule factory)

- **RuleBuilder**: Entry point. `RuleBuilderCreate(tokenFormatter)` returns a builder with `Token`, `Rule`, and `Pratt` endpoints. All share the same type parameters; rules from any can be composed.
- **Token endpoint**: `Expect`, `ExpectVirtual`, `ExpectOneOf`, `List` (open/element/separator/close, trailing mode, empty-list option).
- **Rule endpoint**: `Optional`, `OptionalPrefix`, `OptionalWhen`, `OptionalSuffix`, `Predict`, `Required`, `Root`, `Sequence`, `Block`, `NOrMore`, `ZeroOrMore`, `OneOrMore`, `TransparentNOrMore`, `TransparentZeroOrMore`, `Nest`, `TransparentNest`, `RecoverSync`. `OptionalSuffix` runs a rule then optionally consumes a suffix token and wraps the result. `Predict` runs a rule only when a lookahead predicate holds (false yields FailureNoMatch for Choice); use to resolve prefix overlap.
- **Pratt endpoint**: `Expression(grammarID, PrattConfig)` — precedence-climbing expression rule. Config holds `Primary` (atom rule), `PrefixOps` (token, right binding power, node kind), `InfixOps` (token, left/right binding power, node kind), and optional `RecoveryTokens`. Binding power: higher = tighter binding; left-assoc uses `RightBP < LeftBP`, right-assoc uses `RightBP = LeftBP`. Returns a Rule that composes with Sequence, Choice, etc.
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
	grammarPkg := syntaxa.ProducePackage(root.GetGrammar(), nil, "app", "0.0.0", &root)
	registry := rb.GetRegistry()

	// 3) Parser and context from a slice of lexemes
	eofTok := TokenKind(-1)
	parser := syntaxa.SyntaxaParserCreate(
		&grammarPkg, registry, tokenFmt, lexarch.RuneFormatterDefault(), nil,
		eofTok, NodeList, NodeKind(-1), true, nil, // getAnalysis nil unless using Predict/Pratt
	)

	lexemes := []lexarch.Lexeme[rune, TokenKind, int]{ /* ... */ }
	var cursor int
	errors := &syntaxa.SyntaxErrors[rune]{}
	ctx := syntaxa.BuildExecRuleContextFromSlice(parser, lexemes, errors, &cursor, nil, nil)

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
rule := syntaxa.ParserRuleCreate(identity, exec, syntaxa.RuleContract{MustConsume: true, MustReturnNode: true}, nil, grammar, nil)
```

## Use Cases

- **Compilers and interpreters**: Parse source into an LST with clear node kinds and source spans.
- **DSLs and config parsers**: Use the rule factory for lists, optionals, and nesting without writing recursive-descent by hand.
- **Editors and IDEs**: Integrate with lexarch for streaming or slice-based input; use grammar package for nullable/first/follow in tooling.
- **Structured data parsers**: Combine token expectations and sequences for formats that are not fully context-free (e.g. indentation-sensitive) by driving the parser from a custom context.

## Safety Guidelines

1. **Rule contracts**: If a rule declares `MustConsume` or `MustReturnNode`, the engine panics on success when the contract is violated. Ensure your rule bodies match the contract you pass to `ParserRuleCreate` or the factory.
2. **Context lifetime**: Do not use an `ExecRuleContext` after the parse that built it; the editor and token stream are tied to that parse.
3. **Recovery tokens**: Recovery tokens should be tokens that appear at structural boundaries (e.g. closing delimiter). The engine consumes until it sees one of them; ensure they are correct for your grammar.
4. **Lexeme lifetime**: With slice-based context, `Lexeme.Raw` is a slice into the caller’s buffer; keep that buffer valid for the duration of the parse and any LST use that touches token text.
5. **Skip roles**: If you use `SetDefaultSkips`, skipped tokens are not visible to `Peek`/`Consume` in the logical stream; they are still consumed internally. Use for whitespace/comments.
6. **Probe vs normal**: Rules run in ExecutionProbe do not report errors and do not run recovery; use for optional or choice branches. First successful branch in a sequence commits; later failures are syntax errors (ExecutionNormal).

## Implementation Notes

- **Failure kinds**: `FailureNoMatch` means the production did not match (e.g. wrong token); no diagnostic is required. `FailureError` means a syntax error was detected; the engine may run recovery and then continue.
- **Fragments**: A rule can return a result with `IsFragment: true`. The factory uses this for `TransparentNOrMore` / `TransparentZeroOrMore` so that the caller (e.g. Sequence) unpacks the temporary container’s children instead of attaching the container itself.
- **Context boundary**: Grammar nodes created by `Rule.Root` (and similar) are marked as context boundaries so that `ProducePackage` collects them as named rules and analysis uses them for first/follow context.

## Node Creation Policy

- **Every non-transparent rule that creates a node must correspond to a domain/semantic construct.** Do not create nodes for grammar glue (e.g. a wrapper that exists only because of Nest + Sequence).
- Use **TransparentSequence**, **TransparentNest**, **TransparentNOrMore** when the grouping is structural only (no extra semantic node).
- Prune only when the node adds no semantic distinction, has exactly one meaningful child, or exists only due to Sequence/Nest layering. Do not over-prune: preserve anchors for incremental diffing and transforms.
