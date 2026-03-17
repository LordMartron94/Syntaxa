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

/*
Rule is an alias for syntaxa.ParserRule.

Use it when building or composing rules with the rule factory.
*/
type Rule[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] = syntaxa.ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]

/*
Result is an alias for syntaxa.RuleResult.

It is the return type of rule execution: success/failure, optional LST node, and failure kind.
*/
type Result[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind comparable] = syntaxa.RuleResult[TObservation, TToken, TTokenRole, TNodeKind]

/*
Lexeme is an alias for lexarch.Lexeme.

Represents a single token with observation (position, etc.), token value, and role.
*/
type Lexeme[TObservation cmp.Ordered, TToken, TTokenRole comparable] = lexarch.Lexeme[TObservation, TToken, TTokenRole]

// ------------------------------------------------------------- TOKEN ENDPOINT

type tokenEndpoint[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	sharedCore *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
}

/*
Expect matches a single token at the current position.

On success: consumes the token, creates an LST node of outputNodeKind, and attaches the lexeme as the node's token.
On failure: reports a syntax error and returns FailureError (no rollback of other state beyond the engine's snapshot).

Use cases:
- Matching keywords, operators, or punctuation.
- Building LST nodes for terminals when the node kind is significant.

Prerequisites:
- grammarID identifies this production for error messages and grammar IR.
- outputNodeKind is the LST node kind to create on success.
- token is the exact token value to match.

Edge cases:
- If the current token does not match, a diagnostic is reported and the rule fails.
*/
func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Expect(
	grammarID syntaxa.GrammarLabel,
	outputNodeKind TNodeKind,
	token TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return t.expectCore("Expect", grammarID, outputNodeKind, true, token)
}

/*
ExpectVirtual matches a single token without creating an LST node.

On success: consumes the token and returns success with a nil node.
On failure: reports a syntax error and returns FailureError.

Use cases:
- Skipping punctuation or keywords that do not need a dedicated LST node.
- Matching structure (e.g. closing delimiter) where only the presence matters.

Prerequisites:
- grammarID identifies this production.
- token is the exact token value to match.

Edge cases:
- Same as Expect for mismatch; no node is ever produced.
*/
func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) ExpectVirtual(
	grammarID syntaxa.GrammarLabel,
	token TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	var zeroKind TNodeKind
	return t.expectCore("ExpectVirtual", grammarID, zeroKind, false, token)
}

/*
ExpectOneOf matches one of several tokens at the current position.

On success: consumes the token, creates an LST node of outputNodeKind, and attaches the lexeme.
On failure: reports a syntax error listing the expected tokens.

Use cases:
- Matching a set of keywords or operators that share the same LST node kind.
- Union of terminals without building a full choice of sub-rules.

Prerequisites:
- grammarID identifies this production.
- outputNodeKind is the LST node kind to create on success.
- tokens must contain at least one token.

Edge cases:
- Empty tokens slice is not validated here; typically avoid.
- Error message includes a formatted list of expected tokens via the builder's tokenFormatter.
*/
func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) ExpectOneOf(
	grammarID syntaxa.GrammarLabel,
	outputNodeKind TNodeKind,
	tokens ...TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return t.expectCore("ExpectOneOf", grammarID, outputNodeKind, true, tokens...)
}

func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) expectCore(
	ruleName string,
	grammarID syntaxa.GrammarLabel,
	outputNodeKind TNodeKind,
	addNode bool,
	tokens ...TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	name := t.sharedCore.createRuleName(ruleName, grammarID)
	identity := t.sharedCore.createRuleIdentity(
		name,
		grammarID,
		t.sharedCore.formatTokensAsList(", ", tokens...),
	)

	// ---------------------------
	// Build grammar IR
	// ---------------------------

	var grammar *syntaxa.Grammar[TToken, TNodeKind]

	if len(tokens) == 1 {
		grammar = syntaxa.Token[TToken, TNodeKind](grammarID, tokens[0])
	} else {
		children := make([]*syntaxa.Grammar[TToken, TNodeKind], len(tokens))
		for i, tok := range tokens {
			children[i] = syntaxa.Token[TToken, TNodeKind](grammarID, tok)
		}
		grammar = syntaxa.Choice(grammarID, children...)
	}

	// ---------------------------
	// Runtime execution
	// ---------------------------

	rule := func(ctx *syntaxa.ExecRuleContext[
		TObservation, TToken, TTokenRole, TLexerState, TNodeKind,
	]) Result[TObservation, TToken, TTokenRole, TNodeKind] {

		peeked := ctx.Token.Peek(0)

		if slices.Contains(tokens, peeked.Token) {
			value := ctx.Token.Consume()

			if addNode {
				node := ctx.Editor.NewNode(outputNodeKind)
				ctx.Editor.AddToken(node, value)
				return t.sharedCore.buildSuccessRuleResult(node)
			}

			return t.sharedCore.buildSuccessRuleResult(nil)
		}

		// Do not report here: returning FailureNoMatch lets Choice try other alternatives.
		// Sequences report when a required element fails via listCore (committed context).
		return t.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureNoMatch)
	}

	// ---------------------------
	// Construct rule
	// ---------------------------

	if addNode {
		grammar.OutputNodeKind = &outputNodeKind
		return t.sharedCore.constructStructuralRule(
			identity,
			rule,
			nil,
			grammar,
		)
	}

	return t.sharedCore.constructSkippingRule(
		identity,
		rule,
		nil,
		grammar,
	)
}

type TrailingSeparatorMode uint8

const (
	/* TrailingForbidden forbids a separator after the last element (e.g. "a, b" not "a, b,"). */
	TrailingForbidden TrailingSeparatorMode = iota
	/* TrailingOptional allows an optional trailing separator (e.g. "a, b" or "a, b,"). */
	TrailingOptional
	/* TrailingRequired requires a trailing separator before the closing delimiter. */
	TrailingRequired
)

/*
List matches a list grammar: openToken, then zero or more (elementToken separatorToken)* elementToken, then listEndToken.

Recovery is automatic: listEndToken is used as the recovery boundary so that on error the parser can resync at the closing delimiter.

Semantics:
- Requires listOpenToken, then either an empty list (if allowEmptyList) or at least one element.
- Elements are separated by separatorToken; trailing separator is governed by mode.
- On success builds a parent node of listNodeKind with child nodes of elementNodeKind (one per element).

Use cases:
- Parenthesized or bracketed lists (e.g. ( a, b, c ) or [ x; y; z ]).
- Comma-separated or semicolon-separated lists with configurable trailing separator.

Prerequisites:
- listOpenToken, elementToken, separatorToken, listEndToken must be distinct as appropriate for the grammar.
- allowEmptyList: if true, openToken immediately followed by listEndToken is valid.
- mode: TrailingForbidden, TrailingOptional, or TrailingRequired.

Edge cases:
- Empty list when allowEmptyList is false reports a syntax error.
- TrailingRequired with no trailing separator reports an error at the last element.
- TrailingForbidden with a trailing separator reports an error at the separator.
*/
func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) List(
	grammarID syntaxa.GrammarLabel,
	listOpenToken, elementToken, separatorToken, listEndToken TToken,
	listNodeKind, elementNodeKind TNodeKind,
	allowEmptyList bool,
	mode TrailingSeparatorMode,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	name := t.sharedCore.createRuleName("List", grammarID)
	expectedLabel := t.sharedCore.tokenFormatter(listEndToken)
	if expectedLabel == "" {
		expectedLabel = string(grammarID)
	}
	identity := t.sharedCore.createRuleIdentity(name, grammarID, expectedLabel)
	recovery := []TToken{listEndToken}

	grammar := t.getListGrammar(grammarID, listOpenToken, elementToken, separatorToken, listEndToken, allowEmptyList, mode)
	syntaxa.MarkAsContextBoundary(grammar)
	grammar.OutputNodeKind = &listNodeKind
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
		grammar,
	)
}

