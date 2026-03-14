package lowering

import (
	"cmp"
	"fmt"
	"strings"
	"unicode"

	"syntaxa"
)

/*
StackOp is the kind of stack operation for a transition in the generic state graph.

Match consumes a token with no stack change. Push, Pop, and Set are standard stack
operations. PopAmount for OpPop is the number of grammar stack frames exited; the
editor backend may add +1 when it injects wrapper states.
*/
type StackOp uint8

const (
	OpMatch StackOp = iota
	OpPush
	OpPop
	OpSet
)

/*
Context is an editor-agnostic state in the state graph.

ID uniquely identifies the state; Label is a human-readable name used for debug
and deterministic keying. The graph uses Contexts as nodes.
*/
type Context struct {
	ID    string
	Label string
}

/*
ContextMeta holds optional metadata for a context in the state graph.

HasOptionalContinuation is true when all transitions from this context are from
optional or repeat grammar; the editor backend may use fallthrough pop. When set,
FallthroughPopAmount is the number of stack frames to pop on fallthrough.
*/
type ContextMeta struct {
	HasOptionalContinuation bool
	FallthroughPopAmount    int
}

/*
Transition is a single edge in the state graph.

On Token (with optional semantic NodeKind for highlighting scope), apply Operation
and move to TargetContextIDs. PopAmount is used when Operation is OpPop and holds
the number of grammar stack frames exited. NodeKind identifies what is being matched
for editor backends (e.g. syntax highlighting scope).
*/
type Transition[TToken, TNodeKind comparable] struct {
	Token            TToken
	NodeKind         *TNodeKind // optional; from grammar OutputNodeKind for this token
	Operation        StackOp
	TargetContextIDs []string
	PopAmount        int
}

/*
StateGraph is the generic, editor-agnostic result of lowering a grammar to a state machine.

Contexts are nodes; Transitions are keyed by context ID. RootContextID is the entry
state. NestBodyContextIDs maps nest grammar labels to the context ID for that nest's
body. Transition order per state is discovery order; the editor backend sorts by
lexer priority.
*/
type StateGraph[TToken, TNodeKind comparable] struct {
	Contexts           []Context
	ContextMeta        map[string]ContextMeta
	RootContextID      string
	Transitions        map[string][]Transition[TToken, TNodeKind]
	NestBodyContextIDs map[syntaxa.GrammarLabel]string
}

const rootLabel = "root"

type pendingEntry[TToken, TNodeKind comparable] struct {
	ctxID    string
	ctxKey   string
	terms    []gTerminal[TToken, TNodeKind]
	nameHint syntaxa.GrammarLabel
}

/*
BuildStateGraph builds a generic StateGraph from a GrammarPackage.

tokenFormatter is used for deterministic context keys. Transition order is discovery
order; PopAmount reflects grammar stack frames exited. The editor layer (e.g.
langspec/editor) applies priority sort, nest split, and fallthrough.

Prerequisites:
- pkg must be non-nil with a valid entry rule; tokenFormatter must be non-nil.

Edge cases:
- Returns an error if the entry rule is missing or tokenFormatter is nil.
*/
func BuildStateGraph[
	TObservation cmp.Ordered,
	TToken comparable,
	TTokenRole comparable,
	TNodeKind comparable,
	TLexerState comparable,
](
	pkg *syntaxa.GrammarPackage[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	tokenFormatter func(TToken) string,
) (*StateGraph[TToken, TNodeKind], error) {
	if pkg == nil || tokenFormatter == nil {
		return nil, fmt.Errorf("BuildStateGraph: nil package or tokenFormatter")
	}
	entryLabel := pkg.EntryRule
	rules := pkg.Grammars
	entryRule := rules[entryLabel]
	if entryRule == nil {
		return nil, fmt.Errorf("BuildStateGraph: entry rule %q not found", entryLabel)
	}

	ctxByKey := make(map[string]*Context)
	transitionsByID := make(map[string][]Transition[TToken, TNodeKind])
	metaByID := make(map[string]ContextMeta)
	nestBodyIDs := make(map[syntaxa.GrammarLabel]string)
	counters := make(map[string]int)

	entryTerminals, _ := lookahead[TToken, TNodeKind](entryRule, rules, make(visiting))
	rootKey := lookaheadKey(entryTerminals, tokenFormatter)
	rootCtx := &Context{ID: rootLabel, Label: rootLabel}
	ctxByKey[rootKey] = rootCtx
	if allOptionalTerminals(entryTerminals) {
		metaByID[rootLabel] = ContextMeta{HasOptionalContinuation: true}
	}

	queue := []pendingEntry[TToken, TNodeKind]{{rootLabel, rootKey, entryTerminals, entryLabel}}

	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]

		for _, term := range p.terms {
			tr := buildTransition(
				term,
				p.nameHint,
				rules,
				tokenFormatter,
				ctxByKey,
				transitionsByID,
				metaByID,
				nestBodyIDs,
				counters,
				&queue,
			)
			if tr.TargetContextIDs != nil || tr.Operation == OpPop || tr.Operation == OpMatch {
				transitionsByID[p.ctxID] = append(transitionsByID[p.ctxID], tr)
			}
		}
	}

	contexts := make([]Context, 0, len(ctxByKey))
	seen := make(map[string]struct{})
	for _, c := range ctxByKey {
		if _, ok := seen[c.ID]; !ok {
			seen[c.ID] = struct{}{}
			contexts = append(contexts, *c)
		}
	}
	for id := range nestBodyIDs {
		if c := ctxByKey["NEST_BODY:"+string(id)]; c != nil {
			if _, ok := seen[c.ID]; !ok {
				seen[c.ID] = struct{}{}
				contexts = append(contexts, *c)
			}
		}
	}

	return &StateGraph[TToken, TNodeKind]{
		Contexts:           contexts,
		ContextMeta:        metaByID,
		RootContextID:      rootLabel,
		Transitions:        transitionsByID,
		NestBodyContextIDs: nestBodyIDs,
	}, nil
}

