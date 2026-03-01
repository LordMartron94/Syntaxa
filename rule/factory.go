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

It is the return type of rule execution: success/failure, optional AST node, and failure kind.
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

On success: consumes the token, creates an AST node of outputNodeKind, and attaches the lexeme as the node's token.
On failure: reports a syntax error and returns FailureError (no rollback of other state beyond the engine's snapshot).

Use cases:
- Matching keywords, operators, or punctuation.
- Building AST nodes for terminals when the node kind is significant.

Prerequisites:
- grammarID identifies this production for error messages and grammar IR.
- outputNodeKind is the AST node kind to create on success.
- token is the exact token value to match.

Edge cases:
- If the current token does not match, a diagnostic is reported and the rule fails.
*/
func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Expect(
	grammarID syntaxa.GrammarID,
	outputNodeKind TNodeKind,
	token TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return t.expectCore("Expect", grammarID, outputNodeKind, true, token)
}

/*
ExpectVirtual matches a single token without creating an AST node.

On success: consumes the token and returns success with a nil node.
On failure: reports a syntax error and returns FailureError.

Use cases:
- Skipping punctuation or keywords that do not need a dedicated AST node.
- Matching structure (e.g. closing delimiter) where only the presence matters.

Prerequisites:
- grammarID identifies this production.
- token is the exact token value to match.

Edge cases:
- Same as Expect for mismatch; no node is ever produced.
*/
func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) ExpectVirtual(
	grammarID syntaxa.GrammarID,
	token TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	var zeroKind TNodeKind
	return t.expectCore("ExpectVirtual", grammarID, zeroKind, false, token)
}

/*
ExpectOneOf matches one of several tokens at the current position.

On success: consumes the token, creates an AST node of outputNodeKind, and attaches the lexeme.
On failure: reports a syntax error listing the expected tokens.

Use cases:
- Matching a set of keywords or operators that share the same AST node kind.
- Union of terminals without building a full choice of sub-rules.

Prerequisites:
- grammarID identifies this production.
- outputNodeKind is the AST node kind to create on success.
- tokens must contain at least one token.

Edge cases:
- Empty tokens slice is not validated here; typically avoid.
- Error message includes a formatted list of expected tokens via the builder's tokenFormatter.
*/
func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) ExpectOneOf(
	grammarID syntaxa.GrammarID,
	outputNodeKind TNodeKind,
	tokens ...TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return t.expectCore("ExpectOneOf", grammarID, outputNodeKind, true, tokens...)
}

