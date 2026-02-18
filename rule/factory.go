package rule

import (
	"cmp"
	"fmt"
	"lexarch"
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

/* SuccessAggregator uses the collected success-outputs to construct the final output.*/
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
			return outResult, true
		}

		return r.returnFailureResult(ctx, failureResultRequester)
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
				return r.returnFailureResult(ctx, failureResultRequester)
			}
		}

		outResult := aggregator(successOutputs, ctx.Final)
		return outResult, true
	}
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
			return out, true
		}

		return r.returnFailureResult(ctx, failureResultRequesterBuilder)
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
		return Result[TObservation, TToken, TTokenRole, TNodeKind]{
			Node: errNode,
		}
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
		return Result[TObservation, TToken, TTokenRole, TNodeKind]{
			Node: errNode,
		}
	}
}

/* NilNodeSuccessBuilder builds a success result builder which emits no node. */
func (r *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) NilNodeSuccessBuilder() SuccessResultRequester[TObservation, TToken, TTokenRole, TNodeKind] {
	return func(
		ctx *syntaxa.FinalizationContext[TObservation, TToken, TTokenRole, TNodeKind],
		lexemes []Lexeme[TObservation, TToken, TTokenRole],
	) Result[TObservation, TToken, TTokenRole, TNodeKind] {
		return Result[TObservation, TToken, TTokenRole, TNodeKind]{
			Node:    nil,
			SkipAdd: true,
		}
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

		return Result[TObservation, TToken, TTokenRole, TNodeKind]{
			Node: node,
		}
	}
}

// ------------------------------------------------------------- PRIVATE HELPERS

func (r *RuleBuilder[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]) returnFailureResult(
	ctx syntaxa.ExecRuleContext[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
	failureResultRequester FailureResultRequester[TObservation, TToken, TTokenRole, TNodeKind],
) (Result[TObservation, TToken, TTokenRole, TNodeKind], bool) {
	currentLexeme := ctx.Token.Peek(0)
	failureResult := failureResultRequester(currentLexeme, ctx.Final)
	return failureResult, false
}