/*
TransparentSequence matches a strict sequence of rules but does not create a new parent node.

On success, it returns a fragment result containing all non-nil nodes produced by the sub-rules.
When this result is attached to a parent via the editor, the fragment is unpacked, and its
children are attached directly to that parent.

Use cases:
- Grouping rules logically in the grammar without creating "middle-man" nodes in the LST.
- Breaking down complex productions into smaller, reusable sequences that shouldn't appear in the final tree.

Prerequisites:
- grammarID identifies this production.
- rules must not be empty.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) TransparentSequence(
	grammarID syntaxa.GrammarLabel,
	rules ...Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := r.sharedCore.createRuleName("TransparentSequence", grammarID)
	identity := r.sharedCore.createRuleIdentity(name, grammarID, r.expectedLabelFromRule(rules[0]))

	mustConsume := r.listCoreMustConsume(rules)
	grammar := r.buildConcatGrammarFromRules(grammarID, rules)

	exec := func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
		results := make([]Result[TObservation, TToken, TTokenRole, TNodeKind], len(rules))
		startMarker := ctx.Token.PeekRaw(0).TokenNumber

		for i, rule := range rules {
			result := ctx.ExecuteRule(rule, syntaxa.ExecutionNormal)

			if result.Failed() {
				currentMarker := ctx.Token.PeekRaw(0).TokenNumber
				effectiveKind := sequenceCommitmentFailureKind(startMarker, currentMarker, result.Kind)

				if effectiveKind == syntaxa.FailureError && result.Kind == syntaxa.FailureNoMatch {
					peeked := ctx.Token.Peek(0)

					if inserted, fakeResult := r.tryFollowSetInsertion(ctx, rule, identity, peeked); inserted {
						results[i] = fakeResult
						continue
					}

					msg := fmt.Sprintf("expected %s", rule.GetExpectedLabel())
					if lastLex, ok := ctx.GetLastConsumedLexeme(); ok {
						ctx.Error.ReportAtEnd(string(identity.RuleName), lastLex, msg)
					} else {
						ctx.Error.ReportAt(string(identity.RuleName), peeked, msg)
					}
				}

				return r.sharedCore.buildFailureRuleResult(nil, effectiveKind)
			}
			results[i] = result
		}

		var zeroKind TNodeKind
		fragmentNode := ctx.Editor.NewTransientNode(zeroKind)
		r.attachResultsToNode(ctx, fragmentNode, results)

		return r.sharedCore.buildFragmentRuleResult(fragmentNode)
	}

	return r.sharedCore.constructRule(identity, r.sharedCore.createContract(mustConsume, false), exec, nil, nil, grammar)
}

/*
getListGrammar builds the grammar IR for a list production: open, (elem sep)* elem?, close,
with optional empty list and configurable trailing separator mode.
*/
func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) getListGrammar(
	grammarID syntaxa.GrammarLabel,
	listOpenToken, elementToken, separatorToken, listEndToken TToken,
	allowEmptyList bool,
	mode TrailingSeparatorMode,
) *syntaxa.Grammar[TToken, TNodeKind] {

	// Core grammar pieces

	elemG := syntaxa.Token[TToken, TNodeKind](grammarID, elementToken)
	sepG := syntaxa.Token[TToken, TNodeKind](grammarID, separatorToken)

	// (S E)
	sepElem := syntaxa.Concat[TToken, TNodeKind](grammarID, sepG, elemG)

	// (S E)*
	repeatSepElem := syntaxa.ZeroOrMore[TToken, TNodeKind](grammarID, sepElem)

	// E (S E)*
	baseBody := syntaxa.Concat[TToken, TNodeKind](grammarID, elemG, repeatSepElem)

	var body *syntaxa.Grammar[TToken, TNodeKind]

	switch mode {

	case TrailingForbidden:
		body = baseBody

	case TrailingOptional:
		body = syntaxa.Concat[TToken, TNodeKind](
			grammarID,
			baseBody,
			syntaxa.Optional[TToken, TNodeKind](grammarID, sepG),
		)

	case TrailingRequired:
		body = syntaxa.Concat[TToken, TNodeKind](
			grammarID,
			baseBody,
			sepG,
		)
	}

	if allowEmptyList {
		body = syntaxa.Optional[TToken, TNodeKind](grammarID, body)
	}

	return syntaxa.Nest[TToken, TNodeKind](
		grammarID,
		listOpenToken,
		listEndToken,
		body,
	)
}

/*
runListAutomaton consumes the first element then runs the (separator, element)* loop until
listEndToken. Enforces trailing separator mode and reports diagnostics on mismatch.
*/
func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) runListAutomaton(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	name syntaxa.RuleLabel,
	parentNode *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind],
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

		if peek.Token == listEndToken {
			if mode == TrailingRequired {
				return t.reportSpecificError(ctx, name, "missing required trailing separator", syntaxa.FailureError)
			}
			ctx.Token.Consume()
			return t.sharedCore.buildSuccessRuleResult(parentNode)
		}

		// Handle missing separator: if we see another element immediately
		if peek.Token == elementToken {
			msg := fmt.Sprintf("expected %s between elements", t.sharedCore.tokenFormatter(separatorToken))
			return t.reportSpecificError(ctx, name, msg, syntaxa.FailureError)
		}

		// Consume separator
		if peek.Token != separatorToken {
			msg := fmt.Sprintf("expected %s or %s",
				t.sharedCore.tokenFormatter(separatorToken),
				t.sharedCore.tokenFormatter(listEndToken))
			return t.reportSpecificError(ctx, name, msg, syntaxa.FailureError)
		}
		ctx.Token.Consume()

		// Handle trailing check
		next := ctx.Token.Peek(0)
		if next.Token == listEndToken {
			if mode == TrailingForbidden {
				return t.reportSpecificError(ctx, name, "trailing separator not allowed", syntaxa.FailureError)
			}
			ctx.Token.Consume()
			return t.sharedCore.buildSuccessRuleResult(parentNode)
		}

		// Expect next element
		if next.Token != elementToken {
			msg := fmt.Sprintf("expected %s after separator", t.sharedCore.tokenFormatter(elementToken))
			return t.reportSpecificError(ctx, name, msg, syntaxa.FailureError)
		}

		elem := ctx.Token.Consume()
		t.addChildElement(ctx, parentNode, elementNodeKind, elem)
	}
}

func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) reportSpecificError(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	name syntaxa.RuleLabel,
	msg string,
	kind syntaxa.FailureKind,
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	peek := ctx.Token.Peek(0)
	ctx.Error.ReportAt(string(name), peek, msg)
	return t.sharedCore.buildFailureRuleResult(nil, kind)
}

/*
handleEmptyList consumes the list end token and returns success with node if allowEmpty is true;
otherwise reports an error and returns failure.
*/
func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) handleEmptyList(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	name syntaxa.RuleLabel,
	node *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind],
	peek lexarch.Lexeme[TObservation, TToken, TTokenRole],
	allowEmpty bool,
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	if !allowEmpty {
		ctx.Error.ReportAt(string(name), peek, "empty list not allowed")
		return t.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
	}
	ctx.Token.Consume()
	return t.sharedCore.buildSuccessRuleResult(node)
}

/*
addChildElement creates a new LST node of kind, attaches the lexeme as its token, and appends it as a child of parent.
*/
func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) addChildElement(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	parent *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind],
	kind TNodeKind,
	lexeme lexarch.Lexeme[TObservation, TToken, TTokenRole],
) {
	elem := ctx.Editor.NewNode(kind)
	ctx.Editor.AddToken(elem, lexeme)
	ctx.Editor.AttachChild(parent, elem)
}

/*
reportMismatch reports a syntax error for "unexpected X, expected Y" and returns a failure result.
*/
func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) reportMismatch(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	name syntaxa.RuleLabel,
	found lexarch.Lexeme[TObservation, TToken, TTokenRole],
	expected TToken,
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	ctx.Error.ReportAt(string(name), found, t.sharedCore.formatUnexpectedExpected(found.Token, expected))
	return t.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
}

// ------------------------------------------------------------- RULE ENDPOINT

type ruleEndpoint[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	token      *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
	sharedCore *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
}

