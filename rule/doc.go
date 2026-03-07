// Package rule provides a rule factory and combinators for building syntaxa parser rules.
//
// It extends the syntaxa core (which does not ship concrete rule implementations) with
// predefined, composable rule builders. Use this package for common grammar patterns:
// token expectations, sequences, lists, optional/alternation, repetition, nesting, and
// Pratt-style expression parsing.
//
// Key concepts:
//
//   - RuleBuilder: The main entry point. Create it with RuleBuilderCreate(tokenFormatter),
//     then use RuleBuilder.Token for token-level rules, RuleBuilder.Rule for composite rules,
//     and RuleBuilder.Pratt for precedence-climbing expression rules.
//   - Token endpoint: Expect, ExpectVirtual, ExpectOneOf, ExpectPair, List — match lexer tokens and optionally
//     build LST nodes. ExpectPair expects two tokens in sequence and creates one node with both attached.
//   - Rule endpoint: Sequence, Block, Optional, OptionalPrefix, OptionalWhen, OptionalSuffix, Required, NOrMore, Nest, Root, PredictWithLookahead, PredictLookahead — combine rules and control
//     consumption and recovery. OptionalSuffix runs a rule and, if the next token matches, consumes it and wraps the result in a new node.
//     PredictWithLookahead runs a rule only when a lookahead predicate returns true and optionally records lookaheads on the grammar; PredictLookahead takes a slice of (Offset, Expected) and runs the rule when all match. Use to resolve prefix overlap in Choice.
//   - Pratt endpoint: Expression — build one expression rule from a primary rule (atoms) and
//     prefix/infix operator tables with binding powers. Primary is the atom (e.g. literal, identifier,
//     parenthesized expression). PrefixOps and InfixOps define tokens and precedence; LeftBP/RightBP
//     encode associativity (e.g. RightBP < LeftBP for left-associative). RecoveryTokens let the engine
//     resync at expression boundaries. The grammar IR for a Pratt rule approximates "how an expression
//     can start" (primary or prefix token) for tooling; the full precedence structure is defined at runtime.
//
// For behaviour not covered by these combinators, construct rules manually via
// syntaxa.ParserRuleCreate and the syntaxa grammar IR (Token, Concat, Choice, etc.).
//
// Integration: This package depends on syntaxa for ParserRule, Grammar, ExecRuleContext, and
// RuleResult; and on lexarch for Lexeme. Rules produced here are executed by the syntaxa
// parser engine; the factory only builds rule values and their grammar IR.
package rule
