package lowering

import (
	"cmp"
	"fmt"
	"foundation/hash"
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

ImmediatePushTargetID is set for nest body contexts and names the inner context
that the editor backend should immediately push onto the stack when entering this
state.
*/
type ContextMeta struct {
	ImmediatePushTargetID string
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
	NodeKind         *TNodeKind
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

Nest transitions:
  - Optional / mandatory nests (not in a ZeroOrMore): OpSet with targets
    [afterCtxID, bodyCtxID]. After the body closes the machine is in afterCtxID.
  - ZeroOrMore nests: OpPush with a single target [bodyCtxID]. No separate afterCtx
    frame is pushed; after the body closes via OpPop the machine returns to the
    calling context, which already holds all loop re-entry and outer follow-set
    transitions via lookaheadConcat suffix propagation.

For contexts where all grammar transitions are optional, explicit OpPop transitions
are emitted for each token in the follow set (the First set of remaining grammar rules
after the optional block). These ensure that a parent context's expected tokens trigger
a precise pop rather than relying on a wildcard fallthrough. Tokens that are not in
the follow set and not otherwise handled are left for the editor backend's invalid
fallback (e.g. a catch-all \S match that marks unrecognised input as invalid).
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
	ctxKey   uint64 // Updated to support binary hashing
	terms    []gTerminal[TToken, TNodeKind]
	nameHint syntaxa.GrammarLabel
}

/*
BuildStateGraph builds a generic StateGraph from a GrammarPackage.

tokenHash and hasher are used for high-performance, zero-allocation context keys.
Transition order is discovery order; PopAmount reflects grammar stack frames exited.
The editor layer (e.g. langspec/editor) applies priority sort, nest split, and invalid
fallback handling.

ZeroOrMore nests emit OpPush with a single target [bodyCtxID]. After the body closes
via OpPop the machine returns to the calling context, which already contains all loop
re-entry and outer follow-set transitions. This avoids a per-iteration afterCtx frame
that would cause outer close tokens to be consumed at the wrong stack level.

For Optional nests and mandatory nests, OpSet is used with two targets
[afterCtxID, bodyCtxID]; the afterCtx holds the continuation.

When all transitions from a context are optional (GOptional frames), the lowering pass
computes the First set of remaining grammar rules after the optional block and emits
explicit OpPop transitions for those follow-set tokens. Tokens not covered by any
normal or follow-set transition are handled by the editor backend's invalid fallback
(e.g. \S catch-all).

Prerequisites:
- pkg must be non-nil with a valid entry rule; tokenHash and hasher must be non-nil.

Edge cases:
- Returns an error if the entry rule is missing or hashing components are nil.
*/
func BuildStateGraph[
	TObservation cmp.Ordered,
	TToken comparable,
	TTokenRole comparable,
	TNodeKind comparable,
	TLexerState comparable,
](
	pkg *syntaxa.GrammarPackage[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	tokenHash func(TToken) uint64,
	hasher *hash.XXH3Hasher,
) (*StateGraph[TToken, TNodeKind], error) {
	if pkg == nil || tokenHash == nil || hasher == nil {
		return nil, fmt.Errorf("BuildStateGraph: nil package, tokenHash, or hasher")
	}
	entryLabel := pkg.EntryRule
	rules := pkg.Grammars
	entryRule := rules[entryLabel]
	if entryRule == nil {
		return nil, fmt.Errorf("BuildStateGraph: entry rule %q not found", entryLabel)
	}

	ctxByKey := make(map[uint64]*Context)
	transitionsByID := make(map[string][]Transition[TToken, TNodeKind])
	metaByID := make(map[string]ContextMeta)
	nestBodyIDs := make(map[syntaxa.GrammarLabel]string)

	entryTerminals, _ := lookahead(entryRule, rules, make(visiting))
	rootKey := lookaheadKey(entryTerminals, tokenHash, hasher)
	rootCtx := &Context{ID: rootLabel, Label: rootLabel}
	ctxByKey[rootKey] = rootCtx

	queue := []pendingEntry[TToken, TNodeKind]{{rootLabel, rootKey, entryTerminals, entryLabel}}

	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]

		for _, term := range p.terms {
			tr := buildTransition(
				term, p.nameHint, rules, tokenHash, hasher,
				ctxByKey, transitionsByID, metaByID, nestBodyIDs, &queue,
			)
			if tr.TargetContextIDs != nil || tr.Operation == OpPop || tr.Operation == OpMatch {
				transitionsByID[p.ctxID] = append(transitionsByID[p.ctxID], tr)
			}
		}

		// For contexts where every terminal is optional, emit explicit OpPop transitions
		// for each token in the follow set (First set of grammar after the optional block).
		// This replaces the former HasOptionalContinuation/FallthroughPopAmount fallthrough
		// hint with precise, grammar-driven pop edges keyed on specific parent tokens.
		if allOptionalTerminals(p.terms) {
			popAmt := 1
			if len(p.terms) > 0 && p.terms[0].popOffset > 0 {
				popAmt = 1 + p.terms[0].popOffset
			}
			existing := make(map[TToken]struct{}, len(transitionsByID[p.ctxID]))
			for _, tr := range transitionsByID[p.ctxID] {
				existing[tr.Token] = struct{}{}
			}
			for _, tr := range computeFollowSetTransitions(p.terms, rules, existing, popAmt) {
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
	tokenHash func(TToken) uint64,
	hasher *hash.XXH3Hasher,
	ctxByKey map[uint64]*Context,
	transitionsByID map[string][]Transition[TToken, TNodeKind],
	metaByID map[string]ContextMeta,
	nestBodyIDs map[syntaxa.GrammarLabel]string,
	queue *[]pendingEntry[TToken, TNodeKind],
) Transition[TToken, TNodeKind] {
	isZeroOrMore, _ := outerFrameKind(term)

	if term.nestNode != nil {
		return buildNestTransition(term, isZeroOrMore, rules, tokenHash, hasher, ctxByKey, metaByID, nestBodyIDs, queue)
	}

	return buildStandardTransition(term, nameHint, isZeroOrMore, rules, tokenHash, hasher, ctxByKey, metaByID, queue)
}

func getOrCreateContext[TToken, TNodeKind comparable](
	terms []gTerminal[TToken, TNodeKind],
	ownerLabel syntaxa.GrammarLabel,
	nameHint syntaxa.GrammarLabel,
	tokenHash func(TToken) uint64,
	hasher *hash.XXH3Hasher,
	ctxByKey map[uint64]*Context,
	metaByID map[string]ContextMeta,
	queue *[]pendingEntry[TToken, TNodeKind],
) *Context {
	key := lookaheadKey(terms, tokenHash, hasher)
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

	name := fmt.Sprintf("%s_%08X", base, uint32(key))

	c := &Context{ID: name, Label: name}
	ctxByKey[key] = c

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
	tokenHash func(TToken) uint64,
	hasher *hash.XXH3Hasher,
	ctxByKey map[uint64]*Context,
	metaByID map[string]ContextMeta,
	queue *[]pendingEntry[TToken, TNodeKind],
	nestBodyIDs map[syntaxa.GrammarLabel]string,
) *Context {
	nestKeyBytes := []byte("NEST_BODY:" + string(nestNode.GrammarLabel))
	nestKey := hash.XXH3HasherHash64(hasher, nestKeyBytes)

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

	contentName := name + "_content"
	contentKeyBytes := []byte("NEST_CONTENT:" + string(nestNode.GrammarLabel))
	contentKey := hash.XXH3HasherHash64(hasher, contentKeyBytes)
	contentCtx := &Context{ID: contentName, Label: contentName}
	ctxByKey[contentKey] = contentCtx

	metaByID[name] = ContextMeta{
		ImmediatePushTargetID: contentName,
	}

	closeTokenNode := &syntaxa.Grammar[TToken, TNodeKind]{Kind: syntaxa.GToken, Token: *nestNode.CloseToken}
	bodyAndClose := []*syntaxa.Grammar[TToken, TNodeKind]{nestNode.Children[0], closeTokenNode}
	bodyTerminals, _ := lookaheadConcat(bodyAndClose, rules, make(visiting))

	propagatePopOffset(bodyTerminals, 1)

	if len(bodyTerminals) > 0 {
		*queue = append(*queue, pendingEntry[TToken, TNodeKind]{
			ctxID:    contentName,
			ctxKey:   contentKey,
			terms:    bodyTerminals,
			nameHint: nestNode.GrammarLabel,
		})
	}
	return c
}

func buildNestTransition[TToken, TNodeKind comparable](
	term gTerminal[TToken, TNodeKind],
	isZeroOrMore bool,
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[TToken, TNodeKind],
	tokenHash func(TToken) uint64,
	hasher *hash.XXH3Hasher,
	ctxByKey map[uint64]*Context,
	metaByID map[string]ContextMeta,
	nestBodyIDs map[syntaxa.GrammarLabel]string,
	queue *[]pendingEntry[TToken, TNodeKind],
) Transition[TToken, TNodeKind] {
	bodyCtx := getOrCreateNestBody(term.nestNode, rules, tokenHash, hasher, ctxByKey, metaByID, queue, nestBodyIDs)

	// For ZeroOrMore nests (op=OpPush) we do NOT create a separate afterCtx.
	//
	// The calling context already holds every transition that would appear in an
	// afterCtx frame, because lookaheadConcat propagated the suffix of the
	// surrounding grammar into the calling context's terminal set when that context
	// was first computed. This means:
	//   - Loop re-entry tokens (the next ZeroOrMore iteration) are present.
	//   - Outer follow-set tokens (e.g. a parent nest's close token) are present
	//     with their correct popOffset, producing the right OpPop(N) amount.
	//
	// If we pushed an afterCtx frame for each iteration, outer close tokens would
	// fire OpPop(1) inside that extra frame—consuming the token before the parent
	// context could handle it, leaving the parent's close transition unreachable
	// and causing all subsequent tokens to be marked invalid.
	//
	// After OpPop closes the nest body, the machine returns directly to the calling
	// context which naturally handles both loop re-entry and outer continuation.
	if isZeroOrMore {
		return Transition[TToken, TNodeKind]{
			Token:            term.token,
			NodeKind:         term.nodeKind,
			Operation:        OpPush,
			TargetContextIDs: []string{bodyCtx.ID},
		}
	}

	ownerLabel := owningRule(term)
	nestHint := term.nestNode.GrammarLabel
	noNest := gTerminal[TToken, TNodeKind]{token: term.token, nodeKind: term.nodeKind, remaining: term.remaining, stack: term.stack, popOffset: term.popOffset}
	advTerminals := advanceTerminal(noNest, rules)
	propagatePopOffset(advTerminals, term.popOffset)

	if advTerminals == nil {
		return Transition[TToken, TNodeKind]{
			Token:            term.token,
			NodeKind:         term.nodeKind,
			Operation:        OpSet,
			TargetContextIDs: []string{bodyCtx.ID},
		}
	}

	afterCtx := getOrCreateContext(advTerminals, ownerLabel, nestHint, tokenHash, hasher, ctxByKey, metaByID, queue)

	return Transition[TToken, TNodeKind]{
		Token:            term.token,
		NodeKind:         term.nodeKind,
		Operation:        OpSet,
		TargetContextIDs: []string{afterCtx.ID, bodyCtx.ID},
	}
}

func buildStandardTransition[TToken, TNodeKind comparable](
	term gTerminal[TToken, TNodeKind],
	nameHint syntaxa.GrammarLabel,
	isZeroOrMore bool,
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[TToken, TNodeKind],
	tokenHash func(TToken) uint64,
	hasher *hash.XXH3Hasher,
	ctxByKey map[uint64]*Context,
	metaByID map[string]ContextMeta,
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
		next := getOrCreateContext(advTerminals, ownerLabel, nameHint, tokenHash, hasher, ctxByKey, metaByID, queue)
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

	next := getOrCreateContext(advTerminals, ownerLabel, nameHint, tokenHash, hasher, ctxByKey, metaByID, queue)
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

func owningRule[TToken, TNodeKind comparable](term gTerminal[TToken, TNodeKind]) syntaxa.GrammarLabel {
	// Priority 1: Named non-repetition references (Direct rule calls)
	for i := 0; i < len(term.stack); i++ {
		if !term.stack[i].isRepetition && term.stack[i].label != "" {
			return term.stack[i].label
		}
	}
	// Priority 2: Named repetitions
	for i := 0; i < len(term.stack); i++ {
		if term.stack[i].isRepetition && term.stack[i].label != "" {
			return term.stack[i].label
		}
	}
	return "anon"
}

func outerFrameKind[TToken, TNodeKind comparable](term gTerminal[TToken, TNodeKind]) (zeroOrMore bool, optional bool) {
	if len(term.stack) == 0 {
		return false, false
	}
	outer := term.stack[len(term.stack)-1]
	if outer.repeatNode == nil {
		return false, false
	}

	switch outer.repeatNode.Kind {
	case syntaxa.GRepeat:
		return true, false
	case syntaxa.GOptional:
		return false, true
	default:
		return false, false
	}
}

func allOptionalTerminals[TToken, TNodeKind comparable](terminals []gTerminal[TToken, TNodeKind]) bool {
	if len(terminals) == 0 {
		return false
	}
	for _, t := range terminals {
		_, isOpt := outerFrameKind(t)
		if !isOpt {
			return false
		}
	}
	return true
}

/*
computeFollowSetTransitions computes explicit OpPop transitions for every token in
the follow set of an all-optional context.

For each optional terminal (outerFrameKind returns optional), the follow set is the
First set of the remaining grammar after the optional block. A synthetic terminal whose
remaining is set to optFrame.remaining (the grammar nodes that follow the optional in
the parent production) and whose stack is the outer frames above the optional is passed
to advanceTerminal. The resulting lookahead terminals supply the follow-set tokens.

existing must contain the tokens already covered by normal transitions in the context;
follow-set tokens already present are skipped to avoid ambiguous duplicates. popAmount
is the number of machine stack frames to pop on each emitted OpPop transition.
*/
func computeFollowSetTransitions[TToken, TNodeKind comparable](
	terms []gTerminal[TToken, TNodeKind],
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[TToken, TNodeKind],
	existing map[TToken]struct{},
	popAmount int,
) []Transition[TToken, TNodeKind] {
	seen := make(map[TToken]struct{})
	var result []Transition[TToken, TNodeKind]

	for _, term := range terms {
		_, isOpt := outerFrameKind(term)
		if !isOpt {
			continue
		}

		// The optional frame is the outermost (last) stack entry. The stack is built
		// innermost-first: lookaheadRepetition/lookaheadReference each append their
		// frame to the end, so stack[0] is the innermost (closest to the token) and
		// stack[len-1] is the outermost enclosing repetition or reference. For an
		// all-optional context, outerFrameKind checks stack[len-1] and returns optional
		// only when that outermost frame is a GOptional node.
		// outerFrameKind returns (false, false) for an empty stack, so isOpt is false
		// and we continue before reaching here; the length check is therefore always
		// satisfied, but is made explicit below for clarity.
		if len(term.stack) == 0 {
			continue
		}
		optFrame := term.stack[len(term.stack)-1]
		outerStack := term.stack[:len(term.stack)-1]

		// Construct a synthetic terminal whose "remaining" is the grammar after the
		// optional block and whose stack is the outer context. advanceTerminal uses
		// only term.remaining and term.stack (never term.token), so term.token here is
		// a zero-value placeholder. advanceTerminal computes the First set of that
		// position, propagating through nullable outer frames exactly as it would for
		// a real terminal.
		synth := gTerminal[TToken, TNodeKind]{
			remaining: optFrame.remaining,
			stack:     outerStack,
			popOffset: term.popOffset,
		}

		exitTerms := advanceTerminal(synth, rules)
		for _, et := range exitTerms {
			if _, already := seen[et.token]; already {
				continue
			}
			if _, inExisting := existing[et.token]; inExisting {
				continue
			}
			seen[et.token] = struct{}{}
			result = append(result, Transition[TToken, TNodeKind]{
				Token:     et.token,
				NodeKind:  et.nodeKind,
				Operation: OpPop,
				PopAmount: popAmount,
			})
		}
	}

	return result
}