/*
Optional wraps a rule so that it may match zero or one time.

The inner rule is executed in probe mode: if it fails, no diagnostic is reported and the optional succeeds with a nil node. If it succeeds, its result is returned.

Use cases:
- Optional punctuation or keywords (e.g. trailing semicolon).
- Optional sub-clauses (e.g. "else" branch).

Prerequisites:
- rule must be a valid parser rule (e.g. from Token or Rule endpoints).

Edge cases:
- Failure of the inner rule is treated as "no match" and yields success with nil node.
- Success of the inner rule returns the inner rule's node (or nil if that rule returns nil).
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Optional(
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := syntaxa.RuleLabel(fmt.Sprintf("Optional(%s)", rule.GetName()))
	identity := r.sharedCore.createRuleIdentity(name, rule.GetGrammarLabel(), rule.GetExpectedLabel())
	return r.sharedCore.constructOptionalRule(
		identity,
		func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
			result := ctx.ExecuteRule(rule, syntaxa.ExecutionProbe)
			if result.Failed() {
				return r.sharedCore.buildSuccessRuleResult(nil)
			}

			return result
		},
		rule.GetRecoveryTokens(),
		syntaxa.Optional[TToken, TNodeKind](rule.GetGrammarLabel(), rule.GetGrammar()),
	)
}

/*
OptionalPrefix conditionally executes the rule if the next token matches prefixToken.

Execution flow:
 1. Peeks at the next token without consuming it.
 2. If it matches prefixToken, it commits to the rule. The rule runs in ExecutionNormal,
    meaning any failure inside the rule results in a syntax error.
 3. If it does not match, it skips the rule entirely, consumes nothing, and returns a nil node.

Use cases:
- Parsing optional syntax blocks that are strictly gated by a specific keyword (e.g., an "else" block).

Parameters:
- rule: The parser rule to execute if the prefix is present.
- prefixToken: The token required to trigger the rule's execution.
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
OptionalWhen conditionally executes the rule based on a custom lookahead predicate.

Execution flow:
 1. Evaluates shouldStart. This function must inspect the token stream (e.g., via ctx.Select.Peek)
    without consuming any tokens.
 2. If shouldStart returns true, it commits to the rule. The rule runs in ExecutionNormal;
    failure results in a syntax error.
 3. If shouldStart returns false, it skips the rule, consumes nothing, and returns a nil node.

Use cases:
- Complex lookahead conditions (e.g., "run optional rule when next token is X and the one after is Y").
- Note: Prefer OptionalPrefix if you are only checking a single prefix token.

Parameters:
- rule: The parser rule to execute if the condition is met.
- shouldStart: A predicate determining whether to commit to the rule. Must be side-effect free.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) OptionalWhen(
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	shouldStart func(ctx *syntaxa.SelectRuleContext[TObservation, TToken, TTokenRole]) bool,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := syntaxa.RuleLabel(fmt.Sprintf("OptionalWhen(%s)", rule.GetName()))
	identity := r.sharedCore.createRuleIdentity(name, rule.GetGrammarLabel(), rule.GetExpectedLabel())

	return r.sharedCore.constructOptionalRule(
		identity,
		func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
			if !shouldStart(ctx.Select) {
				return r.sharedCore.buildSuccessRuleResult(nil)
			}

			return ctx.ExecuteRule(rule, syntaxa.ExecutionNormal)
		},
		rule.GetRecoveryTokens(),
		syntaxa.Optional[TToken, TNodeKind](rule.GetGrammarLabel(), rule.GetGrammar()),
	)
}

/*
PredictLookahead runs the inner rule only when all lookahead conditions are satisfied.

The predicate is implemented inside the factory from the slice: for each entry, ctx.Peek(l.Offset).Token
must equal l.Expected (after skip). The rule's grammar has Lookaheads set for introspection.
The caller does not provide a predicate; use PredictWithLookahead for custom predicate logic.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) PredictLookahead(
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	lookaheads []syntaxa.Lookahead[TToken],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return r.predictRule(rule, lookaheads, nil)
}

/*
PredictWithLookahead runs the inner rule only when the custom predicate returns true.

Use when lookahead logic cannot be expressed as a slice of (Offset, Expected). The rule's
grammar is not given Lookaheads (only PredictLookahead sets that). The predicate must
only inspect the token stream and must not consume.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) PredictWithLookahead(
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	predicate func(ctx *syntaxa.SelectRuleContext[TObservation, TToken, TTokenRole]) bool,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return r.predictRule(rule, nil, predicate)
}

func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) predictRule(
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	lookaheads []syntaxa.Lookahead[TToken],
	predicate func(ctx *syntaxa.SelectRuleContext[TObservation, TToken, TTokenRole]) bool,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := syntaxa.RuleLabel(fmt.Sprintf("Predict(%s)", rule.GetName()))
	identity := r.sharedCore.createRuleIdentity(name, rule.GetGrammarLabel(), rule.GetExpectedLabel())
	if g := rule.GetGrammar(); g != nil && len(lookaheads) > 0 {
		g.Lookaheads = lookaheads
	}
	var pred func(ctx *syntaxa.SelectRuleContext[TObservation, TToken, TTokenRole]) bool
	if len(lookaheads) > 0 {
		pred = func(ctx *syntaxa.SelectRuleContext[TObservation, TToken, TTokenRole]) bool {
			for _, l := range lookaheads {
				if ctx.Peek(l.Offset).Token != l.Expected {
					return false
				}
			}
			return true
		}
	} else {
		pred = predicate
	}
	if pred == nil {
		panic("predictRule: need non-empty lookaheads or non-nil predicate")
	}
	exec := func(ctx *syntaxa.ExecRuleContext[
		TObservation, TToken, TTokenRole, TLexerState, TNodeKind,
	]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
		if !pred(ctx.Select) {
			return r.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureNoMatch)
		}
		return ctx.ExecuteRule(rule, syntaxa.ExecutionNormal)
	}
	return r.sharedCore.constructRule(
		identity,
		rule.GetContract(),
		exec,
		rule.GetRecoveryTokens(),
		nil,
		rule.GetGrammar(),
	)
}

/*
OptionalSuffix runs the inner rule and, on success, optionally consumes suffixToken.

If the inner rule succeeds and the current token equals suffixToken, the suffix is consumed and
a new node of wrapNodeKind is created with the suffix lexeme attached and the inner rule's
node attached as its only child; that wrapper node is returned. Otherwise the inner rule's
result is returned unchanged. Inner rule failure is propagated.

Use cases:
- Postfix operators: segment followed by optional * (e.g. regex star).
- Optional trailing token that wraps the preceding result in a new node.

Prerequisites:
- grammarID identifies this production.
- wrapNodeKind is the LST node kind for the wrapper when the suffix is present.
- rule must be a valid parser rule that returns a node on success.
- suffixToken is the token that triggers wrapping when present after rule success.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) OptionalSuffix(
	grammarID syntaxa.GrammarLabel,
	wrapNodeKind TNodeKind,
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	suffixToken TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := r.sharedCore.createRuleName("OptionalSuffix", grammarID)
	identity := r.sharedCore.createRuleIdentity(name, grammarID, rule.GetExpectedLabel())
	grammar := syntaxa.Concat[TToken, TNodeKind](
		grammarID,
		rule.GetGrammar(),
		syntaxa.Optional[TToken, TNodeKind](grammarID, syntaxa.Token[TToken, TNodeKind](grammarID, suffixToken)),
	)
	syntaxa.MarkAsContextBoundary(grammar)
	exec := func(ctx *syntaxa.ExecRuleContext[
		TObservation, TToken, TTokenRole, TLexerState, TNodeKind,
	]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
		result := ctx.ExecuteRule(rule, syntaxa.ExecutionNormal)
		if result.Failed() {
			return result
		}
		innerNode := result.Node
		if ctx.Token.Peek(0).Token != suffixToken {
			return result
		}
		suffixLex := ctx.Token.Consume()
		wrapper := ctx.Editor.NewNode(wrapNodeKind)
		ctx.Editor.AddToken(wrapper, suffixLex)
		ctx.Editor.AttachChild(wrapper, innerNode)
		return r.sharedCore.buildSuccessRuleResult(wrapper)
	}
	grammar.OutputNodeKind = &wrapNodeKind
	return r.sharedCore.constructRule(
		identity,
		r.sharedCore.createContract(rule.GetContract().MustConsume, true),
		exec,
		rule.GetRecoveryTokens(),
		nil,
		grammar,
	)
}

/*
Required wraps a rule so that FailureNoMatch is upgraded to FailureError with a custom message.

The inner rule is executed in normal mode. If it returns FailureNoMatch (e.g. grammar boundary,
first token did not match), the wrapper reports a syntax error with errMessage and returns
FailureError. Success or FailureError from the inner rule are returned unchanged.

Use cases:
- Forcing a production to be committed once entered (e.g. "after 'if', expression is required").
- Converting "no match" into a user-facing error with a clear message.

Prerequisites:
- rule must be a valid parser rule.
- errMessage is reported at the current token when inner rule returns FailureNoMatch.

Edge cases:
- Inner FailureError: returned as-is (recovery already ran).
- Inner success: returned as-is.
- Inner FailureNoMatch: errMessage is reported via ReportAt, then FailureError is returned.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Required(
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	errMessage string,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	identity := r.sharedCore.createRuleIdentity(
		syntaxa.RuleLabel(fmt.Sprintf("Required(%s)", rule.GetName())),
		rule.GetGrammarLabel(),
		rule.GetExpectedLabel(),
	)

	return r.sharedCore.constructRule(
		identity,
		rule.GetContract(),
		func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
			result := ctx.ExecuteRule(rule, syntaxa.ExecutionNormal)
			if result.Kind == syntaxa.FailureNoMatch {
				peeked := ctx.Token.Peek(0)

				if lastLex, ok := ctx.GetLastConsumedLexeme(); ok {
					ctx.Error.ReportAtEnd(string(identity.RuleName), lastLex, errMessage)
				} else {
					ctx.Error.ReportAt(string(identity.RuleName), peeked, errMessage)
				}

				return r.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
			}
			return result
		},
		rule.GetRecoveryTokens(),
		nil,
		rule.GetGrammar(),
	)
}

/*
Root builds a top-level structural rule that runs a sequence of rules and attaches all produced nodes under a single root node of nodeKind.

Rules are run in order. On success, a single root node is created and each rule's result is attached; the root node is never nil on success. When any rule fails, that failure is propagated so the caller sees the real syntax error (e.g. missing semicolon) instead of a partial success. If mustConsume is false, zero consumed tokens is allowed (empty program). If mustConsume is true, at least one token must be consumed for success.

Use cases:
- Root of the grammar (e.g. "program" as sequence of declarations and statements).
- Wrapping a series of optional or repeated rules under one node.

Prerequisites:
- grammarID identifies this production.
- nodeKind is the LST kind for the root node.
- mustConsume: if true, success requires at least one token consumed across the rules.

Edge cases:
- First rule FailureNoMatch: entire Root fails with FailureNoMatch.
- Later rule failure: failure is propagated unchanged so diagnostics (e.g. expected semicolon) are reported.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Root(
	grammarID syntaxa.GrammarLabel,
	nodeKind TNodeKind,
	mustConsume bool,
	rules ...Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	name := r.sharedCore.createRuleName("Root", grammarID)
	var expectedLabel string
	if len(rules) > 0 {
		expectedLabel = r.expectedLabelFromRule(rules[0])
	} else {
		expectedLabel = string(grammarID)
	}
	identity := r.sharedCore.createRuleIdentity(name, grammarID, expectedLabel)

	grammar := r.buildConcatGrammarFromRules(grammarID, rules)
	grammar.OutputNodeKind = &nodeKind

	exec := func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
		node := ctx.Editor.NewNode(nodeKind)
		isCommitted := false

		for _, rule := range rules {
			result := ctx.ExecuteRule(rule, syntaxa.ExecutionNormal)

			if result.Failed() {
				if isCommitted && result.Kind == syntaxa.FailureNoMatch {
					peeked := ctx.Token.Peek(0)
					msg := fmt.Sprintf("expected %s", rule.GetExpectedLabel())

					if lastLex, ok := ctx.GetLastConsumedLexeme(); ok {
						ctx.Error.ReportAtEnd(string(identity.RuleName), lastLex, msg)
					} else {
						ctx.Error.ReportAt(string(identity.RuleName), peeked, msg)
					}
				}

				return r.handleRootFailure(node, result, isCommitted)
			}

			ctx.Editor.AttachResult(node, result)
			isCommitted = true
		}

		return r.sharedCore.buildSuccessRuleResult(node)
	}

	return r.sharedCore.constructRule(
		identity,
		r.sharedCore.createContract(mustConsume, true),
		exec,
		nil,
		nil,
		grammar,
	)
}

func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) handleRootFailure(
	node *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind],
	result Result[TObservation, TToken, TTokenRole, TNodeKind],
	isCommitted bool,
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	failKind := result.Kind

	if isCommitted && failKind == syntaxa.FailureNoMatch {
		failKind = syntaxa.FailureError
	}

	return Result[TObservation, TToken, TTokenRole, TNodeKind]{
		Node:             node,
		Succeeded:        false,
		Kind:             failKind,
		ConsumeSyncToken: result.ConsumeSyncToken,
	}
}

/*
Sequence matches a strict sequence of rules and attaches all non-nil child nodes under a new node of nodeKind.

Semantics:
- First rule may fail with FailureNoMatch (grammar boundary); the sequence then fails with NoMatch.
- Once any rule has succeeded, the sequence is committed; any later failure is a syntax error (FailureError).
- On success, a node of nodeKind is created and each rule's result node (if non-nil) is attached in order.

Use cases:
- Fixed-order productions (e.g. "keyword identifier semicolon").
- Composing token and sub-rule matches into one structural node.

Prerequisites:
- grammarID identifies this production.
- nodeKind is the LST kind for the container node.
- rules must not be empty (empty sequence is not useful).

Edge cases:
- First rule FailureNoMatch: entire Sequence fails with FailureNoMatch.
- Any rule FailureError or later FailureNoMatch: Sequence fails with FailureError.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Sequence(
	grammarID syntaxa.GrammarLabel,
	nodeKind TNodeKind,
	rules ...Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := r.sharedCore.createRuleName("Sequence", grammarID)
	identity := r.sharedCore.createRuleIdentity(name, grammarID, r.expectedLabelFromRule(rules[0]))
	return r.listCore(identity, nodeKind, nil, rules...)
}

/*
Block is like Sequence but adds blockEndToken to the recovery set so the parser can resync at the block end.

You must still include a rule that matches the block end token (e.g. ExpectVirtual(grammarID, blockEndToken)) in the rules list. Block only adds recovery; it does not implicitly consume the end token.

Use cases:
- Braced or bracketed blocks (e.g. { ... } or [ ... ]) with improved error recovery.
- Any sequence where a known closing token should be used for resynchronization.

Prerequisites:
- grammarID, nodeKind, and rules as in Sequence.
- blockEndToken is the token used for recovery; typically the closing brace/bracket.

Edge cases:
- Same as Sequence for success/failure; recovery attempts will consider blockEndToken.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Block(
	grammarID syntaxa.GrammarLabel,
	nodeKind TNodeKind,
	blockEndToken TToken,
	rules ...Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := r.sharedCore.createRuleName("Block", grammarID)
	expectedLabel := ""
	if len(rules) > 0 {
		expectedLabel = r.expectedLabelFromRule(rules[0])
	}
	if expectedLabel == "" {
		expectedLabel = r.sharedCore.tokenFormatter(blockEndToken)
	}
	if expectedLabel == "" {
		expectedLabel = string(grammarID)
	}
	identity := r.sharedCore.createRuleIdentity(name, grammarID, expectedLabel)
	return r.listCore(identity, nodeKind, []TToken{blockEndToken}, rules...)
}

/*
Path matches: <prefixToken> <separatorToken> <elementToken> { <separatorToken> <elementToken> }
The separator is mandatory and non-trailing.

All grammar labels (prefix, separator, element, tail) are supplied by the caller; syntaxa does not
invent or concatenate labels. Use ScopedBuilder.Path for a scope-bound API that derives prefix/sep/tail via Sub.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Path(
	grammarID syntaxa.GrammarLabel,
	nodeKind TNodeKind,
	prefixGrammarLabel syntaxa.GrammarLabel,
	prefixNodeKind TNodeKind,
	prefixToken TToken,
	separatorGrammarLabel syntaxa.GrammarLabel,
	separatorToken TToken,
	elementGrammarLabel syntaxa.GrammarLabel,
	elementNodeKind TNodeKind,
	elementToken TToken,
	tailGrammarLabel syntaxa.GrammarLabel,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	segment := r.TransparentSequence(
		separatorGrammarLabel,
		r.token.ExpectVirtual(separatorGrammarLabel, separatorToken),
		r.token.Expect(elementGrammarLabel, elementNodeKind, elementToken),
	)

	return r.Sequence(
		grammarID,
		nodeKind,
		r.token.Expect(prefixGrammarLabel, prefixNodeKind, prefixToken),
		r.Required(
			segment,
			fmt.Sprintf("expected %s followed by identifier", r.sharedCore.tokenFormatter(separatorToken)),
		),
		r.TransparentZeroOrMore(tailGrammarLabel, segment),
	)
}

/*
Path builds a Path rule in this scope. The rule ID is the scope base; prefix, separator, and tail
labels are derived via Sub; elementGrammarLabel is supplied by the client so overrides (e.g. editor
scope) can target the element by the same label.
*/
func (b *ScopedBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Path(
	nodeKind TNodeKind,
	prefixNodeKind TNodeKind,
	prefixToken TToken,
	separatorToken TToken,
	elementGrammarLabel syntaxa.GrammarLabel,
	elementNodeKind TNodeKind,
	elementToken TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return b.rb.Rule.Path(
		b.base,
		nodeKind,
		b.Sub("prefix"),
		prefixNodeKind,
		prefixToken,
		b.Sub("sep"),
		separatorToken,
		elementGrammarLabel,
		elementNodeKind,
		elementToken,
		b.Sub("tail"),
	)
}

/*
listCore builds a rule that matches a strict sequence of rules and attaches all non-nil child nodes under a new node of nodeKind.

It is shared by Sequence and Block. Recovery tokens (e.g. blockEndToken) are optional; when provided, the rule is marked for resync at those boundaries.

Time complexity: O(r) where r is the number of rules; each rule runs once.
Space complexity: O(r) for the results slice and grammar children.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) listCore(
	identity syntaxa.RuleIdentity,
	nodeKind TNodeKind,
	recovery []TToken,
	rules ...Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	mustConsume := r.listCoreMustConsume(rules)
	grammar := r.buildConcatGrammarFromRules(identity.GrammarLabel, rules)
	grammar.OutputNodeKind = &nodeKind

	exec := func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
		results := make([]Result[TObservation, TToken, TTokenRole, TNodeKind], len(rules))
		startMarker := ctx.Token.PeekRaw(0).TokenNumber

		for i, rule := range rules {
			result := ctx.ExecuteRule(rule, syntaxa.ExecutionNormal)

			if result.Failed() {
				currentMarker := ctx.Token.PeekRaw(0).TokenNumber
				effectiveKind := sequenceCommitmentFailureKind(startMarker, currentMarker, result.Kind)

				if effectiveKind == syntaxa.FailureError && result.Kind == syntaxa.FailureNoMatch {
					peeked := ctx.Token.Peek(0)

					if inserted, fakeResult := r.tryFollowSetInsertion(ctx, rule, identity, peeked); inserted {
						results[i] = fakeResult
						continue
					}

					msg := fmt.Sprintf("expected %s", rule.GetExpectedLabel())
					if lastLex, ok := ctx.GetLastConsumedLexeme(); ok {
						ctx.Error.ReportAtEnd(string(identity.RuleName), lastLex, msg)
					} else {
						ctx.Error.ReportAt(string(identity.RuleName), peeked, msg)
					}
				}

				return r.sharedCore.buildFailureRuleResult(nil, effectiveKind)
			}
			results[i] = result
		}

		node := ctx.Editor.NewNode(nodeKind)
		r.attachResultsToNode(ctx, node, results)
		return r.sharedCore.buildSuccessRuleResult(node)
	}

	return r.sharedCore.constructRule(identity, r.sharedCore.createContract(mustConsume, true), exec, recovery, nil, grammar).WithRecoveryBarrier()
}

/*
listCoreMustConsume returns true if any rule in the list has MustConsume set.
Used by listCore to derive the sequence contract.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) listCoreMustConsume(
	rules []Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) bool {
	for i := range rules {
		if rules[i].GetContract().MustConsume {
			return true
		}
	}
	return false
}

/*
ensureMinNonNegative panics with a message including ruleName if min < 0.
Used by NOrMore and TransparentNOrMore.
*/
func ensureMinNonNegative(min int, ruleName string) {
	if min < 0 {
		panic(ruleName + ": min must be >= 0")
	}
}

/*
expectedLabelFromRule returns a non-empty expected label for use in createRuleIdentity.
Returns rule.GetExpectedLabel(); if empty, returns string(rule.GetGrammarLabel()).
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) expectedLabelFromRule(
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) string {
	if label := rule.GetExpectedLabel(); label != "" {
		return label
	}
	return string(rule.GetGrammarLabel())
}

/*
choiceExpectedLabel builds a non-empty expected label for Choice from its rules.
Returns "one of: " + joined labels from rules, or string(grammarID) if no labels.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) choiceExpectedLabel(
	rules []Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	grammarID syntaxa.GrammarLabel,
) string {
	parts := make([]string, 0, len(rules))
	for _, rule := range rules {
		parts = append(parts, r.expectedLabelFromRule(rule))
	}
	joined := strings.Join(parts, ", ")
	if joined != "" {
		return "one of: " + joined
	}
	return string(grammarID)
}

/*
sequenceCommitmentFailureKind returns the failure kind to surface when a sub-rule fails in a sequence or committed context.

Commitment is determined by whether the lexer progressed: if the token position did not advance (startTokenNumber == currentTokenNumber) and the failure was NoMatch, the sequence is not committed and FailureNoMatch is returned. Otherwise the sequence is committed and FailureError is returned.

Use this whenever a production has logically "started" (e.g. after consuming a token or running a sub-rule that could consume) and a subsequent failure must be classified as grammar boundary (NoMatch) vs syntax error (Error). Do not use rule indices to infer commitment; use token position.
*/
func sequenceCommitmentFailureKind(
	startTokenNumber, currentTokenNumber int,
	failureKind syntaxa.FailureKind,
) syntaxa.FailureKind {
	if failureKind == syntaxa.FailureNoMatch && startTokenNumber == currentTokenNumber {
		return syntaxa.FailureNoMatch
	}
	return syntaxa.FailureError
}

/*
buildConcatGrammarFromRules builds a Concat grammar from the given rules' grammars and marks it as a context boundary.
Used by Root and listCore.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) buildConcatGrammarFromRules(
	grammarID syntaxa.GrammarLabel,
	rules []Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) *syntaxa.Grammar[TToken, TNodeKind] {
	children := r.grammarsFromRules(rules)
	grammar := syntaxa.Concat(grammarID, children...)
	syntaxa.MarkAsContextBoundary(grammar)
	return grammar
}

/*
grammarsFromRules returns the grammar IR nodes for the given rules in order.
Used to build Concat grammar in Root, Sequence, and Block.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) grammarsFromRules(
	rules []Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) []*syntaxa.Grammar[TToken, TNodeKind] {
	children := make([]*syntaxa.Grammar[TToken, TNodeKind], len(rules))
	for i, rule := range rules {
		children[i] = rule.GetGrammar()
	}
	return children
}

/*
attachResultsToNode attaches each non-nil result node to parent.
Fragment results are unpacked: their children are detached and attached directly to parent.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) attachResultsToNode(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	parent *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind],
	results []Result[TObservation, TToken, TTokenRole, TNodeKind],
) {
	for _, result := range results {
		ctx.Editor.AttachResult(parent, result)
	}
}

/*
runRepetitionLoop executes rule repeatedly until FailureNoMatch or FailureError with no progress.
On each success, the result's node is attached to container. Returns count of matches; if
FailureError with no progress occurs, hasError is true and errResult is the failure to return.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) runRepetitionLoop(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	min int,
	container *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind],
) (count int, errResult Result[TObservation, TToken, TTokenRole, TNodeKind], hasError bool) {
	for {
		before := ctx.Token.PeekRaw(0)
		result := ctx.ExecuteRule(rule, syntaxa.ExecutionNormal)

		if result.Succeeded {
			ctx.Editor.AttachResult(container, result)
			count++
			continue
		}

		switch result.Kind {
		case syntaxa.FailureNoMatch:
			return count, errResult, false

		case syntaxa.FailureError:
			after := ctx.Token.PeekRaw(0)
			noProgress := before.TokenNumber == after.TokenNumber
			leaveSyncForParent := !result.ConsumeSyncToken
			if noProgress || leaveSyncForParent {
				return count, result, true
			}
			continue
		}
	}
}

/*
constructNOrMoreRule builds a repetition rule with the given container creation and success result builders.
Shared by NOrMore and TransparentNOrMore to avoid duplicated grammar setup and loop handling.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) constructNOrMoreRule(
	ruleName string,
	grammarID syntaxa.GrammarLabel,
	min int,
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	outputNodeKind *TNodeKind,
	makeContainer func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind],
	onSuccess func(container *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	ensureMinNonNegative(min, ruleName)

	grammar := syntaxa.Repeat[TToken, TNodeKind](grammarID, rule.GetGrammar(), min, nil)
	syntaxa.MarkAsContextBoundary(grammar)
	if outputNodeKind != nil {
		grammar.OutputNodeKind = outputNodeKind
	}

	name := r.sharedCore.createRuleName(ruleName, grammarID)
	identity := r.sharedCore.createRuleIdentity(name, grammarID, rule.GetExpectedLabel())
	mustConsume := min > 0 && rule.GetContract().MustConsume

	return r.sharedCore.constructRule(
		identity,
		r.sharedCore.createContract(mustConsume, true),
		func(ctx *syntaxa.ExecRuleContext[
			TObservation, TToken, TTokenRole, TLexerState, TNodeKind,
		]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
			container := makeContainer(ctx)
			count, errResult, hasError := r.runRepetitionLoop(ctx, rule, min, container)
			if hasError {
				return errResult
			}
			if count < min {
				return r.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureNoMatch)
			}
			return onSuccess(container)
		},
		rule.GetRecoveryTokens(),
		nil,
		grammar,
	)
}

/*
NOrMore matches the inner rule at least min times, then as many times as it succeeds.

Semantics:
- Success: attach the rule's node to the container and continue.
- FailureNoMatch: stop repetition and succeed if count >= min; otherwise fail with FailureNoMatch.
- FailureError: recovery has already run. If the token position advanced, retry the repetition; if not, propagate the error (guards against infinite loops).

Use cases:
- OneOrMore (min=1) or ZeroOrMore (min=0) style repetition.
- Lists of elements without explicit separators (e.g. repeated declarations).

Prerequisites:
- grammarID identifies this production.
- nodeKind is the LST kind for the container node.
- min must be >= 0 (panics otherwise).
- rule must be a valid parser rule.

Edge cases:
- min is 0: may succeed with empty container node (zero matches).
- After FailureError, progress is checked via token position; no progress means immediate propagation.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) NOrMore(
	grammarID syntaxa.GrammarLabel,
	nodeKind TNodeKind,
	min int,
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return r.constructNOrMoreRule(
		"NOrMore",
		grammarID,
		min,
		rule,
		&nodeKind,
		func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind] {
			return ctx.Editor.NewNode(nodeKind)
		},
		func(container *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
			return r.sharedCore.buildSuccessRuleResult(container)
		},
	)
}

/*
ZeroOrMore matches the rule zero or more times.

It is equivalent to NOrMore(grammarID, nodeKind, 0, rule). On success, a node of nodeKind is created and each successful match's node is attached; zero matches yields an empty container node.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) ZeroOrMore(
	grammarID syntaxa.GrammarLabel,
	nodeKind TNodeKind,
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return r.NOrMore(grammarID, nodeKind, 0, rule)
}

/*
OneOrMore matches the rule one or more times.

It is equivalent to NOrMore(grammarID, nodeKind, 1, rule). Fails with FailureNoMatch if the rule does not match at least once.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) OneOrMore(
	grammarID syntaxa.GrammarLabel,
	nodeKind TNodeKind,
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return r.NOrMore(grammarID, nodeKind, 1, rule)
}

func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) TransparentNOrMore(
	grammarID syntaxa.GrammarLabel,
	min int,
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	var zeroKind TNodeKind
	return r.constructNOrMoreRule(
		"TransparentNOrMore",
		grammarID,
		min,
		rule,
		nil,
		func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind] {
			return ctx.Editor.NewTransientNode(zeroKind)
		},
		func(container *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
			return r.sharedCore.buildFragmentRuleResult(container)
		},
	)
}

func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) TransparentZeroOrMore(
	grammarID syntaxa.GrammarLabel,
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return r.TransparentNOrMore(grammarID, 0, rule)
}

/*
Nest matches openToken, then the inner rule, then closeToken, and wraps the inner result in a node of nodeKind.

Semantics:
- Requires openToken; if missing, fails with FailureNoMatch.
- Executes innerRule; on failure, propagates the failure.
- Requires closeToken; if missing after inner rule, reports a syntax error and fails with FailureError.
- Builds a node of nodeKind and attaches the inner rule's node (if non-nil) as its only child.
- closeToken is used as the recovery boundary.

Use cases:
- Parenthesized expressions, braced blocks, or any balanced delimiter pair.
- Grouping without changing the inner rule's LST shape beyond wrapping.

Prerequisites:
- grammarID identifies this production.
- nodeKind is the LST kind for the wrapper node.
- openToken and closeToken are the delimiter pair.
- innerRule is the rule for the content between the delimiters.

Edge cases:
- openToken mismatch: FailureNoMatch (no diagnostic).
- closeToken mismatch after inner success: syntax error and FailureError.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Nest(
	grammarID syntaxa.GrammarLabel,
	nodeKind TNodeKind,
	openToken, closeToken TToken,
	innerRule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	name := r.sharedCore.createRuleName("Nest", grammarID)
	expectedLabel := r.sharedCore.tokenFormatter(closeToken)
	if expectedLabel == "" {
		expectedLabel = string(grammarID)
	}
	identity := r.sharedCore.createRuleIdentity(name, grammarID, expectedLabel)

	// ---------------------------
	// Build grammar IR
	// ---------------------------

	grammar := syntaxa.Nest(
		grammarID,
		openToken,
		closeToken,
		innerRule.GetGrammar(),
	)
	syntaxa.MarkAsContextBoundary(grammar)
	grammar.OutputNodeKind = &nodeKind

	// ---------------------------
	// Runtime execution
	// ---------------------------

	exec := func(ctx *syntaxa.ExecRuleContext[
		TObservation, TToken, TTokenRole, TLexerState, TNodeKind,
	]) Result[TObservation, TToken, TTokenRole, TNodeKind] {

		// ---- Expect OPEN token ----

		open := ctx.Token.Peek(0)
		if open.Token != openToken {
			return r.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureNoMatch)
		}
		ctx.Token.Consume()

		// ---- Execute inner rule ----

		innerResult := r.executeInnerWithCommitmentFailure(ctx, innerRule)
		if innerResult.Failed() {
			return innerResult
		}

		// ---- Expect CLOSE token ----

		closeLex := ctx.Token.Peek(0)
		if closeLex.Token != closeToken {
			msg := r.sharedCore.formatUnexpectedExpected(closeLex.Token, closeToken)

			if lastLex, ok := ctx.GetLastConsumedLexeme(); ok {
				ctx.Error.ReportAtEnd(string(name), lastLex, msg)
			} else {
				ctx.Error.ReportAt(string(name), closeLex, msg)
			}

			return r.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
		}
		ctx.Token.Consume()

		// ---- Construct wrapping node ----

		node := ctx.Editor.NewNode(nodeKind)

		ctx.Editor.AttachResult(node, innerResult)
		return r.sharedCore.buildSuccessRuleResult(node)
	}

	// Must consume because open + close must exist
	mustConsume := true

	return r.sharedCore.constructRule(
		identity,
		r.sharedCore.createContract(mustConsume, true),
		exec,
		[]TToken{closeToken}, // recovery boundary
		nil,
		grammar,
	).WithRecoveryBarrier()
}

func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) TransparentNest(
	grammarID syntaxa.GrammarLabel,
	openToken, closeToken TToken,
	innerRule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := r.sharedCore.createRuleName("TransparentNest", grammarID)
	expectedLabel := r.sharedCore.tokenFormatter(closeToken)
	if expectedLabel == "" {
		expectedLabel = string(grammarID)
	}
	identity := r.sharedCore.createRuleIdentity(name, grammarID, expectedLabel)

	grammar := syntaxa.Nest(grammarID, openToken, closeToken, innerRule.GetGrammar())
	syntaxa.MarkAsContextBoundary(grammar)

	exec := func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {

		if !r.consumeIfMatch(ctx, openToken) {
			return r.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureNoMatch)
		}

		innerResult := r.executeInnerWithCommitmentFailure(ctx, innerRule)
		if innerResult.Failed() {
			return innerResult
		}

		if !r.enforceToken(ctx, name, closeToken) {
			return r.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
		}

		return innerResult
	}

	return r.sharedCore.constructRule(
		identity,
		r.sharedCore.createContract(innerRule.GetContract().MustConsume, innerRule.GetContract().MustReturnNode),
		exec,
		[]TToken{closeToken},
		nil,
		grammar,
	).WithRecoveryBarrier()
}

/*
Reference creates a late-binding proxy rule that resolves and executes its target at runtime.

This rule acts as a transparent thunk. It defines a graph edge at compile-time
and resolves the actual execution logic dynamically during parsing.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Reference(
	grammarID syntaxa.GrammarLabel,
	target syntaxa.GrammarLabel,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	name := r.sharedCore.createRuleName("Reference", grammarID)
	expectedLabel := string(target)
	if expectedLabel == "" {
		expectedLabel = string(grammarID)
	}
	identity := r.sharedCore.createRuleIdentity(name, grammarID, expectedLabel)

	grammar := syntaxa.Ref[TToken, TNodeKind](grammarID, target)
	// syntaxa.MarkAsContextBoundary(grammar)

	exec := func(ctx *syntaxa.ExecRuleContext[
		TObservation, TToken, TTokenRole, TLexerState, TNodeKind,
	]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
		result := ctx.ExecuteReference(target, syntaxa.ExecutionNormal)
		return result
	}

	contract := r.sharedCore.createContract(false, false)

	return r.sharedCore.constructRule(
		identity,
		contract,
		exec,
		nil,
		nil,
		grammar,
	)
}

/*
RecoverSync wraps a rule and adds specific tokens to its recovery set.
This ensures that if the rule (or its children) fails, the parser knows
it can safely resync at these boundaries.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) RecoverSync(
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	tokens ...TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	// Merge existing recovery tokens with new ones
	currentRecovery := rule.GetRecoveryTokens()
	newRecovery := make([]TToken, len(currentRecovery)+len(tokens))
	copy(newRecovery, currentRecovery)
	copy(newRecovery[len(currentRecovery):], tokens)

	return r.sharedCore.constructRule(
		rule.GetIdentity(),
		rule.GetContract(),
		func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
			return ctx.ExecuteRule(rule, syntaxa.ExecutionNormal)
		},
		newRecovery,
		nil,
		rule.GetGrammar(),
	)
}

/*
RecoverSyncBoundary is like RecoverSync but treats noConsumeTokens as sync points that are not consumed after recovery.

Use when the list body is inside a delimited block (e.g. { ... }): include the closing delimiter in noConsumeTokens so the parent nest can consume it and the real syntax error (e.g. missing semicolon) is reported instead of "expected closing brace".
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) RecoverSyncBoundary(
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	syncTokens []TToken,
	noConsumeTokens []TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	currentRecovery := rule.GetRecoveryTokens()
	newRecovery := make([]TToken, 0, len(currentRecovery)+len(syncTokens)+len(noConsumeTokens))
	newRecovery = append(newRecovery, currentRecovery...)
	newRecovery = append(newRecovery, syncTokens...)
	newRecovery = append(newRecovery, noConsumeTokens...)
	return r.sharedCore.constructRule(
		rule.GetIdentity(),
		rule.GetContract(),
		func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
			return ctx.ExecuteRule(rule, syntaxa.ExecutionNormal)
		},
		newRecovery,
		noConsumeTokens,
		rule.GetGrammar(),
	)
}

/*
executeInnerWithCommitmentFailure runs innerRule in normal mode and, on failure, returns a failure result
with the commitment-aware failure kind (from sequenceCommitmentFailureKind). Use after the production has
logically started (e.g. after consuming an open token) so that commitment is based on lexer progress.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) executeInnerWithCommitmentFailure(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	innerRule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	startMarker := ctx.Token.PeekRaw(0).TokenNumber
	result := ctx.ExecuteRule(innerRule, syntaxa.ExecutionNormal)
	if result.Failed() {
		currentMarker := ctx.Token.PeekRaw(0).TokenNumber
		effectiveKind := sequenceCommitmentFailureKind(startMarker, currentMarker, result.Kind)
		return r.sharedCore.buildFailureRuleResult(nil, effectiveKind)
	}
	return result
}

/*
consumeIfMatch consumes the current token if it equals expected and returns true; otherwise returns false without consuming.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) consumeIfMatch(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	expected TToken,
) bool {
	if ctx.Token.Peek(0).Token == expected {
		ctx.Token.Consume()
		return true
	}
	return false
}

/*
enforceToken consumes the current token if it equals expected and returns true; otherwise reports a syntax error and returns false.
The error is reported at the end of the unexpected token (so the position is the column after it).
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) enforceToken(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	ruleName syntaxa.RuleLabel,
	expected TToken,
) bool {
	found := ctx.Token.Peek(0)
	if found.Token == expected {
		ctx.Token.Consume()
		return true
	}

	msg := r.sharedCore.formatUnexpectedExpected(found.Token, expected)
	ctx.Error.ReportAtEnd(string(ruleName), found, msg)
	return false
}

/*
Prefixed matches a specific prefix token, discards it, and returns the result of the inner rule.

Use cases:
- Matching virtual prefixes where the LST node is entirely defined by the inner rule.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Prefixed(
	grammarID syntaxa.GrammarLabel,
	prefixToken TToken,
	innerRule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := r.sharedCore.createRuleName("Prefixed", grammarID)
	identity := r.sharedCore.createRuleIdentity(name, grammarID, innerRule.GetExpectedLabel())

	grammar := syntaxa.Concat[TToken, TNodeKind](
		grammarID,
		syntaxa.Token[TToken, TNodeKind](grammarID, prefixToken),
		innerRule.GetGrammar(),
	)
	syntaxa.MarkAsContextBoundary(grammar)

	return r.sharedCore.constructRule(
		identity,
		r.sharedCore.createContract(true, innerRule.GetContract().MustReturnNode),
		r.executePrefixedRule(prefixToken, innerRule),
		innerRule.GetRecoveryTokens(),
		nil,
		grammar,
	)
}

func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) executePrefixedRule(
	prefixToken TToken,
	innerRule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) syntaxa.ParserRuleExecutor[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	return func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
		if !r.consumeIfMatch(ctx, prefixToken) {
			return r.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureNoMatch)
		}

		return r.executeInnerWithCommitmentFailure(ctx, innerRule)
	}
}

/*
Choice tries a series of rules in order and returns the result of the first successful one.

Semantics:
- Executes rules sequentially in ExecutionNormal mode.
- If a rule succeeds, Choice succeeds immediately and returns that rule's result.
- If a rule fails with FailureNoMatch, it proceeds to the next rule (backtracking).
- If a rule fails with FailureError (a committed syntax error), Choice fails immediately and propagates the error.
- If all rules fail with FailureNoMatch, Choice fails with FailureNoMatch.

Use cases:
- Alternation in productions (e.g., matching different types of statements or expressions).

Prerequisites:
- grammarID identifies this production.
- rules must contain at least one valid parser rule.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Choice(
	grammarID syntaxa.GrammarLabel,
	rules ...Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	name := r.sharedCore.createRuleName("Choice", grammarID)
	expectedLabel := r.choiceExpectedLabel(rules, grammarID)
	identity := r.sharedCore.createRuleIdentity(name, grammarID, expectedLabel)

	grammar := r.buildChoiceGrammarFromRules(grammarID, rules)

	exec := func(ctx *syntaxa.ExecRuleContext[
		TObservation, TToken, TTokenRole, TLexerState, TNodeKind,
	]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
		return r.executeChoiceLoop(ctx, rules)
	}

	mustConsume := r.choiceMustConsume(rules)

	return r.sharedCore.constructRule(
		identity,
		r.sharedCore.createContract(mustConsume, false),
		exec,
		nil,
		nil,
		grammar,
	)
}

/*
buildChoiceGrammarFromRules builds a Choice grammar from the given rules and marks it as a context boundary.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) buildChoiceGrammarFromRules(
	grammarID syntaxa.GrammarLabel,
	rules []Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) *syntaxa.Grammar[TToken, TNodeKind] {
	children := r.grammarsFromRules(rules)
	grammar := syntaxa.Choice[TToken, TNodeKind](grammarID, children...)
	syntaxa.MarkAsContextBoundary(grammar)
	return grammar
}

/*
executeChoiceLoop iterates through the rules, handling match successes, benign mismatches, and fatal errors.
Extracted to enforce strict single-level nesting and low cognitive complexity.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) executeChoiceLoop(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	rules []Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	for _, rule := range rules {
		result := ctx.ExecuteRule(rule, syntaxa.ExecutionNormal)

		if result.Succeeded {
			return result
		}

		if result.Kind == syntaxa.FailureError {
			return result
		}
	}

	return r.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureNoMatch)
}

/*
choiceMustConsume verifies if the overall Choice rule is guaranteed to consume a token.
It returns true only if every single sub-rule strictly requires consumption.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) choiceMustConsume(
	rules []Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) bool {
	if len(rules) == 0 {
		return false
	}

	for _, rule := range rules {
		if !rule.GetContract().MustConsume {
			return false
		}
	}

	return true
}

/*
Define promotes any existing rule to a context boundary and registers it.

Use this when you construct a rule from low-level primitives (like ExpectPair)
but need it to serve as a targetable node in the global graph for GReference.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Define(
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	grammar := rule.GetGrammar()

	if grammar != nil {
		syntaxa.MarkAsContextBoundary(grammar)
		r.sharedCore.registry[rule.GetIdentity().GrammarLabel] = rule
	}

	return rule
}

/*
tryFollowSetInsertion checks if the current lexer token is a mathematically valid
continuation for the rule that just failed. If safe, it synthesizes a ghost result,
reports the missing token, and returns true to keep the sequence alive.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) tryFollowSetInsertion(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	identity syntaxa.RuleIdentity,
	peeked lexarch.Lexeme[TObservation, TToken, TTokenRole],
) (bool, Result[TObservation, TToken, TTokenRole, TNodeKind]) {
	analysis := ctx.GetAnalysis()
	if analysis == nil || rule.GetGrammar() == nil || rule.GetGrammar().NodePath == nil {
		return false, Result[TObservation, TToken, TTokenRole, TNodeKind]{}
	}

	ruleKey := syntaxa.NodeKeyFromPath(*rule.GetGrammar().NodePath)

	// If the actual token is NOT in the follow set, insertion is unsafe (leads to loops).
	if _, safe := analysis.Follow[ruleKey][peeked.Token]; !safe {
		return false, Result[TObservation, TToken, TTokenRole, TNodeKind]{}
	}

	// 1. Report the error (Token is missing)
	msg := fmt.Sprintf("missing %s", rule.GetExpectedLabel())
	if lastLex, ok := ctx.GetLastConsumedLexeme(); ok {
		ctx.Error.ReportAtEnd(string(identity.RuleName), lastLex, msg)
	} else {
		ctx.Error.ReportAt(string(identity.RuleName), peeked, msg)
	}

	// 2. Synthesize the ghost node if required
	var fakeNode *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind]
	if rule.GetContract().MustReturnNode {
		var zeroKind TNodeKind
		fakeNode = ctx.Editor.NewTransientNode(zeroKind)
	}

	return true, r.sharedCore.buildSuccessRuleResult(fakeNode)
}

// ------------------------------------------------------------- RULEBUILDER

/*
RuleBuilder is the rule factory entry point: it provides Token, Rule, and Pratt endpoints for building parser rules.

Use Token for rules that match lexer tokens (Expect, ExpectVirtual, ExpectOneOf, List). Use Rule for rules that combine other rules (Sequence, Block, Optional, NOrMore, Nest, Root). Use Pratt for precedence-climbing expression rules (Expression). All endpoints share the same type parameters and token formatter; rules from any can be composed together.

For common grammar patterns, build rules via this type. For behaviour not covered by the factory, build rules manually with syntaxa.ParserRuleCreate and the syntaxa grammar IR.
*/
type RuleBuilder[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	/* Token exposes token-level rules: Expect, ExpectVirtual, ExpectOneOf, List. */
	Token *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]

	/* Rule exposes composite rules: Sequence, Block, Optional, Required, NOrMore, Nest, Root, etc. */
	Rule *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]

	/* Pratt exposes Pratt-style expression rules: Expression (precedence-climbing). */
	Pratt *prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]

	sharedCore *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
}

