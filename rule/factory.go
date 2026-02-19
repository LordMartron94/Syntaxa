package rule

import (
	"cmp"
	"fmt"
	"lexarch"
	"strings"
	"syntaxa"
)

type Rule[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] = syntaxa.ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]

type Result[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind comparable] = syntaxa.RuleResult[TObservation, TToken, TTokenRole, TNodeKind]

type Lexeme[TObservation cmp.Ordered, TToken, TTokenRole comparable] = lexarch.Lexeme[TObservation, TToken, TTokenRole]

/* FailureResultRequester gets called when a combinator fails to get the right output from the client. */
type FailureResultRequester[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind comparable] func(
	currentLexeme lexarch.Lexeme[TObservation, TToken, TTokenRole],
	ctx *syntaxa.FinalizationContext[TObservation, TToken, TTokenRole, TNodeKind],
) Result[TObservation, TToken, TTokenRole, TNodeKind]

/*
SuccessAggregator uses the collected success-outputs to construct the final output.

This can be a nil result.
*/
type SuccessAggregator[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind comparable] func(
	successResults []Result[TObservation, TToken, TTokenRole, TNodeKind],
	ctx *syntaxa.FinalizationContext[TObservation, TToken, TTokenRole, TNodeKind],
) Result[TObservation, TToken, TTokenRole, TNodeKind]

/* SuccessResultRequester constructs a rule result on success for a rule. */
type SuccessResultRequester[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind comparable] func(
	ctx *syntaxa.FinalizationContext[TObservation, TToken, TTokenRole, TNodeKind],
	lexemes []Lexeme[TObservation, TToken, TTokenRole],
) Result[TObservation, TToken, TTokenRole, TNodeKind]

/*
RuleBuilder encapsulates the factories for rule building.

The primary reason for this encapsulation is easier dealing with generics for the client.
*/
type RuleBuilder[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable] struct {
	tokenFormatter func(token TToken) string
}

func RuleBuilderCreate[TObservation cmp.Ordered, TToken, TTokenRole, TLexerState, TNodeKind comparable](
	tokenFormatter func(token TToken) string,
) *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return &RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]{
		tokenFormatter: tokenFormatter,
	}
}

