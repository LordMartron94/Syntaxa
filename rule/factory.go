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
	identity := t.sharedCore.createRuleIdentity(name, grammarID, t.sharedCore.formatTokensAsList(", ", tokens...))

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
			name,
			peeked,
			fmt.Sprintf(
				"unexpected %s, wanted one of %s",
				t.sharedCore.tokenFormatter(peeked.Token),
				t.sharedCore.formatTokensAsList(", ", tokens...),
			),
		)

		return t.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureNoMatch)
	}

	if addNode {
		return t.sharedCore.constructStructuralRule(
			identity,
			rule,
			nil,
		)
	} else {
		return t.sharedCore.constructSkippingRule(
			identity,
			rule,
			nil,
		)
	}
}

type TrailingSeparatorMode uint8

const (
	TrailingForbidden TrailingSeparatorMode = iota
	TrailingOptional
	TrailingRequired
)

/*
List matches a list-like grammar.

It automatically handles recovery.

If allowEmptyList is true, OPEN CLOSE is valid.
If optionalTrailingSeparator is true, the final separator may appear before CLOSE.
*/
func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) List(
	grammarID string,
	listOpenToken, elementToken, separatorToken, listEndToken TToken,
	listNodeKind, elementNodeKind TNodeKind,
	allowEmptyList bool,
	mode TrailingSeparatorMode,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	name := t.sharedCore.createRuleName("List", grammarID)
	identity := t.sharedCore.createRuleIdentity(name, grammarID, "")
	recovery := []TToken{listEndToken}

	return t.sharedCore.constructStructuralRule(
		identity,
		func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {

			// 1. Initial validation of the opening token
			open := ctx.Token.Peek(0)
			if open.Token != listOpenToken {
				return t.reportMismatch(ctx, name, open, listOpenToken)
			}
			ctx.Token.Consume()

			node := ctx.Editor.NewNode(listNodeKind)

			// 2. Handle the immediate exit (Empty List)
			if peek := ctx.Token.Peek(0); peek.Token == listEndToken {
				return t.handleEmptyList(ctx, name, node, peek, allowEmptyList)
			}

			// 3. Enter Main Automaton
			return t.runListAutomaton(ctx, name, node, elementToken, separatorToken, listEndToken, elementNodeKind, mode)
		},
		recovery,
	)
}

func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) runListAutomaton(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	name string,
	parentNode *syntaxa.SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
	elementToken, separatorToken, listEndToken TToken,
	elementNodeKind TNodeKind,
	mode TrailingSeparatorMode,
) Result[TObservation, TToken, TTokenRole, TNodeKind] {

	// --- first element (already ensured not empty) ---

	first := ctx.Token.Peek(0)
	if first.Token != elementToken {
		return t.reportMismatch(ctx, name, first, elementToken)
	}

	elem := ctx.Token.Consume()
	t.addChildElement(ctx, parentNode, elementNodeKind, elem)

	for {
		peek := ctx.Token.Peek(0)

		// ------------------------------
		// NORMAL CLOSE (no trailing)
		// ------------------------------
		if peek.Token == listEndToken {

			if mode == TrailingRequired {
				ctx.Error.ReportAtEnd(name, elem, "missing required trailing separator")
				return t.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
			}

			ctx.Token.Consume()
			return t.sharedCore.buildSuccessRuleResult(parentNode)
		}

		// ------------------------------
		// EXPECT SEPARATOR
		// ------------------------------
		if peek.Token == elementToken {
			ctx.Error.ReportAtEnd(
				name,
				elem,
				fmt.Sprintf("missing %s between list elements", t.sharedCore.tokenFormatter(separatorToken)),
			)
			return t.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
		}

		sep := ctx.Token.Consume()

		next := ctx.Token.Peek(0)

		// ------------------------------
		// TRAILING SEPARATOR CASE
		// ------------------------------
		if next.Token == listEndToken {

			if mode == TrailingForbidden {
				ctx.Error.ReportAt(name, sep, "trailing separator not allowed")
				return t.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
			}

			ctx.Token.Consume()
			return t.sharedCore.buildSuccessRuleResult(parentNode)
		}

		// ------------------------------
		// NEXT ELEMENT
		// ------------------------------
		if next.Token != elementToken {
			return t.reportMismatch(ctx, name, next, elementToken)
		}

		elem = ctx.Token.Consume()
		t.addChildElement(ctx, parentNode, elementNodeKind, elem)
	}
}

