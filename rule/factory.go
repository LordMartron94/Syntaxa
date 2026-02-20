package rule

import (
	"cmp"
	"fmt"
	"lexarch"
	"slices"
	"strings"
	"syntaxa"
)

// ------------------------------------------------------------- TYPE ALIASES

type Rule[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] = syntaxa.ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]

type Result[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind comparable] = syntaxa.RuleResult[TObservation, TToken, TTokenRole, TNodeKind]

type Lexeme[TObservation cmp.Ordered, TToken, TTokenRole comparable] = lexarch.Lexeme[TObservation, TToken, TTokenRole]

// ------------------------------------------------------------- ENDPOINTS

type tokenEndpoint[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	sharedCore *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
}

/*
Expect matches a single token.

On success it creates a node of the given kind and assigns the lexeme as value.
On failure it reports a syntax error.
*/
func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Expect(
	grammarID string,
	outputNodeKind TNodeKind,
	token TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return t.expectCore("Expect", grammarID, outputNodeKind, true, token)
}

/*
ExpectVirtual matches a single token.

On success it does not create a node.
On failure it reports a syntax error.
*/
func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) ExpectVirtual(
	grammarID string,
	token TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	var zeroKind TNodeKind
	return t.expectCore("ExpectVirtual", grammarID, zeroKind, false, token)
}

/*
ExpectOneOf matches one of several tokens.

On success it creates a node of the given kind and assigns the lexeme as value.
On failure it reports a syntax error.
*/
func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) ExpectOneOf(
	grammarID string,
	outputNodeKind TNodeKind,
	tokens ...TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return t.expectCore("ExpectOneOf", grammarID, outputNodeKind, true, tokens...)
}

func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) expectCore(
	ruleName string,
	grammarID string,
	outputNodeKind TNodeKind,
	addNode bool,
	tokens ...TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := t.sharedCore.createRuleName(ruleName, grammarID)

	rule := func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
		peeked := ctx.Token.Peek(0)
		if slices.Contains(tokens, peeked.Token) {
			value := ctx.Token.Consume()

			if addNode {
				node := ctx.Editor.NewNode(outputNodeKind)
				ctx.Editor.AddToken(node, value)

				return t.sharedCore.buildSuccessRuleResult(node)
			} else {
				return t.sharedCore.buildSuccessRuleResult(nil)
			}

		}

		ctx.Error.ReportAt(
			peeked,
			fmt.Sprintf(
				"unexpected %s, wanted one of %s",
				t.sharedCore.tokenFormatter(peeked.Token),
				t.sharedCore.formatTokensAsList(", ", tokens...),
			),
		)

		return t.sharedCore.buildFailureRuleResult(nil)
	}

	if addNode {
		return t.sharedCore.constructStructuralRule(
			name,
			grammarID,
			rule,
			nil,
		)
	} else {
		return t.sharedCore.constructSkippingRule(
			name,
			grammarID,
			rule,
			nil,
		)
	}
}