/*
ScopedBuilder is a builder bound to a specific grammar scope (base ID). Use Scope(grammarID) on RuleBuilder
to obtain one. Sub(name) derives a hierarchical sub-scope from the base so syntaxa never invents labels;
Path and other methods use the base and derived labels as provided by the client or Sub.
*/
type ScopedBuilder[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	base syntaxa.GrammarLabel
	rb   *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
}

/*
Scope returns a ScopedBuilder bound to grammarID. All rules built from it (e.g. Path) use this ID as the
rule identity; sub-parts use Sub(name) for hierarchical identities so the client controls the label contract.
*/
func (rb *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Scope(grammarID syntaxa.GrammarLabel) *ScopedBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return &ScopedBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{base: grammarID, rb: rb}
}

/*
GetRegistry returns the rule registry maintained by the builder for the core engine.

The registry maps GrammarLabel to ParserRule for every context-boundary rule built through this
builder. Clients pass it to the parser so that references (e.g. GReference) resolve at runtime.
*/
func (rb *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) GetRegistry() syntaxa.RuleRegistry[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return rb.sharedCore.registry
}

/*
GetDefinedGrammars exports the structural IR of all defined context boundaries.

Pass this into syntaxa.ProducePackage as the 'additionalRules' parameter
so the analysis phase can compute FIRST/FOLLOW sets for disconnected sub-graphs.
Includes auxiliary grammars (e.g. Pratt level roots) so GReference targets resolve.
*/
func (rb *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) GetDefinedGrammars() []*syntaxa.Grammar[TToken, TNodeKind] {
	// 1. Extract and sort registry keys for strict determinism
	labels := make([]string, 0, len(rb.sharedCore.registry))
	for label := range rb.sharedCore.registry {
		labels = append(labels, string(label))
	}
	slices.Sort(labels)

	grammars := make([]*syntaxa.Grammar[TToken, TNodeKind], 0, len(labels)+len(rb.sharedCore.auxiliaryGrammars))

	// 2. Append registry grammars in deterministic order
	for _, labelStr := range labels {
		label := syntaxa.GrammarLabel(labelStr)
		g := rb.sharedCore.registry[label]
		if gr := g.GetGrammar(); gr != nil {
			grammars = append(grammars, gr)
		}
	}

	// 3. Append auxiliary grammars (already deterministic from slice appends)
	grammars = append(grammars, rb.sharedCore.auxiliaryGrammars...)
	return grammars
}