func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) handleEmptyList(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	name string,
	node *syntaxa.SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
	peek lexarch.Lexeme[TObservation, TToken, TTokenRole],
	allowEmpty bool,
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	if !allowEmpty {
		ctx.Error.ReportAt(name, peek, "empty list not allowed")
		return t.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
	}
	ctx.Token.Consume()
	return t.sharedCore.buildSuccessRuleResult(node)
}

func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) addChildElement(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	parent *syntaxa.SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
	kind TNodeKind,
	lexeme lexarch.Lexeme[TObservation, TToken, TTokenRole],
) {
	elem := ctx.Editor.NewNode(kind)
	ctx.Editor.AddToken(elem, lexeme)
	ctx.Editor.AttachChild(parent, elem)
}

func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) reportMismatch(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	name string,
	found lexarch.Lexeme[TObservation, TToken, TTokenRole],
	expected TToken,
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	msg := fmt.Sprintf("unexpected %s, expected %s",
		t.sharedCore.tokenFormatter(found.Token),
		t.sharedCore.tokenFormatter(expected))
	ctx.Error.ReportAt(name, found, msg)
	return t.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
}

type ruleEndpoint[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	token      *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
	sharedCore *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
}

/*
Optional makes a rule optional to execute.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Optional(
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := fmt.Sprintf("Optional(%s)", rule.GetName())
	identity := r.sharedCore.createRuleIdentity(name, rule.GetGrammarID(), rule.GetExpectedLabel())
	return r.sharedCore.constructOptionalRule(
		identity,
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
	identity := r.sharedCore.createRuleIdentity(name, rule.GetGrammarID(), rule.GetExpectedLabel())

	return r.sharedCore.constructOptionalRule(
		identity,
		func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
			if !shouldStart(ctx.Select) {
				return Result[TObservation, TToken, TTokenRole, TNodeKind]{Node: nil, Succeeded: true}
			}

			return ctx.ExecuteRule(rule, syntaxa.ExecutionNormal)
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
	identity := r.sharedCore.createRuleIdentity(name, grammarID, "")

	return r.sharedCore.constructRule(
		identity,
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

Semantics:
  - First rule may fail with FailureNoMatch (grammar boundary)
  - Once any rule succeeds, the sequence is committed
  - Any later failure is a syntax error (FailureError)

On success all child nodes are attached (nil nodes skipped).
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Sequence(
	grammarID string,
	nodeKind TNodeKind,
	rules ...Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := r.sharedCore.createRuleName("Sequence", grammarID)
	identity := r.sharedCore.createRuleIdentity(name, grammarID, "")
	return r.listCore(identity, nodeKind, nil, rules...)
}

/*
Block is akin to Sequence except that it has an integrated blockEnd token.

It automatically handles recovery.

Note: You must still include the rule for the block end token.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Block(
	grammarID string,
	nodeKind TNodeKind,
	blockEndToken TToken,
	rules ...Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := r.sharedCore.createRuleName("Block", grammarID)
	identity := r.sharedCore.createRuleIdentity(name, grammarID, "")
	return r.listCore(identity, nodeKind, []TToken{blockEndToken}, rules...)
}

func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) listCore(
	identity syntaxa.RuleIdentity,
	nodeKind TNodeKind,
	recovery []TToken,
	rules ...Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	mustConsume := false
	for i := range rules {
		if rules[i].GetContract().MustConsume {
			mustConsume = true
			break
		}
	}

	return r.sharedCore.constructRule(
		identity,
		r.sharedCore.createContract(mustConsume, true),
		func(ctx *syntaxa.ExecRuleContext[
			TObservation, TToken, TTokenRole, TLexerState, TNodeKind,
		]) Result[TObservation, TToken, TTokenRole, TNodeKind] {

			results := make([]Result[TObservation, TToken, TTokenRole, TNodeKind], len(rules))

			for i, rule := range rules {
				result := ctx.ExecuteRule(rule, syntaxa.ExecutionNormal)

				if result.Failed() {
					if i == 0 && result.Kind == syntaxa.FailureNoMatch {
						return r.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureNoMatch)
					}

					return r.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
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
		recovery,
	)
}

/*
NOrMore matches a rule at least min times and then as many times as possible.

Semantics:
  - Success → attach + continue
  - FailureNoMatch → stop repetition (grammar boundary)
  - FailureError → recovery already ran:
  - if progress happened → retry
  - if no progress → propagate error

Guards against infinite loops by requiring progress on recovery.
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
	identity := r.sharedCore.createRuleIdentity(name, grammarID, rule.GetExpectedLabel())

	mustConsume := min > 0 && rule.GetContract().MustConsume

	return r.sharedCore.constructRule(
		identity,
		r.sharedCore.createContract(mustConsume, true),
		func(ctx *syntaxa.ExecRuleContext[
			TObservation, TToken, TTokenRole, TLexerState, TNodeKind,
		]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
			node := ctx.Editor.NewNode(nodeKind)
			count := 0

			for {
				before := ctx.Token.PeekRaw(0)
				result := ctx.ExecuteRule(rule, syntaxa.ExecutionNormal)

				if result.Succeeded {
					if result.Node != nil {
						ctx.Editor.AttachChild(node, result.Node)
					}
					count++
					continue
				}

				switch result.Kind {

				case syntaxa.FailureNoMatch:
					goto done

				case syntaxa.FailureError:
					after := ctx.Token.PeekRaw(0)

					if before.TokenNumber == after.TokenNumber {
						return result
					}

					continue
				}
			}

		done:
			if count < min {
				return r.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureNoMatch)
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
		token:      tokenEndpoint,
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
	failureKind syntaxa.FailureKind,
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	return Result[TObservation, TToken, TTokenRole, TNodeKind]{
		Node:      node,
		Succeeded: false,
		Kind:      failureKind,
	}
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) createRuleName(
	rule, grammarID string,
) string {
	return fmt.Sprintf("Rule %s (id:%s)", rule, grammarID)
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) createRuleIdentity(
	name string,
	grammarID string,
	expectedLabel string,
) syntaxa.RuleIdentity {
	return syntaxa.RuleIdentity{
		RuleName:      name,
		GrammarID:     grammarID,
		ExpectedLabel: expectedLabel,
	}
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) constructStructuralRule(
	identity syntaxa.RuleIdentity,
	execution syntaxa.ParserRuleExecutor[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	recovery []TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return s.constructRule(identity, s.createContract(true, true), execution, recovery)
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) constructSkippingRule(
	identity syntaxa.RuleIdentity,
	execution syntaxa.ParserRuleExecutor[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	recovery []TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return s.constructRule(identity, s.createContract(true, false), execution, recovery)
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) constructOptionalRule(
	identity syntaxa.RuleIdentity,
	execution syntaxa.ParserRuleExecutor[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	recovery []TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return s.constructRule(identity, s.createContract(false, false), execution, recovery)
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) constructVirtualRule(
	identity syntaxa.RuleIdentity,
	execution syntaxa.ParserRuleExecutor[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	recovery []TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return s.constructRule(identity, s.createContract(false, true), execution, recovery)
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
	identity syntaxa.RuleIdentity,
	ruleContract syntaxa.RuleContract,
	execution syntaxa.ParserRuleExecutor[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	recovery []TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return syntaxa.ParserRuleCreate(
		identity,
		execution,
		ruleContract,
		recovery,
	)
}