/*
List matches a list-like grammar:

	OPEN ( ELEMENT ( SEP ELEMENT )* [SEP]? ) CLOSE

It automatically handles recovery.

If allowEmptyList is true, OPEN CLOSE is valid.
If optionalTrailingSeparator is true, the final separator may appear before CLOSE.
*/
func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) List(
	grammarID string,
	listOpenToken, elementToken, separatorToken, listEndToken TToken,
	listNodeKind, elementNodeKind TNodeKind,
	allowEmptyList, optionalTrailingSeparator bool,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := t.sharedCore.createRuleName("List", grammarID)

	recovery := []TToken{listEndToken}

	return t.sharedCore.constructStructuralRule(
		name,
		grammarID,
		func(ctx *syntaxa.ExecRuleContext[
			TObservation, TToken, TTokenRole, TLexerState, TNodeKind,
		]) Result[TObservation, TToken, TTokenRole, TNodeKind] {

			peek := ctx.Token.Peek(0)
			if peek.Token != listOpenToken {
				ctx.Error.ReportAt(peek, fmt.Sprintf(
					"unexpected %s, expected %s",
					t.sharedCore.tokenFormatter(peek.Token),
					t.sharedCore.tokenFormatter(listOpenToken),
				))
				return t.sharedCore.buildFailureRuleResult(nil)
			}

			ctx.Token.Consume()

			node := ctx.Editor.NewNode(listNodeKind)

			peek = ctx.Token.Peek(0)
			if peek.Token == listEndToken {
				if !allowEmptyList {
					ctx.Error.ReportAt(peek, "empty list not allowed")
				}
				ctx.Token.Consume()
				return t.sharedCore.buildSuccessRuleResult(node)
			}

			for {
				peek = ctx.Token.Peek(0)
				if peek.Token != elementToken {
					ctx.Error.ReportAt(peek, fmt.Sprintf(
						"unexpected %s, expected %s",
						t.sharedCore.tokenFormatter(peek.Token),
						t.sharedCore.tokenFormatter(elementToken),
					))
					return t.sharedCore.buildFailureRuleResult(nil)
				}

				value := ctx.Token.Consume()
				elem := ctx.Editor.NewNode(elementNodeKind)
				ctx.Editor.AddToken(elem, value)
				ctx.Editor.AttachChild(node, elem)

				peek = ctx.Token.Peek(0)

				if peek.Token == separatorToken {
					ctx.Token.Consume()

					peek = ctx.Token.Peek(0)
					if peek.Token == listEndToken && optionalTrailingSeparator {
						ctx.Token.Consume()
						return t.sharedCore.buildSuccessRuleResult(node)
					}

					continue
				}

				if peek.Token == listEndToken {
					ctx.Token.Consume()
					return t.sharedCore.buildSuccessRuleResult(node)
				}

				ctx.Error.ReportAt(peek, fmt.Sprintf(
					"unexpected %s, expected %s or %s",
					t.sharedCore.tokenFormatter(peek.Token),
					t.sharedCore.tokenFormatter(separatorToken),
					t.sharedCore.tokenFormatter(listEndToken),
				))

				return t.sharedCore.buildFailureRuleResult(nil)
			}
		},
		recovery,
	)
}

type ruleEndpoint[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	sharedCore *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
}