/*
Sub returns a derived GrammarLabel for a named sub-scope under the builder's base. Derivation is in one
place so hierarchical identities are predictable and syntaxa does not invent or concatenate labels elsewhere.
*/
func (b *ScopedBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Sub(name string) syntaxa.GrammarLabel {
	if b.base == "" {
		return syntaxa.GrammarLabel(name)
	}
	return syntaxa.GrammarLabel(string(b.base) + " " + name)
}

/*
RuleBuilderCreate allocates and returns a new RuleBuilder with the given token formatter.

The tokenFormatter is used when building error messages that mention token values (e.g. "expected one of ID, COMMA"). It should return a short, readable string for each token (e.g. token ID or name). The returned RuleBuilder is ready to use: RuleBuilder.Token, RuleBuilder.Rule, and RuleBuilder.Pratt are non-nil.

Prerequisites:
- tokenFormatter must not be nil; it is used for diagnostics and expected-label formatting.

Edge cases:
- tokenFormatter may be called with any token value that appears in rules built from this builder.
*/
func RuleBuilderCreate[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable](
	tokenFormatter func(token TToken) string,
) *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	sharedCore := &sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{
		tokenFormatter: tokenFormatter,
		registry:       make(syntaxa.RuleRegistry[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]),
	}

	tokenEndpoint := &tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{
		sharedCore: sharedCore,
	}

	ruleEndpoint := &ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{
		sharedCore: sharedCore,
		token:      tokenEndpoint,
	}

	prattEndpoint := &prattEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{
		sharedCore: sharedCore,
	}

	return &RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{
		Token:      tokenEndpoint,
		Rule:       ruleEndpoint,
		Pratt:      prattEndpoint,
		sharedCore: sharedCore,
	}
}