func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) expectCore(
	ruleName string,
	grammarID syntaxa.GrammarID,
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

	var grammar *syntaxa.Grammar[TToken]

	if len(tokens) == 1 {
		grammar = syntaxa.Token(grammarID, tokens[0])
	} else {
		children := make([]*syntaxa.Grammar[TToken], len(tokens))
		for i, tok := range tokens {
			children[i] = syntaxa.Token(grammarID, tok)
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

		ctx.Error.ReportAt(
			string(name),
			peeked,
			fmt.Sprintf(
				"unexpected %s, wanted one of %s",
				t.sharedCore.tokenFormatter(peeked.Token),
				t.sharedCore.formatTokensAsList(", ", tokens...),
			),
		)

		return t.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureNoMatch)
	}

	// ---------------------------
	// Construct rule
	// ---------------------------

	if addNode {
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
	grammarID syntaxa.GrammarID,
	listOpenToken, elementToken, separatorToken, listEndToken TToken,
	listNodeKind, elementNodeKind TNodeKind,
	allowEmptyList bool,
	mode TrailingSeparatorMode,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	name := t.sharedCore.createRuleName("List", grammarID)
	identity := t.sharedCore.createRuleIdentity(name, grammarID, "")
	recovery := []TToken{listEndToken}

	grammar := t.getListGrammar(grammarID, listOpenToken, elementToken, separatorToken, listEndToken, allowEmptyList, mode)
	syntaxa.MarkAsContextBoundary(grammar)

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
- Grouping rules logically in the grammar without creating "middle-man" nodes in the AST.
- Breaking down complex productions into smaller, reusable sequences that shouldn't appear in the final tree.

Prerequisites:
- grammarID identifies this production.
- rules must not be empty.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) TransparentSequence(
	grammarID syntaxa.GrammarID,
	rules ...Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := r.sharedCore.createRuleName("TransparentSequence", grammarID)
	identity := r.sharedCore.createRuleIdentity(name, grammarID, "")

	mustConsume := r.listCoreMustConsume(rules)
	grammar := r.buildConcatGrammarFromRules(grammarID, rules)

	exec := func(ctx *syntaxa.ExecRuleContext[
		TObservation, TToken, TTokenRole, TLexerState, TNodeKind,
	]) Result[TObservation, TToken, TTokenRole, TNodeKind] {

		results := make([]Result[TObservation, TToken, TTokenRole, TNodeKind], len(rules))
		startMarker := ctx.Token.PeekRaw(0).TokenNumber

		for i, rule := range rules {
			result := ctx.ExecuteRule(rule, syntaxa.ExecutionNormal)

			if result.Failed() {
				currentMarker := ctx.Token.PeekRaw(0).TokenNumber
				effectiveKind := sequenceCommitmentFailureKind(startMarker, currentMarker, result.Kind)
				return r.sharedCore.buildFailureRuleResult(nil, effectiveKind)
			}
			results[i] = result
		}

		// Create a transient node to hold the fragment children
		var zeroKind TNodeKind
		fragmentNode := ctx.Editor.NewTransientNode(zeroKind)
		r.attachResultsToNode(ctx, fragmentNode, results)

		return r.sharedCore.buildFragmentRuleResult(fragmentNode)
	}

	return r.sharedCore.constructRule(
		identity,
		r.sharedCore.createContract(mustConsume, false),
		exec,
		nil,
		grammar,
	)
}

/*
getListGrammar builds the grammar IR for a list production: open, (elem sep)* elem?, close,
with optional empty list and configurable trailing separator mode.
*/
func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) getListGrammar(
	grammarID syntaxa.GrammarID,
	listOpenToken, elementToken, separatorToken, listEndToken TToken,
	allowEmptyList bool,
	mode TrailingSeparatorMode,
) *syntaxa.Grammar[TToken] {

	// Core grammar pieces

	elemG := syntaxa.Token(grammarID, elementToken)
	sepG := syntaxa.Token(grammarID, separatorToken)

	// (S E)
	sepElem := syntaxa.Concat(grammarID, sepG, elemG)

	// (S E)*
	repeatSepElem := syntaxa.ZeroOrMore(grammarID, sepElem)

	// E (S E)*
	baseBody := syntaxa.Concat(grammarID, elemG, repeatSepElem)

	var body *syntaxa.Grammar[TToken]

	switch mode {

	case TrailingForbidden:
		body = baseBody

	case TrailingOptional:
		body = syntaxa.Concat(
			grammarID,
			baseBody,
			syntaxa.Optional(grammarID, sepG),
		)

	case TrailingRequired:
		body = syntaxa.Concat(
			grammarID,
			baseBody,
			sepG,
		)
	}

	if allowEmptyList {
		body = syntaxa.Optional(grammarID, body)
	}

	return syntaxa.Nest(
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
				ctx.Error.ReportAtEnd(string(name), elem, "missing required trailing separator")
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
				string(name),
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
				ctx.Error.ReportAt(string(name), sep, "trailing separator not allowed")
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

/*
handleEmptyList consumes the list end token and returns success with node if allowEmpty is true;
otherwise reports an error and returns failure.
*/
func (t *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) handleEmptyList(
	ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	name syntaxa.RuleLabel,
	node *syntaxa.SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
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
addChildElement creates a new AST node of kind, attaches the lexeme as its token, and appends it as a child of parent.
*/
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
	identity := r.sharedCore.createRuleIdentity(name, rule.GetGrammarID(), rule.GetExpectedLabel())
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
		syntaxa.Optional(rule.GetGrammarID(), rule.GetGrammar()),
	)
}

/*
OptionalPrefix makes the rule optional only when the current token equals prefixToken.

If the next token is prefixToken, the rule is executed (in normal mode). Otherwise the optional succeeds without consuming and returns a nil node. Use this to avoid committing to a production until the prefix is seen.

Use cases:
- Optional blocks that start with a keyword (e.g. "else" block).
- Prefixed optional clauses without full lookahead.

Prerequisites:
- rule must be a valid parser rule.
- prefixToken is the token that triggers execution of rule.

Edge cases:
- When prefix does not match, succeeds immediately with nil node (no consumption).
- When prefix matches, rule runs in ExecutionNormal; its failure is a syntax error.
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
OptionalWhen makes the rule optional when shouldStart returns false at the current position.

When shouldStart(ctx.Select) is false, the rule is not run and the optional succeeds with a nil node. When true, the rule is executed in normal mode and its result is returned.

Use cases:
- Custom lookahead (e.g. "optional when next token is not X").
- Prefixed optionals; prefer OptionalPrefix when the condition is "next token == prefix".

Prerequisites:
- rule must be a valid parser rule.
- shouldStart must only inspect the token stream (e.g. ctx.Select.Peek(0)); it should not consume.

Edge cases:
- shouldStart true and rule failure: syntax error (normal execution).
- shouldStart false: success with nil node, no consumption.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) OptionalWhen(
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	shouldStart func(ctx *syntaxa.SelectRuleContext[TObservation, TToken, TTokenRole]) bool,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := syntaxa.RuleLabel(fmt.Sprintf("OptionalWhen(%s)", rule.GetName()))
	identity := r.sharedCore.createRuleIdentity(name, rule.GetGrammarID(), rule.GetExpectedLabel())

	return r.sharedCore.constructOptionalRule(
		identity,
		func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
			if !shouldStart(ctx.Select) {
				return r.sharedCore.buildSuccessRuleResult(nil)
			}

			return ctx.ExecuteRule(rule, syntaxa.ExecutionNormal)
		},
		rule.GetRecoveryTokens(),
		syntaxa.Optional(rule.GetGrammarID(), rule.GetGrammar()),
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
		rule.GetGrammarID(),
		rule.GetExpectedLabel(),
	)

	return r.sharedCore.constructRule(
		identity,
		rule.GetContract(),
		func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
			result := ctx.ExecuteRule(rule, syntaxa.ExecutionNormal)
			if result.Kind == syntaxa.FailureNoMatch {
				peeked := ctx.Token.Peek(0)
				ctx.Error.ReportAt(string(identity.RuleName), peeked, errMessage)
				return r.sharedCore.buildFailureRuleResult(nil, syntaxa.FailureError)
			}
			return result
		},
		rule.GetRecoveryTokens(),
		rule.GetGrammar(),
	)
}

/*
Root builds a top-level structural rule that runs a sequence of rules and attaches all produced nodes under a single root node of nodeKind.

Rules are run in order. The first rule may fail with FailureNoMatch (grammar boundary); once any rule succeeds, the sequence is committed. If mustConsume is false, zero consumed tokens is allowed (empty program). If mustConsume is true, at least one token must be consumed for success.

Use cases:
- Root of the grammar (e.g. "program" as sequence of declarations and statements).
- Wrapping a series of optional or repeated rules under one node.

Prerequisites:
- grammarID identifies this production.
- nodeKind is the AST kind for the root node.
- mustConsume: if true, success requires at least one token consumed across the rules.

Edge cases:
- First rule FailureNoMatch: entire Root fails with FailureNoMatch.
- Later rule failure: returns success with the root node and whatever children were attached so far (partial consumption; engine semantics may vary).
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Root(
	grammarID syntaxa.GrammarID,
	nodeKind TNodeKind,
	mustConsume bool,
	rules ...Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	name := r.sharedCore.createRuleName("Root", grammarID)
	identity := r.sharedCore.createRuleIdentity(name, grammarID, "")

	// ---------------------------
	// Build grammar IR
	// ---------------------------

	grammar := r.buildConcatGrammarFromRules(grammarID, rules)

	// ---------------------------
	// Runtime execution
	// ---------------------------

	exec := func(ctx *syntaxa.ExecRuleContext[
		TObservation, TToken, TTokenRole, TLexerState, TNodeKind,
	]) Result[TObservation, TToken, TTokenRole, TNodeKind] {

		node := ctx.Editor.NewNode(nodeKind)

		for _, rule := range rules {
			result := ctx.ExecuteRule(rule, syntaxa.ExecutionNormal)

			if result.Failed() {
				return r.sharedCore.buildSuccessRuleResult(node)
			}

			ctx.Editor.AttachResult(node, result)
		}

		return r.sharedCore.buildSuccessRuleResult(node)
	}

	return r.sharedCore.constructRule(
		identity,
		r.sharedCore.createContract(mustConsume, true),
		exec,
		nil,
		grammar,
	)
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
- nodeKind is the AST kind for the container node.
- rules must not be empty (empty sequence is not useful).

Edge cases:
- First rule FailureNoMatch: entire Sequence fails with FailureNoMatch.
- Any rule FailureError or later FailureNoMatch: Sequence fails with FailureError.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Sequence(
	grammarID syntaxa.GrammarID,
	nodeKind TNodeKind,
	rules ...Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := r.sharedCore.createRuleName("Sequence", grammarID)
	identity := r.sharedCore.createRuleIdentity(name, grammarID, "")
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
	grammarID syntaxa.GrammarID,
	nodeKind TNodeKind,
	blockEndToken TToken,
	rules ...Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := r.sharedCore.createRuleName("Block", grammarID)
	identity := r.sharedCore.createRuleIdentity(name, grammarID, "")
	return r.listCore(identity, nodeKind, []TToken{blockEndToken}, rules...)
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

	// ---------------------------
	// Build grammar IR
	// ---------------------------

	grammar := r.buildConcatGrammarFromRules(identity.GrammarID, rules)

	// ---------------------------
	// Runtime execution
	// ---------------------------

	exec := func(ctx *syntaxa.ExecRuleContext[
		TObservation, TToken, TTokenRole, TLexerState, TNodeKind,
	]) Result[TObservation, TToken, TTokenRole, TNodeKind] {

		results := make([]Result[TObservation, TToken, TTokenRole, TNodeKind], len(rules))

		startMarker := ctx.Token.PeekRaw(0).TokenNumber

		for i, rule := range rules {
			result := ctx.ExecuteRule(rule, syntaxa.ExecutionNormal)

			if result.Failed() {
				currentMarker := ctx.Token.PeekRaw(0).TokenNumber
				effectiveKind := sequenceCommitmentFailureKind(startMarker, currentMarker, result.Kind)
				return r.sharedCore.buildFailureRuleResult(nil, effectiveKind)
			}

			results[i] = result
		}

		node := ctx.Editor.NewNode(nodeKind)
		r.attachResultsToNode(ctx, node, results)

		return r.sharedCore.buildSuccessRuleResult(node)
	}

	return r.sharedCore.constructRule(
		identity,
		r.sharedCore.createContract(mustConsume, true),
		exec,
		recovery,
		grammar,
	)
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
	grammarID syntaxa.GrammarID,
	rules []Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) *syntaxa.Grammar[TToken] {
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
) []*syntaxa.Grammar[TToken] {
	children := make([]*syntaxa.Grammar[TToken], len(rules))
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
	parent *syntaxa.SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
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
	container *syntaxa.SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
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
			if before.TokenNumber == after.TokenNumber {
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
	grammarID syntaxa.GrammarID,
	min int,
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	makeContainer func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) *syntaxa.SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
	onSuccess func(container *syntaxa.SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	ensureMinNonNegative(min, ruleName)

	grammar := syntaxa.Repeat(grammarID, rule.GetGrammar(), min, nil)
	syntaxa.MarkAsContextBoundary(grammar)

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
- nodeKind is the AST kind for the container node.
- min must be >= 0 (panics otherwise).
- rule must be a valid parser rule.

Edge cases:
- min is 0: may succeed with empty container node (zero matches).
- After FailureError, progress is checked via token position; no progress means immediate propagation.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) NOrMore(
	grammarID syntaxa.GrammarID,
	nodeKind TNodeKind,
	min int,
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return r.constructNOrMoreRule(
		"NOrMore",
		grammarID,
		min,
		rule,
		func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) *syntaxa.SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind] {
			return ctx.Editor.NewNode(nodeKind)
		},
		func(container *syntaxa.SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
			return r.sharedCore.buildSuccessRuleResult(container)
		},
	)
}

/*
ZeroOrMore matches the rule zero or more times.

It is equivalent to NOrMore(grammarID, nodeKind, 0, rule). On success, a node of nodeKind is created and each successful match's node is attached; zero matches yields an empty container node.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) ZeroOrMore(
	grammarID syntaxa.GrammarID,
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
	grammarID syntaxa.GrammarID,
	nodeKind TNodeKind,
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return r.NOrMore(grammarID, nodeKind, 1, rule)
}

func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) TransparentNOrMore(
	grammarID syntaxa.GrammarID,
	min int,
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	var zeroKind TNodeKind
	return r.constructNOrMoreRule(
		"TransparentNOrMore",
		grammarID,
		min,
		rule,
		func(ctx *syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) *syntaxa.SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind] {
			return ctx.Editor.NewTransientNode(zeroKind)
		},
		func(container *syntaxa.SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
			return r.sharedCore.buildFragmentRuleResult(container)
		},
	)
}

func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) TransparentZeroOrMore(
	grammarID syntaxa.GrammarID,
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
- Grouping without changing the inner rule's AST shape beyond wrapping.

Prerequisites:
- grammarID identifies this production.
- nodeKind is the AST kind for the wrapper node.
- openToken and closeToken are the delimiter pair.
- innerRule is the rule for the content between the delimiters.

Edge cases:
- openToken mismatch: FailureNoMatch (no diagnostic).
- closeToken mismatch after inner success: syntax error and FailureError.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Nest(
	grammarID syntaxa.GrammarID,
	nodeKind TNodeKind,
	openToken, closeToken TToken,
	innerRule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	name := r.sharedCore.createRuleName("Nest", grammarID)
	identity := r.sharedCore.createRuleIdentity(name, grammarID, "")

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
			ctx.Error.ReportAt(
				string(name),
				closeLex,
				r.sharedCore.formatUnexpectedExpected(closeLex.Token, closeToken),
			)
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
		grammar,
	)
}

func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) TransparentNest(
	grammarID syntaxa.GrammarID,
	openToken, closeToken TToken,
	innerRule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := r.sharedCore.createRuleName("TransparentNest", grammarID)
	identity := r.sharedCore.createRuleIdentity(name, grammarID, "")

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

	mustConsume := true
	return r.sharedCore.constructRule(
		identity,
		r.sharedCore.createContract(mustConsume, innerRule.GetContract().MustReturnNode),
		exec,
		[]TToken{closeToken},
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

	ctx.Error.ReportAt(string(ruleName), found, r.sharedCore.formatUnexpectedExpected(found.Token, expected))
	return false
}

/*
Prefixed matches a specific prefix token, discards it, and returns the result of the inner rule.

Use cases:
- Matching virtual prefixes where the AST node is entirely defined by the inner rule.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Prefixed(
	grammarID syntaxa.GrammarID,
	prefixToken TToken,
	innerRule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	name := r.sharedCore.createRuleName("Prefixed", grammarID)
	identity := r.sharedCore.createRuleIdentity(name, grammarID, innerRule.GetExpectedLabel())

	grammar := syntaxa.Concat(
		grammarID,
		syntaxa.Token(grammarID, prefixToken),
		innerRule.GetGrammar(),
	)
	syntaxa.MarkAsContextBoundary(grammar)

	return r.sharedCore.constructRule(
		identity,
		r.sharedCore.createContract(true, innerRule.GetContract().MustReturnNode),
		r.executePrefixedRule(prefixToken, innerRule),
		innerRule.GetRecoveryTokens(),
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
	grammarID syntaxa.GrammarID,
	rules ...Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	name := r.sharedCore.createRuleName("Choice", grammarID)
	identity := r.sharedCore.createRuleIdentity(name, grammarID, "")

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
		grammar,
	)
}

/*
buildChoiceGrammarFromRules builds a Choice grammar from the given rules and marks it as a context boundary.
*/
func (r *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) buildChoiceGrammarFromRules(
	grammarID syntaxa.GrammarID,
	rules []Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) *syntaxa.Grammar[TToken] {
	children := r.grammarsFromRules(rules)
	grammar := syntaxa.Choice(grammarID, children...)
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

// ------------------------------------------------------------- RULEBUILDER

/*
RuleBuilder is the rule factory entry point: it provides Token and Rule endpoints for building parser rules.

Use Token for rules that match lexer tokens (Expect, ExpectVirtual, ExpectOneOf, List). Use Rule for rules that combine other rules (Sequence, Block, Optional, NOrMore, Nest, Root). Both endpoints share the same type parameters and token formatter; rules from either can be composed together.

For common grammar patterns, build rules via this type. For behaviour not covered by the factory, build rules manually with syntaxa.ParserRuleCreate and the syntaxa grammar IR.
*/
type RuleBuilder[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	/* Token exposes token-level rules: Expect, ExpectVirtual, ExpectOneOf, List. */
	Token *tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]

	/* Rule exposes composite rules: Sequence, Block, Optional, Required, NOrMore, Nest, Root, etc. */
	Rule *ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]

	sharedCore *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
}