/*
Optional makes a rule optional to execute.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Optional(
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := fmt.Sprintf("Optional(%s)", rule.GetName())
	return r.sharedCore.constructOptionalRule(
		name,
		rule.GetExpectedLabel(),
		func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
			result := ctx.ExecuteRule(rule, syntaxa.ExecutionProbe)
			if result.Failed() {
				return Result[TObservation, TToken, TTokenRole, TNodeKind]{
					Node:      nil,
					Succeeded: true,
				}
			}

			return result
		},
		rule.GetRecoveryTokens(),
	)
}

/*
Root creates a structural wrapper for the program.

If mustConsume is set to false, this may succeed without consuming (i.e., the program's contents may be empty)
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Root(
	grammarID string,
	nodeKind TNodeKind,
	mustConsume bool,
	rules ...Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := r.sharedCore.createRuleName("Root", grammarID)

	return r.sharedCore.constructRule(
		name,
		grammarID,
		r.sharedCore.createContract(mustConsume, true),
		func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {

			node := ctx.Editor.NewNode(nodeKind)

			for _, rule := range rules {
				result := ctx.ExecuteRule(rule, syntaxa.ExecutionNormal)

				if result.Failed() {
					return r.sharedCore.buildSuccessRuleResult(node)
				}

				if result.Node != nil {
					ctx.Editor.AttachChild(node, result.Node)
				}
			}

			return r.sharedCore.buildSuccessRuleResult(node)
		},
		nil,
	)
}

/*
Sequence matches a sequence of rules.

On success it adds all child nodes to this node (skipping nil-nodes).
On failure it emits a syntax error (propagating the child-rule syntax error).
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Sequence(
	grammarID string,
	nodeKind TNodeKind,
	rules ...Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := r.sharedCore.createRuleName("Sequence", grammarID)

	mustConsume := false
	for i := range rules {
		if rules[i].GetContract().MustConsume {
			mustConsume = true
			break
		}
	}

	return r.sharedCore.constructRule(
		name,
		grammarID,
		r.sharedCore.createContract(mustConsume, true),
		func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
			results := make([]Result[TObservation, TToken, TTokenRole, TNodeKind], len(rules))

			for i, rule := range rules {
				result := ctx.ExecuteRule(rule, syntaxa.ExecutionNormal)
				if result.Failed() {
					return r.sharedCore.buildFailureRuleResult(nil)
				}
				results[i] = result
			}

			node := ctx.Editor.NewNode(nodeKind)
			for _, result := range results {
				if result.Node != nil {
					ctx.Editor.AttachChild(node, result.Node)
				}
			}

			return r.sharedCore.buildSuccessRuleResult(node)
		},
		nil,
	)
}

/*
NOrMore matches a rule at least min times and then as many times as possible.

Each iteration is speculative — failure simply stops the repetition.

Guards against infinite loops by requiring consumption per success.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) NOrMore(
	grammarID string,
	nodeKind TNodeKind,
	min int,
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	if min < 0 {
		panic("NOrMore: min must be >= 0")
	}

	name := r.sharedCore.createRuleName("NOrMore", grammarID)

	mustConsume := min > 0 && rule.GetContract().MustConsume

	return r.sharedCore.constructRule(
		name,
		rule.GetExpectedLabel(),
		r.sharedCore.createContract(mustConsume, true),
		func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {

			node := ctx.Editor.NewNode(nodeKind)

			count := 0

			for {
				before := ctx.Token.PeekRaw(0)

				result := ctx.ExecuteRule(rule, syntaxa.ExecutionProbe)
				if result.Failed() {
					break
				}

				after := ctx.Token.PeekRaw(0)

				if before.Token == after.Token &&
					before.StartLine == after.StartLine &&
					before.StartColumn == after.StartColumn {
					panic("NOrMore: rule succeeded without consuming input (infinite loop)")
				}

				if result.Node != nil {
					ctx.Editor.AttachChild(node, result.Node)
				}

				count++
			}

			if count < min {
				return r.sharedCore.buildFailureRuleResult(nil)
			}

			return r.sharedCore.buildSuccessRuleResult(node)
		},
		rule.GetRecoveryTokens(),
	)
}

/* ZeroOrMore is a thin wrapper around NOrMore. */
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) ZeroOrMore(
	grammarID string,
	nodeKind TNodeKind,
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return r.NOrMore(grammarID, nodeKind, 0, rule)
}

/* OneOrMore is a thin wrapper around NOrMore. */
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) OneOrMore(
	grammarID string,
	nodeKind TNodeKind,
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return r.NOrMore(grammarID, nodeKind, 1, rule)
}

/*
OptionalPrefix is a wrapper around OptionalWhen that turns a rule optional when the first token is matched.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) OptionalPrefix(
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	prefixToken TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return r.OptionalWhen(rule, func(ctx *syntaxa.SelectRuleContext[TObservation, TToken, TTokenRole]) bool {
		peeked := ctx.Peek(0)
		return peeked.Token == prefixToken
	})
}

/*
OptionalWhen turns a rule into an optional rule but only when shouldStart is false.

This can be used for prefixed optionals.

Recommended is to use the wrappers around this.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) OptionalWhen(
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	shouldStart func(ctx *syntaxa.SelectRuleContext[TObservation, TToken, TTokenRole]) bool,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := fmt.Sprintf("OptionalWhen(%s)", rule.GetName())

	return r.sharedCore.constructOptionalRule(
		name,
		rule.GetExpectedLabel(),
		func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
			if !shouldStart(ctx.Select) {
				return Result[TObservation, TToken, TTokenRole, TNodeKind]{Node: nil, Succeeded: true}
			}

			return ctx.ExecuteRule(rule, syntaxa.ExecutionNormal)
		},
		rule.GetRecoveryTokens(),
	)
}

// ------------------------------------------------------------- RULEBUILDER

/*
RuleBuilder serves as the Syntaxa extension with predefined rule behaviour.

Use this for common grammar.
For advanced behaviour, create manual rules using the Syntaxa factory.
*/
type RuleBuilder[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	/* The Token endpoint contains rules dealing directly with lexer tokens. */
	Token *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]

	/* The Rule endpoint provides rules for combining and compositing rules. */
	Rule *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
}