/*
Choice constructs a rule that tries candidate rules in order and stops at the first match.

If no match is found, it returns a failure output.
*/
func (r *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Choice(
	failureResultRequester FailureResultRequester[TObservation, TToken, TTokenRole, TNodeKind],
	candidates ...Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	return func(_ struct{}, ctx syntaxa.ExecRuleContext[
		TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	) (Result[TObservation, TToken, TTokenRole, TNodeKind], bool) {

		var outResult Result[TObservation, TToken, TTokenRole, TNodeKind]

		fns := make([]func() bool, len(candidates))

		for i, candidate := range candidates {
			c := candidate

			fns[i] = func() bool {
				res, ok := ctx.ExecuteRule(c)
				if ok {
					outResult = res
				}
				return ok
			}
		}

		if ctx.Transaction.TryChoice(fns...) {
			outResult = r.adjustRuleName(outResult, "Choice")
			return outResult, true
		}

		return r.returnFailureResult(ctx, failureResultRequester, "Choice")
	}
}

/*
Sequence builds a rule that needs all rules to succeed.

If a single rule fails, it aborts and returns a failure output.
If all rules succeed, it calls the aggregator to return the final output.
*/
func (r *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Sequence(
	failureResultRequester FailureResultRequester[TObservation, TToken, TTokenRole, TNodeKind],
	aggregator SuccessAggregator[TObservation, TToken, TTokenRole, TNodeKind],
	rules ...Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return func(_ struct{}, ctx syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) (Result[TObservation, TToken, TTokenRole, TNodeKind], bool) {
		successOutputs := make([]Result[TObservation, TToken, TTokenRole, TNodeKind], 0)

		for _, rule := range rules {
			if success := ctx.Transaction.Try(func() bool {
				res, ok := ctx.ExecuteRule(rule)

				if ok {
					successOutputs = append(successOutputs, res)
				}

				return ok
			}); !success {
				return r.returnFailureResult(ctx, failureResultRequester, "Sequence")
			}
		}

		outResult := aggregator(successOutputs, ctx.Final)
		outResult = r.adjustRuleName(outResult, "Sequence")
		return outResult, true
	}
}

/*
TokenList deals with list type tokens.

If optionalTrailingSeparator is set to true, it allows for the last separator to be gone.
If emptyListAllowed is set to true, the list can be without elements as well.

It automatically handles syntax errors.
*/
func (r *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) TokenList(
	listNodeKind, elementNodeKind TNodeKind,
	openToken, elementToken, separatorToken, closeToken TToken,
	optionalTrailingSeparator, emptyListAllowed bool,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {

	elementRule := r.Expect(
		elementToken,
		r.UnexpectedTokenFailureBuilder(elementToken, "expected element"),
		func(
			ctx *syntaxa.FinalizationContext[TObservation, TToken, TTokenRole, TNodeKind],
			lexemes []Lexeme[TObservation, TToken, TTokenRole],
		) Result[TObservation, TToken, TTokenRole, TNodeKind] {

			node := ctx.NewNode(elementNodeKind)
			ctx.AddToken(node, lexemes[0])

			return r.constructResult(
				"TokenList - Element",
				node,
				false,
				false,
			)
		},
	)

	mainRule := func(
		_ struct{},
		ctx syntaxa.ExecRuleContext[
			TObservation,
			TToken,
			TTokenRole,
			TLexerState,
			TNodeKind,
		],
	) (Result[TObservation, TToken, TTokenRole, TNodeKind], bool) {

		start := ctx.Token.Peek(0)

		// ─────────────────────────────────────────────
		// OPEN
		// ─────────────────────────────────────────────

		if start.Token != openToken {
			ctx.Error.Report(
				start.StartLine,
				start.StartColumn,
				fmt.Sprintf("expected %s opening", r.tokenFormatter(openToken)),
			)
			return r.constructResult("TokenList", nil, true, false), false
		}

		ctx.Token.Consume()

		listNode := ctx.Editor.NewNode(listNodeKind)

		// Track last consumed element lexeme
		var lastElementLexeme *Lexeme[TObservation, TToken, TTokenRole] = nil

		cur := ctx.Token.Peek(0)

		// ─────────────────────────────────────────────
		// EMPTY LIST
		// ─────────────────────────────────────────────

		if cur.Token == closeToken {

			if !emptyListAllowed {
				ctx.Error.Report(
					start.StartLine,
					start.StartColumn,
					"expected at least 1 element in the list",
				)
				return r.constructResult("TokenList", nil, true, false), false
			}

			ctx.Token.Consume()
			return r.constructResult("TokenList", listNode, false, false), true
		}

		// ─────────────────────────────────────────────
		// FIRST ELEMENT
		// ─────────────────────────────────────────────

		{
			before := ctx.Token.Peek(0)

			res, ok := ctx.ExecuteRule(elementRule)
			if !ok {
				cur := ctx.Token.Peek(0)
				ctx.Error.Report(
					cur.StartLine,
					cur.StartColumn,
					fmt.Sprintf("expected %s", r.tokenFormatter(elementToken)),
				)
				return r.constructResult("TokenList", nil, true, false), false
			}

			ctx.Editor.AttachChild(listNode, res.Node)

			lex := before
			lastElementLexeme = &lex
		}

		// ─────────────────────────────────────────────
		// LOOP
		// ─────────────────────────────────────────────

		for {

			cur = ctx.Token.Peek(0)

			// ─── CONTINUE: , element
			if cur.Token == separatorToken {

				ctx.Token.Consume()

				next := ctx.Token.Peek(0)

				// Optional trailing separator
				if optionalTrailingSeparator && next.Token == closeToken {
					break
				}

				before := ctx.Token.Peek(0)

				res, ok := ctx.ExecuteRule(elementRule)
				if !ok {
					cur := ctx.Token.Peek(0)
					ctx.Error.Report(
						cur.StartLine,
						cur.StartColumn,
						fmt.Sprintf(
							"expected %s after %s",
							r.tokenFormatter(elementToken),
							r.tokenFormatter(separatorToken),
						),
					)
					return r.constructResult("TokenList", nil, true, false), false
				}

				ctx.Editor.AttachChild(listNode, res.Node)

				lex := before
				lastElementLexeme = &lex

				continue
			}

			// ─── END: }
			if cur.Token == closeToken {
				break
			}

			// ─── ERROR: missing separator
			ctx.Error.Report(
				lastElementLexeme.EndLine,
				lastElementLexeme.EndColumn,
				fmt.Sprintf(
					"expected %s or %s",
					r.tokenFormatter(separatorToken),
					r.tokenFormatter(closeToken),
				),
			)

			return r.constructResult("TokenList", nil, true, false), false
		}

		// ─────────────────────────────────────────────
		// CLOSE
		// ─────────────────────────────────────────────

		end := ctx.Token.Peek(0)

		if end.Token != closeToken {
			ctx.Error.Report(
				end.StartLine,
				end.StartColumn,
				fmt.Sprintf("expected %s close", r.tokenFormatter(closeToken)),
			)
			return r.constructResult("TokenList", nil, true, false), false
		}

		ctx.Token.Consume()

		return r.constructResult("TokenList", listNode, false, false), true
	}

	return mainRule
}

/*
Expect constructs a rule that needs to consume a specific token.

On success it uses the output requester to get a result.
On failure it requests and returns the failure result from the client.

To report syntax errors, this will be done in the failureResultRequester provided.
*/
func (r *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Expect(
	expectation TToken,
	failureResultRequesterBuilder FailureResultRequester[TObservation, TToken, TTokenRole, TNodeKind],
	successResultRequester SuccessResultRequester[TObservation, TToken, TTokenRole, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return func(_ struct{}, ctx syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) (Result[TObservation, TToken, TTokenRole, TNodeKind], bool) {
		var out Lexeme[TObservation, TToken, TTokenRole]
		if success := ctx.Transaction.Try(func() bool {
			peeked := ctx.Token.Peek(0)

			if peeked.Token == expectation {
				out = ctx.Token.Consume()
				return true
			}

			return false
		}); success {
			out := successResultRequester(ctx.Final, []Lexeme[TObservation, TToken, TTokenRole]{out})
			out = r.adjustRuleName(out, "Expect")
			return out, true
		}

		return r.returnFailureResult(ctx, failureResultRequesterBuilder, "Expect")
	}
}

/*
ExpectSequence constructs a rule that needs to consume a specific sequence of tokens.

On success it uses the output requester to get a result.
On failure it requests and returns the failure result from the client.

To report syntax errors, this will be done in the failureResultRequester provided.
*/
func (r *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) ExpectSequence(
	failureResultRequesterBuilder FailureResultRequester[TObservation, TToken, TTokenRole, TNodeKind],
	successResultRequester SuccessResultRequester[TObservation, TToken, TTokenRole, TNodeKind],
	expectations ...TToken,
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return func(_ struct{}, ctx syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) (Result[TObservation, TToken, TTokenRole, TNodeKind], bool) {
		lexemes := make([]Lexeme[TObservation, TToken, TTokenRole], 0)
		if success := ctx.Transaction.Try(func() bool {
			success := true

			for i, tk := range expectations {
				peeked := ctx.Token.Peek(i)
				if peeked.Token != tk {
					success = false
					break
				}
			}

			if success {
				for range expectations {
					lexeme := ctx.Token.Consume()
					lexemes = append(lexemes, lexeme)
				}
			}

			return success
		}); success {
			out := successResultRequester(ctx.Final, lexemes)
			r.adjustRuleName(out, "ExpectSequence")
			return out, true
		}

		return r.returnFailureResult(ctx, failureResultRequesterBuilder, "ExpectSequence")
	}
}

/*
Optional constructs a rule that executes a rule which is allowed to fail.

On success it returns the original rule's output.
On failure it returns a nil node.
*/
func (r *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) Optional(
	rule Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return func(_ struct{}, ctx syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) (Result[TObservation, TToken, TTokenRole, TNodeKind], bool) {
		var successResult Result[TObservation, TToken, TTokenRole, TNodeKind]

		if success := ctx.Transaction.Try(func() bool {
			res, ok := ctx.ExecuteRule(rule)

			if ok {
				successResult = res
			}

			return ok
		}); success {
			successResult = r.adjustRuleName(successResult, "Optional")
			successResult.Optional = true
			return successResult, true
		}

		out := r.NilNodeSuccessBuilder()(ctx.Final, nil)
		out.Optional = true
		out = r.adjustRuleName(out, "Optional")
		return out, true
	}
}

/*
OptionalExpect constructs a rule that optionally consumes a specific token.

If the token is present, it is consumed and aggregated.
If absent, it succeeds silently (no error, no rollback needed, nil node).
*/
func (r *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) OptionalExpect(
	expectation TToken,
	successResultRequester SuccessResultRequester[TObservation, TToken, TTokenRole, TNodeKind],
) Rule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind] {
	return func(_ struct{},
		ctx syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	) (Result[TObservation, TToken, TTokenRole, TNodeKind], bool) {
		var outLexeme Lexeme[TObservation, TToken, TTokenRole]

		if ctx.Transaction.TrySpeculative(func() bool {
			peeked := ctx.Token.Peek(0)
			if peeked.Token == expectation {
				outLexeme = ctx.Token.Consume()
				return true
			}
			return false
		}) {
			out := successResultRequester(
				ctx.Final,
				[]Lexeme[TObservation, TToken, TTokenRole]{outLexeme},
			)
			out = r.adjustRuleName(out, "OptionalExpect")
			return out, true
		}

		return r.constructResult("OptionalExpect", nil, true, true), true
	}
}

/*
ChoiceFailureBuilder returns a failure builder for a choice combinator.

It automatically handles syntax error reporting.
*/
func (r *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) ChoiceFailureBuilder(
	syntaxError string,
) FailureResultRequester[TObservation, TToken, TTokenRole, TNodeKind] {
	return func(
		currentLexeme lexarch.Lexeme[TObservation, TToken, TTokenRole],
		ctx *syntaxa.FinalizationContext[TObservation, TToken, TTokenRole, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
		ctx.ReportError(currentLexeme.StartLine, currentLexeme.StartColumn, syntaxError)
		errNode := ctx.CreateErrorNode(syntaxError)
		return r.constructResult("ChoiceFailureBuilder", errNode, false, false)
	}
}

/*
UnexpectedTokenFailureBuilder returns a failure builder for unexpected tokens.

It automatically handles syntax error reporting.
*/
func (r *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) UnexpectedTokenFailureBuilder(
	expectedToken TToken,
	description string,
) FailureResultRequester[TObservation, TToken, TTokenRole, TNodeKind] {
	return func(
		currentLexeme lexarch.Lexeme[TObservation, TToken, TTokenRole],
		ctx *syntaxa.FinalizationContext[TObservation, TToken, TTokenRole, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
		syntaxError := fmt.Sprintf("%s: unexpected %s, wanted %s", description, r.tokenFormatter(currentLexeme.Token), r.tokenFormatter(expectedToken))
		ctx.ReportError(currentLexeme.StartLine, currentLexeme.StartColumn, syntaxError)
		errNode := ctx.CreateErrorNode(syntaxError)
		return r.constructResult("UnexpectedTokenFailureBuilder", errNode, false, false)
	}
}

/*
UnexpectedTokenFailureBuilderSequence returns a failure builder for unexpected tokens.

It reports a full expected token sequence instead of a single token.
*/
func (r *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) UnexpectedTokenFailureBuilderSequence(
	description string,
	expectedTokens ...TToken,
) FailureResultRequester[TObservation, TToken, TTokenRole, TNodeKind] {

	// Pre-format once (builder is constructed once, invoked many times)
	expectedFormatted := make([]string, len(expectedTokens))
	for i, tok := range expectedTokens {
		expectedFormatted[i] = r.tokenFormatter(tok)
	}

	expectedStr := strings.Join(expectedFormatted, " → ")

	return func(
		currentLexeme lexarch.Lexeme[TObservation, TToken, TTokenRole],
		ctx *syntaxa.FinalizationContext[TObservation, TToken, TTokenRole, TNodeKind],
	) Result[TObservation, TToken, TTokenRole, TNodeKind] {

		syntaxError := fmt.Sprintf(
			"%s: unexpected %s, expected sequence %s",
			description,
			r.tokenFormatter(currentLexeme.Token),
			expectedStr,
		)

		ctx.ReportError(
			currentLexeme.StartLine,
			currentLexeme.StartColumn,
			syntaxError,
		)

		errNode := ctx.CreateErrorNode(syntaxError)

		return r.constructResult("UnexpectedTokenFailureBuilderSequence", errNode, false, false)
	}
}

/* NilNodeSuccessBuilder builds a success result builder which emits no node. */
func (r *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) NilNodeSuccessBuilder() SuccessResultRequester[TObservation, TToken, TTokenRole, TNodeKind] {
	return func(
		ctx *syntaxa.FinalizationContext[TObservation, TToken, TTokenRole, TNodeKind],
		lexemes []Lexeme[TObservation, TToken, TTokenRole],
	) Result[TObservation, TToken, TTokenRole, TNodeKind] {
		return r.constructResult("NilNodeSuccessBuilder", nil, true, false)
	}
}

/*
ExpectedNodeSuccessBuilder builds a success builder for an expected node.

It automatically sets the required tokens.
*/
func (r *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) ExpectedNodeSuccessBuilder(
	kind TNodeKind,
) SuccessResultRequester[TObservation, TToken, TTokenRole, TNodeKind] {
	return func(
		ctx *syntaxa.FinalizationContext[TObservation, TToken, TTokenRole, TNodeKind],
		lexemes []Lexeme[TObservation, TToken, TTokenRole],
	) Result[TObservation, TToken, TTokenRole, TNodeKind] {
		node := ctx.NewNode(kind)
		ctx.SetTokens(node, lexemes)

		return r.constructResult("ExpectedNodeSuccessBuilder", node, false, false)
	}
}

/*
SequenceFailureBuilder builds an error node for a sequence failure.
*/
func (r *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) SequenceFailureBuilder(
	errorMessage string,
) FailureResultRequester[TObservation, TToken, TTokenRole, TNodeKind] {
	return func(
		currentLexeme lexarch.Lexeme[TObservation, TToken, TTokenRole],
		ctx *syntaxa.FinalizationContext[TObservation, TToken, TTokenRole, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
		node := ctx.CreateErrorNode(errorMessage)
		return r.constructResult("SequenceFailureBuilder", node, false, false)
	}
}

/*
SequenceSuccessBuilder builds a success builder for sequences.

If there are no successes, it returns a nil node.
*/
func (r *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) SequenceSuccessBuilder(
	nodeKind TNodeKind,
) SuccessAggregator[TObservation, TToken, TTokenRole, TNodeKind] {
	return func(
		successResults []Result[TObservation, TToken, TTokenRole, TNodeKind],
		ctx *syntaxa.FinalizationContext[TObservation, TToken, TTokenRole, TNodeKind]) Result[TObservation, TToken, TTokenRole, TNodeKind] {
		if len(successResults) == 0 {
			return r.constructResult("SequenceSuccessBuilder", nil, true, false)
		}

		headerNode := ctx.NewNode(nodeKind)
		for _, child := range successResults {
			ctx.AttachChild(headerNode, child.Node)
		}

		return r.constructResult("SequenceSuccessBuilder", headerNode, false, false)
	}
}

// ------------------------------------------------------------- PRIVATE HELPERS

func (r *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) constructResult(
	ruleName string,
	node *syntaxa.SyntaxaASTNode[TObservation, TToken, TTokenRole, TNodeKind],
	skipAdd bool,
	optional bool,
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	return Result[TObservation, TToken, TTokenRole, TNodeKind]{
		Node:            node,
		SkipAdd:         skipAdd,
		Optional:        optional,
		RuleDiagnostics: fmt.Sprintf("NAME: %s", ruleName),
	}
}

func (r *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) adjustRuleName(
	result Result[TObservation, TToken, TTokenRole, TNodeKind],
	ruleName string,
) Result[TObservation, TToken, TTokenRole, TNodeKind] {
	return Result[TObservation, TToken, TTokenRole, TNodeKind]{
		Node:            result.Node,
		SkipAdd:         result.SkipAdd,
		Optional:        result.Optional,
		RuleDiagnostics: fmt.Sprintf("NAME: %s", ruleName),
	}
}

func (r *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) returnFailureResult(
	ctx syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	failureResultRequester FailureResultRequester[TObservation, TToken, TTokenRole, TNodeKind],
	ruleName string,
) (Result[TObservation, TToken, TTokenRole, TNodeKind], bool) {
	currentLexeme := ctx.Token.Peek(0)
	failureResult := failureResultRequester(currentLexeme, ctx.Final)
	failureResult = r.adjustRuleName(failureResult, ruleName)
	return failureResult, false
}