/*
RuleBuilderCreate allocates and returns a new RuleBuilder with the given token formatter.

The tokenFormatter is used when building error messages that mention token values (e.g. "expected one of ID, COMMA"). It should return a short, readable string for each token (e.g. token ID or name). The returned RuleBuilder is ready to use: RuleBuilder.Token and RuleBuilder.Rule are non-nil.

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
	}

	tokenEndpoint := &tokenEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{
		sharedCore: sharedCore,
	}

	ruleEndpoint := &ruleEndpoint[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{
		sharedCore: sharedCore,
		token:      tokenEndpoint,
	}

	return &RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{
		Token:      tokenEndpoint,
		Rule:       ruleEndpoint,
		sharedCore: sharedCore,
	}
}

// ------------------------------------------------------------- PRIVATE HELPERS

/*
sharedCore holds the token formatter and provides rule construction helpers: result builders,
identity/contract creation, and constructRule variants (structural, skipping, optional, virtual).
*/
type sharedCore[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	tokenFormatter func(token TToken) string
}

/*
formatUnexpectedExpected returns the standard "unexpected X, expected Y" message using the shared token formatter.
Used by reportMismatch, enforceToken, and Nest close-token errors.
*/
func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) formatUnexpectedExpected(
	found, expected TToken,
) string {
	return fmt.Sprintf(
		"unexpected %s, expected %s",
		s.tokenFormatter(found),
		s.tokenFormatter(expected),
	)
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

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) buildFragmentRuleResult(
	node *syntaxa.SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	return Result[TObservation, TToken, TTokenRole, TNodeKind]{
		Node:       node,
		Succeeded:  true,
		IsFragment: true,
	}
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) createRuleName(
	rule string,
	grammarID syntaxa.GrammarID,
) syntaxa.RuleLabel {
	return syntaxa.RuleLabel(fmt.Sprintf("Rule %s (id:%s)", rule, grammarID))
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) createRuleIdentity(
	name syntaxa.RuleLabel,
	grammarID syntaxa.GrammarID,
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
	grammar *syntaxa.Grammar[TToken],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return s.constructRule(
		identity,
		s.createContract(true, true),
		execution,
		recovery,
		grammar,
	)
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) constructSkippingRule(
	identity syntaxa.RuleIdentity,
	execution syntaxa.ParserRuleExecutor[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	recovery []TToken,
	grammar *syntaxa.Grammar[TToken],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return s.constructRule(
		identity,
		s.createContract(true, false),
		execution,
		recovery,
		grammar,
	)
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) constructOptionalRule(
	identity syntaxa.RuleIdentity,
	execution syntaxa.ParserRuleExecutor[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	recovery []TToken,
	grammar *syntaxa.Grammar[TToken],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return s.constructRule(
		identity,
		s.createContract(false, false),
		execution,
		recovery,
		grammar,
	)
}

func (s *sharedCore[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) constructVirtualRule(
	identity syntaxa.RuleIdentity,
	execution syntaxa.ParserRuleExecutor[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	recovery []TToken,
	grammar *syntaxa.Grammar[TToken],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return s.constructRule(
		identity,
		s.createContract(false, true),
		execution,
		recovery,
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
	grammar *syntaxa.Grammar[TToken],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return syntaxa.ParserRuleCreate(
		identity,
		execution,
		ruleContract,
		recovery,
		grammar,
	)
}