// ------------------------------------------------------------- PRIVATE HELPERS

/*
sharedCore holds the token formatter, the rule registry for the core engine, and rule construction
helpers: result builders, identity/contract creation, and constructRule variants (structural, skipping,
optional, virtual). Context-boundary rules are automatically added to the registry when constructed.
Auxiliary grammars (e.g. Pratt level roots) are appended for ProducePackage so refs resolve.
*/
type sharedCore[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	tokenFormatter    func(token TToken) string
	registry          syntaxa.RuleRegistry[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
	auxiliaryGrammars []*syntaxa.Grammar[TToken, TNodeKind]
}

/*
formatUnexpectedExpected returns the standard "unexpected X, expected Y" message.
Used by reportMismatch, enforceToken, and Nest close-token errors.
*/
func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) formatUnexpectedExpected(
	found, expected TToken,
) string {
	return fmt.Sprintf("unexpected %s, expected '%s'", s.formatToken(found), s.formatToken(expected))
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) formatToken(t TToken) string {
	if str := s.tokenFormatter(t); str != "" {
		return str
	}

	if str := fmt.Sprintf("%v", t); str != "" {
		return str
	}

	return fmt.Sprintf("%#v", t)
}

/*
appendAuxiliaryGrammars appends grammar roots that must be in the package (e.g. Pratt level rules)
so GReference targets resolve. Call from Pratt Expression() when using level refs.
*/
func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) appendAuxiliaryGrammars(roots []*syntaxa.Grammar[TToken, TNodeKind]) {
	s.auxiliaryGrammars = append(s.auxiliaryGrammars, roots...)
}