func buildTransition[TToken, TNodeKind comparable](
	term gTerminal[TToken, TNodeKind],
	nameHint syntaxa.GrammarLabel,
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[TToken, TNodeKind],
	tokenFormatter func(TToken) string,
	ctxByKey map[string]*Context,
	transitionsByID map[string][]Transition[TToken, TNodeKind],
	metaByID map[string]ContextMeta,
	nestBodyIDs map[syntaxa.GrammarLabel]string,
	counters map[string]int,
	queue *[]pendingEntry[TToken, TNodeKind],
) Transition[TToken, TNodeKind] {
	isZeroOrMore, _ := outerFrameKind(term)

	if term.nestNode != nil {
		return buildNestTransition(term, isZeroOrMore, rules, tokenFormatter, ctxByKey, transitionsByID, metaByID, nestBodyIDs, counters, queue)
	}

	return buildStandardTransition(term, nameHint, isZeroOrMore, rules, tokenFormatter, ctxByKey, metaByID, counters, queue)
}

func getOrCreateContext[TToken, TNodeKind comparable](
	terms []gTerminal[TToken, TNodeKind],
	ownerLabel syntaxa.GrammarLabel,
	nameHint syntaxa.GrammarLabel,
	tokenFormatter func(TToken) string,
	ctxByKey map[string]*Context,
	metaByID map[string]ContextMeta,
	counters map[string]int,
	queue *[]pendingEntry[TToken, TNodeKind],
) *Context {
	key := lookaheadKey(terms, tokenFormatter)
	if c, ok := ctxByKey[key]; ok {
		return c
	}

	effectiveLabel := ownerLabel
	if (effectiveLabel == "anon" || effectiveLabel == "") && nameHint != "" {
		effectiveLabel = nameHint
	}
	base := sanitizeForLabel(string(effectiveLabel))
	if base == "" {
		base = "ctx"
	}
	n := counters[base]
	counters[base] = n + 1
	name := fmt.Sprintf("%s__%d", base, n)

	c := &Context{ID: name, Label: name}
	ctxByKey[key] = c

	if allOptionalTerminals(terms) {
		popAmt := 1
		if len(terms) > 0 && terms[0].popOffset > 0 {
			popAmt = 1 + terms[0].popOffset
		}
		metaByID[name] = ContextMeta{
			HasOptionalContinuation: true,
			FallthroughPopAmount:    popAmt,
		}
	}

	*queue = append(*queue, pendingEntry[TToken, TNodeKind]{ctxID: name, ctxKey: key, terms: terms, nameHint: effectiveLabel})
	return c
}

func sanitizeForLabel(s string) string {
	var b []byte
	for _, r := range s {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b = append(b, byte(r))
		} else if unicode.IsSpace(r) || unicode.IsPunct(r) {
			if len(b) > 0 && b[len(b)-1] != '_' {
				b = append(b, '_')
			}
		}
	}
	return strings.TrimRight(string(b), "_")
}

func getOrCreateNestBody[TToken, TNodeKind comparable](
	nestNode *syntaxa.Grammar[TToken, TNodeKind],
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[TToken, TNodeKind],
	tokenFormatter func(TToken) string,
	ctxByKey map[string]*Context,
	metaByID map[string]ContextMeta,
	counters map[string]int,
	queue *[]pendingEntry[TToken, TNodeKind],
	nestBodyIDs map[syntaxa.GrammarLabel]string,
) *Context {
	nestKey := "NEST_BODY:" + string(nestNode.GrammarLabel)
	if c, ok := ctxByKey[nestKey]; ok {
		return c
	}

	name := sanitizeForLabel(string(nestNode.GrammarLabel))
	if name == "" {
		name = "nest_body"
	}
	c := &Context{ID: name, Label: name}
	ctxByKey[nestKey] = c
	nestBodyIDs[nestNode.GrammarLabel] = name

	closeTokenNode := &syntaxa.Grammar[TToken, TNodeKind]{Kind: syntaxa.GToken, Token: *nestNode.CloseToken}
	bodyAndClose := []*syntaxa.Grammar[TToken, TNodeKind]{nestNode.Children[0], closeTokenNode}
	bodyTerminals, _ := lookaheadConcat(bodyAndClose, rules, make(visiting))

	propagatePopOffset(bodyTerminals, 1)

	if len(bodyTerminals) > 0 {
		*queue = append(*queue, pendingEntry[TToken, TNodeKind]{ctxID: name, ctxKey: nestKey, terms: bodyTerminals, nameHint: nestNode.GrammarLabel})
	}
	return c
}

