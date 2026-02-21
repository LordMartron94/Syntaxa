package syntaxa

/*
GrammarKind denotes the algebraic operation represented by a grammar node.

The grammar IR is intentionally minimal and purely structural. Each kind
corresponds to a fundamental grammar combinator found in EBNF/PEG-style
grammars. No semantic behavior, recovery policy, or execution state is
encoded at this layer.

Kinds:

  - GToken
    Atomic terminal symbol. Matches exactly one lexer token.

  - GConcat
    Sequential composition. All child grammars must match in order.

  - GChoice
    Alternation (union). Exactly one child grammar must match.

  - GRepeat
    Repetition operator. The single child grammar is matched multiple
    times according to Min/Max bounds.

  - GOptional
    Optional operator. Equivalent to GRepeat with Min=0 and Max=1,
    but preserved explicitly for clarity and tooling.

  - GEpsilon
    Empty production. Always succeeds without consuming input.

This algebra is closed and intentionally small. Any higher-level grammar
constructs (lists, blocks, separators, precedence rules, etc.) must compile
down into these primitives rather than extending the IR.

Execution semantics, error recovery, AST construction, and contextual logic
are handled by the parser engine, not by the grammar IR itself.
*/
//go:generate stringer -type GrammarKind
type GrammarKind uint8

const (
	GToken GrammarKind = iota + 1
	GConcat
	GChoice
	GRepeat
	GOptional
	GEpsilon
)

/*
Grammar represents a node in the declarative grammar intermediate
representation.

The grammar forms a tree of composable combinators describing the syntactic
structure of a language. It is intentionally free of execution behavior and
serves as a canonical, introspectable description of the language grammar.

Field semantics depend on Kind:

  - GToken
    Token holds the terminal token value to be matched.
    Children, Min, and Max are unused.

  - GConcat, GChoice
    Children contains the ordered sub-grammars.
    Token, Min, and Max are unused.

  - GRepeat
    Children must contain exactly one grammar node (the repeated body).
    Min specifies the minimum number of repetitions.
    Max specifies the maximum number of repetitions.

  - nil indicates unbounded repetition.

  - GOptional
    Children must contain exactly one grammar node.
    Token, Min, and Max are unused.

  - GEpsilon
    Represents the empty production.
    All other fields are unused.

Invariants:

  - GrammarKind determines which fields are semantically valid.
  - No grammar node may contain execution logic or recovery metadata.
  - Higher-level constructs must be expressed by composition, not by adding
    new GrammarKind values or special-case fields.

This structure enables grammar introspection, transformation, visualization,
pattern derivation, and parser execution from a single authoritative grammar
definition.
*/
type Grammar[TToken comparable] struct {
	GrammarID string

	/* Kind defines the grammar combinator represented by this node. */
	Kind GrammarKind

	/* Token is the terminal symbol matched by GToken nodes. */
	Token TToken

	/* Children holds sub-grammars for composite operators (Concat, Choice, Repeat, Optional). */
	Children []*Grammar[TToken]

	/* Min is the minimum repetition count for GRepeat nodes. */
	Min int

	/* Max is the maximum repetition count for GRepeat nodes. Nil denotes unbounded repetition. */
	Max *int

	RuleName string
}

/*
Token constructs an atomic terminal grammar node that matches exactly one token.
*/
func Token[TToken comparable](id string, tok TToken) *Grammar[TToken] {
	return &Grammar[TToken]{
		GrammarID: id,
		Kind:      GToken,
		Token:     tok,
	}
}

/*
Concat constructs a sequential composition of grammar nodes.

All children must match in order for the production to succeed.
At least one child must be provided.
*/
func Concat[TToken comparable](id string, children ...*Grammar[TToken]) *Grammar[TToken] {
	if len(children) == 0 {
		panic("Concat: at least one child grammar required")
	}

	return &Grammar[TToken]{
		GrammarID: id,
		Kind:      GConcat,
		Children:  children,
	}
}

/*
Choice constructs an alternation between grammar nodes.

Exactly one of the children must match.
At least one child must be provided.
*/
func Choice[TToken comparable](id string, children ...*Grammar[TToken]) *Grammar[TToken] {
	if len(children) == 0 {
		panic("Choice: at least one child grammar required")
	}

	return &Grammar[TToken]{
		GrammarID: id,
		Kind:      GChoice,
		Children:  children,
	}
}

/*
Repeat constructs a bounded or unbounded repetition grammar.

The body grammar is repeated between min and max times.

If max is nil, repetition is unbounded.

Invariants:
  - body must be non-nil
  - min must be >= 0
  - if max != nil, *max must be >= min
*/
func Repeat[TToken comparable](id string, body *Grammar[TToken], min int, max *int) *Grammar[TToken] {
	if body == nil {
		panic("Repeat: body grammar must not be nil")
	}
	if min < 0 {
		panic("Repeat: min must be >= 0")
	}
	if max != nil && *max < min {
		panic("Repeat: max must be >= min")
	}

	return &Grammar[TToken]{
		GrammarID: id,
		Kind:      GRepeat,
		Children:  []*Grammar[TToken]{body},
		Min:       min,
		Max:       max,
	}
}

/*
Optional constructs an optional grammar node.

Equivalent to Repeat(body, 0, 1) but preserved explicitly for semantic clarity.
*/
func Optional[TToken comparable](id string, body *Grammar[TToken]) *Grammar[TToken] {
	if body == nil {
		panic("Optional: body grammar must not be nil")
	}

	return &Grammar[TToken]{
		GrammarID: id,
		Kind:      GOptional,
		Children:  []*Grammar[TToken]{body},
	}
}

/*
Epsilon constructs an empty production grammar node.

This always succeeds without consuming input.
*/
func Epsilon[TToken comparable](id string) *Grammar[TToken] {
	return &Grammar[TToken]{
		GrammarID: id,
		Kind:      GEpsilon,
	}
}

/*
ZeroOrMore constructs an unbounded repetition with minimum 0.
*/
func ZeroOrMore[TToken comparable](id string, body *Grammar[TToken]) *Grammar[TToken] {
	return Repeat(id, body, 0, nil)
}

/*
OneOrMore constructs an unbounded repetition with minimum 1.
*/
func OneOrMore[TToken comparable](id string, body *Grammar[TToken]) *Grammar[TToken] {
	return Repeat(id, body, 1, nil)
}

/*
Exactly constructs a repetition that must occur exactly n times.
*/
func Exactly[TToken comparable](id string, body *Grammar[TToken], n int) *Grammar[TToken] {
	max := n
	return Repeat(id, body, n, &max)
}

/*
AtMost constructs a repetition with no minimum and a fixed maximum.
*/
func AtMost[TToken comparable](id string, body *Grammar[TToken], max int) *Grammar[TToken] {
	return Repeat(id, body, 0, &max)
}

/*
Between constructs a repetition bounded inclusively between min and max.
*/
func Between[TToken comparable](id string, body *Grammar[TToken], min, max int) *Grammar[TToken] {
	m := max
	return Repeat(id, body, min, &m)
}
