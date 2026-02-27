// Package rule provides a rule factory and combinators for building syntaxa parser rules.
//
// It extends the syntaxa core (which does not ship concrete rule implementations) with
// predefined, composable rule builders. Use this package for common grammar patterns:
// token expectations, sequences, lists, optional/alternation, repetition, and nesting.
//
// Key concepts:
//
//   - RuleBuilder: The main entry point. Create it with RuleBuilderCreate(tokenFormatter),
//     then use RuleBuilder.Token for token-level rules and RuleBuilder.Rule for composite rules.
//   - Token endpoint: Expect, ExpectVirtual, ExpectOneOf, List — match lexer tokens and optionally
//     build AST nodes.
//   - Rule endpoint: Sequence, Block, Optional, NOrMore, Nest, Root — combine rules and control
//     consumption and recovery.
//
// For behaviour not covered by these combinators, construct rules manually via
// syntaxa.ParserRuleCreate and the syntaxa grammar IR (Token, Concat, Choice, etc.).
//
// Integration: This package depends on syntaxa for ParserRule, Grammar, ExecRuleContext, and
// RuleResult; and on lexarch for Lexeme. Rules produced here are executed by the syntaxa
// parser engine; the factory only builds rule values and their grammar IR.
package rule