/*
formatTokensAsList formats the given tokens as a single string using separator between each,
via the shared token formatter. Used for expected-label and error messages.
*/
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
	node *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind],
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	return Result[TObservation, TToken, TTokenRole, TNodeKind]{
		Node:      node,
		Succeeded: true,
	}
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) buildFailureRuleResult(
	node *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind],
	failureKind syntaxa.FailureKind,
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	return Result[TObservation, TToken, TTokenRole, TNodeKind]{
		Node:             node,
		Succeeded:        false,
		Kind:             failureKind,
		ConsumeSyncToken: true,
	}
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) buildFragmentRuleResult(
	node *syntaxa.SyntaxaLSTNode[TObservation, TToken, TTokenRole, TNodeKind],
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	return Result[TObservation, TToken, TTokenRole, TNodeKind]{
		Node:       node,
		Succeeded:  true,
		IsFragment: true,
	}
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) createRuleName(
	rule string,
	grammarID syntaxa.GrammarLabel,
) syntaxa.RuleLabel {
	return syntaxa.RuleLabel(fmt.Sprintf("Rule %s (id:%s)", rule, grammarID))
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) createRuleIdentity(
	name syntaxa.RuleLabel,
	grammarID syntaxa.GrammarLabel,
	expectedLabel string,
) syntaxa.RuleIdentity {
	if expectedLabel == "" {
		panic("createRuleIdentity: expectedLabel must not be empty; use a token formatter, rule expected/grammar label, or grammar ID")
	}
	return syntaxa.RuleIdentity{
		RuleName:      name,
		GrammarLabel:  grammarID,
		ExpectedLabel: expectedLabel,
	}
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) constructStructuralRule(
	identity syntaxa.RuleIdentity,
	execution syntaxa.ParserRuleExecutor[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	recovery []TToken,
	grammar *syntaxa.Grammar[TToken, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return s.constructRule(
		identity,
		s.createContract(true, true),
		execution,
		recovery,
		nil,
		grammar,
	)
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) constructSkippingRule(
	identity syntaxa.RuleIdentity,
	execution syntaxa.ParserRuleExecutor[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	recovery []TToken,
	grammar *syntaxa.Grammar[TToken, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return s.constructRule(
		identity,
		s.createContract(true, false),
		execution,
		recovery,
		nil,
		grammar,
	)
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) constructOptionalRule(
	identity syntaxa.RuleIdentity,
	execution syntaxa.ParserRuleExecutor[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	recovery []TToken,
	grammar *syntaxa.Grammar[TToken, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return s.constructRule(
		identity,
		s.createContract(false, false),
		execution,
		recovery,
		nil,
		grammar,
	)
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) constructVirtualRule(
	identity syntaxa.RuleIdentity,
	execution syntaxa.ParserRuleExecutor[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	recovery []TToken,
	grammar *syntaxa.Grammar[TToken, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return s.constructRule(
		identity,
		s.createContract(false, true),
		execution,
		recovery,
		nil,
		grammar,
	)
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
	noConsumeOnRecovery []TToken,
	grammar *syntaxa.Grammar[TToken, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	rule := syntaxa.ParserRuleCreate(
		identity,
		execution,
		ruleContract,
		recovery,
		grammar,
		noConsumeOnRecovery,
	)
	if grammar != nil && grammar.IsContextBoundary {
		s.registry[identity.GrammarLabel] = rule
	}
	return rule
}