func buildNestTransition[TToken, TNodeKind comparable](
	term gTerminal[TToken, TNodeKind],
	isZeroOrMore bool,
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[TToken, TNodeKind],
	tokenFormatter func(TToken) string,
	ctxByKey map[string]*Context,
	transitionsByID map[string][]Transition[TToken, TNodeKind],
	metaByID map[string]ContextMeta,
	nestBodyIDs map[syntaxa.GrammarLabel]string,
	counters map[string]int,
	queue *[]pendingEntry[TToken, TNodeKind],
) Transition[TToken, TNodeKind] {
	bodyCtx := getOrCreateNestBody(term.nestNode, rules, tokenFormatter, ctxByKey, metaByID, counters, queue, nestBodyIDs)
	ownerLabel := owningRule(term)
	nestHint := term.nestNode.GrammarLabel
	noNest := gTerminal[TToken, TNodeKind]{token: term.token, nodeKind: term.nodeKind, remaining: term.remaining, stack: term.stack, popOffset: term.popOffset}
	advTerminals := advanceTerminal(noNest, rules)

	op := OpSet
	if isZeroOrMore {
		op = OpPush
	}

	if op == OpSet {
		propagatePopOffset(advTerminals, term.popOffset)
	}

	if advTerminals == nil {
		return Transition[TToken, TNodeKind]{
			Token:            term.token,
			NodeKind:         term.nodeKind,
			Operation:        op,
			TargetContextIDs: []string{bodyCtx.ID},
		}
	}

	afterCtx := getOrCreateContext(advTerminals, ownerLabel, nestHint, tokenFormatter, ctxByKey, metaByID, counters, queue)

	meta := metaByID[afterCtx.ID]
	meta.HasOptionalContinuation = true
	if op == OpSet && term.popOffset > 0 && meta.FallthroughPopAmount == 0 {
		meta.FallthroughPopAmount = 1 + term.popOffset
		metaByID[afterCtx.ID] = meta
	}

	return Transition[TToken, TNodeKind]{
		Token:            term.token,
		NodeKind:         term.nodeKind,
		Operation:        op,
		TargetContextIDs: []string{afterCtx.ID, bodyCtx.ID},
	}
}

func buildStandardTransition[TToken, TNodeKind comparable](
	term gTerminal[TToken, TNodeKind],
	nameHint syntaxa.GrammarLabel,
	isZeroOrMore bool,
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[TToken, TNodeKind],
	tokenFormatter func(TToken) string,
	ctxByKey map[string]*Context,
	metaByID map[string]ContextMeta,
	counters map[string]int,
	queue *[]pendingEntry[TToken, TNodeKind],
) Transition[TToken, TNodeKind] {
	ownerLabel := owningRule(term)

	var advTerminals []gTerminal[TToken, TNodeKind]
	if isZeroOrMore {
		stripped := gTerminal[TToken, TNodeKind]{
			token:     term.token,
			nodeKind:  term.nodeKind,
			remaining: term.remaining,
			stack:     term.stack[:len(term.stack)-1],
			popOffset: term.popOffset,
		}
		advTerminals = advanceTerminal(stripped, rules)
	} else {
		advTerminals = advanceTerminal(term, rules)
		propagatePopOffset(advTerminals, term.popOffset)
	}

	if isZeroOrMore {
		if advTerminals == nil {
			return Transition[TToken, TNodeKind]{Token: term.token, NodeKind: term.nodeKind, Operation: OpMatch}
		}
		next := getOrCreateContext(advTerminals, ownerLabel, nameHint, tokenFormatter, ctxByKey, metaByID, counters, queue)
		return Transition[TToken, TNodeKind]{
			Token:            term.token,
			NodeKind:         term.nodeKind,
			Operation:        OpPush,
			TargetContextIDs: []string{next.ID},
		}
	}

	if advTerminals == nil {
		return Transition[TToken, TNodeKind]{
			Token:     term.token,
			NodeKind:  term.nodeKind,
			Operation: OpPop,
			PopAmount: 1 + term.popOffset,
		}
	}

	next := getOrCreateContext(advTerminals, ownerLabel, nameHint, tokenFormatter, ctxByKey, metaByID, counters, queue)
	return Transition[TToken, TNodeKind]{
		Token:            term.token,
		NodeKind:         term.nodeKind,
		Operation:        OpSet,
		TargetContextIDs: []string{next.ID},
	}
}

func propagatePopOffset[TToken, TNodeKind comparable](terminals []gTerminal[TToken, TNodeKind], offset int) {
	for i := range terminals {
		terminals[i].popOffset = offset
	}
}