/*
RuleBuilderCreate constructs the rule builder instance.
*/
func RuleBuilderCreate[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable](
	tokenFormatter func(token TToken) string,
) *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	sharedCore := &sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{
		tokenFormatter: tokenFormatter,
	}

	tokenEndpoint := &tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{
		sharedCore: sharedCore,
	}

	ruleEndpoint := &ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{
		sharedCore: sharedCore,
	}

	return &RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{
		Token: tokenEndpoint,
		Rule:  ruleEndpoint,
	}
}

// ------------------------------------------------------------- PRIVATE HELPERS

type sharedCore[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	tokenFormatter func(token TToken) string
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) formatTokensAsList(
	separator string,
	tokens ...TToken,
) string {
	if separator == "" {
		separator = ", "
	}

	// Fast paths.
	switch len(tokens) {
	case 0:
		return ""
	case 1:
		if s == nil || s.tokenFormatter == nil {
			return "<nil-formatter>"
		}
		return s.tokenFormatter(tokens[0])
	}

	if s == nil || s.tokenFormatter == nil {
		return "<nil-formatter>"
	}

	var sb strings.Builder

	sb.Grow(len(tokens) * (8 + len(separator)))

	for i, tok := range tokens {
		if i > 0 {
			sb.WriteString(separator)
		}
		sb.WriteString(s.tokenFormatter(tok))
	}

	return sb.String()
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) buildSuccessRuleResult(
	node *syntaxa.SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	return Result[TObservation, TToken, TTokenRole, TNodeKind]{
		Node:      node,
		Succeeded: true,
	}
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) buildFailureRuleResult(
	node *syntaxa.SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	return Result[TObservation, TToken, TTokenRole, TNodeKind]{
		Node:      node,
		Succeeded: false,
	}
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) createRuleName(
	rule, grammarID string,
) string {
	return fmt.Sprintf("Rule %s (id:%s)", rule, grammarID)
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) constructStructuralRule(
	ruleName, expectedLabel string,
	execution syntaxa.ParserRuleExecutor[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	recovery []TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return s.constructRule(ruleName, expectedLabel, s.createContract(true, true), execution, recovery)
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) constructSkippingRule(
	ruleName, expectedLabel string,
	execution syntaxa.ParserRuleExecutor[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	recovery []TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return s.constructRule(ruleName, expectedLabel, s.createContract(true, false), execution, recovery)
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) constructOptionalRule(
	ruleName, expectedLabel string,
	execution syntaxa.ParserRuleExecutor[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	recovery []TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return s.constructRule(ruleName, expectedLabel, s.createContract(false, false), execution, recovery)
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) constructVirtualRule(
	ruleName, expectedLabel string,
	execution syntaxa.ParserRuleExecutor[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	recovery []TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return s.constructRule(ruleName, expectedLabel, s.createContract(false, true), execution, recovery)
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) createContract(
	mustConsume, mustReturnNode bool,
) syntaxa.RuleContract {
	return syntaxa.RuleContract{
		MustConsume:    mustConsume,
		MustReturnNode: mustReturnNode,
	}
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) constructRule(
	name, expectedLabel string,
	ruleContract syntaxa.RuleContract,
	execution syntaxa.ParserRuleExecutor[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	recovery []TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return syntaxa.ParserRuleCreate(
		name,
		expectedLabel,
		execution,
		ruleContract,
		recovery,
	)
}
