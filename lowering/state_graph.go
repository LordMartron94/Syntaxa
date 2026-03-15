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

OpRecoverPop and OpRecoverNoConsume are recovery-only operations emitted for every
state that has active recovery tokens (i.e. the union of RecoveryTokens and
NoConsumeOnRecoveryTokens from all grammar rules in the terminal stack for that
state). They mirror the behaviour of performRecovery in the parser:

  - OpRecoverPop: the token is a sync point and should be consumed; the consumer
    should pop the appropriate number of frames and resume normal parsing.
  - OpRecoverNoConsume: the token is a sync point but must NOT be consumed; the
    consumer should pop frame(s) and leave the token in the stream so an ancestor
    context can process it via its own normal transitions.

For both recovery operations PopAmount is 0, indicating that the consumer is
responsible for determining the correct pop depth from its own runtime stack.
*/
type StackOp uint8

const (
	OpMatch StackOp = iota
	OpPush
	OpPop
	OpSet
	OpSyncToken          // recovery sync token: consume and pop frame(s)
	OpSyncTokenNoConsume // recovery sync token: do NOT consume, pop frame(s)
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
	ImmediatePushTargetID   string
}

/*
Transition is a single edge in the state graph.

On Token (with optional semantic NodeKind for highlighting scope), apply Operation
and move to TargetContextIDs. PopAmount is used when Operation is OpPop and holds
the number of grammar stack frames exited. NodeKind identifies what is being matched
for editor backends (e.g. syntax highlighting scope).

Recovery transitions (OpRecoverPop, OpRecoverNoConsume) are added for every token
in the union of all active grammar rule recovery sets at that state. These transitions
have PopAmount = 0; the consumer determines the correct pop depth at runtime.

IsRecoveryTransition is true when the transition is a sync-point recovery edge
(Operation is OpRecoverPop or OpRecoverNoConsume). The Token is a valid sync
token with its normal semantic scope carried by NodeKind; it is NOT an error.
Consumers must use NodeKind for syntax-highlighting and must NOT apply an error
scope to these transitions. The invalid.illegal.unexpected-token scope should
instead be applied to a synthesised catch-all rule for input that matches none
of the normal or recovery transitions.
*/
type Transition[TToken, TNodeKind comparable] struct {
	Token                TToken
	NodeKind             *TNodeKind
	Operation            StackOp
	TargetContextIDs     []string
	PopAmount            int
	IsRecoveryTransition bool
}

/*
StateGraph is the generic, editor-agnostic result of lowering a grammar to a state machine.

Contexts are nodes; Transitions are keyed by context ID. RootContextID is the entry
state. NestBodyContextIDs maps nest grammar labels to the context ID for that nest's
body. Transition order per state is discovery order; the editor backend sorts by
lexer priority.

Every state that has active recovery tokens (from the grammar rules in the terminal
stack for that state) will also contain OpRecoverPop and/or OpRecoverNoConsume
transitions in its Transitions entry. These recovery transitions carry the union of
all recovery sets from all enclosing grammar frames, exactly mirroring the
currentRecoveryAllFrames() computation performed by the parser at runtime.
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
	ctxID          string
	ctxKey         uint64
	terms          []gTerminal[TToken, TNodeKind]
	nameHint       syntaxa.GrammarLabel
	inheritedSyncs []TToken
}

/*
BuildStateGraph builds a generic StateGraph from a GrammarPackage.

tokenHash and hasher are used for high-performance, zero-allocation context keys.
Transition order is discovery order; PopAmount reflects grammar stack frames exited.
The editor layer (e.g. langspec/editor) applies priority sort, nest split, and fallthrough.

Every state whose terminal stack references grammar rules with recovery tokens will
have OpRecoverPop (consume) and/or OpRecoverNoConsume transitions appended. These
mirror the parser's currentRecoveryAllFrames() recovery set for that state, so
consumers can implement identical recovery behaviour without access to the parser.

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
	if allOptionalTerminals(entryTerminals) {
		metaByID[rootLabel] = ContextMeta{HasOptionalContinuation: true}
	}

	queue := []pendingEntry[TToken, TNodeKind]{{
		ctxID: rootLabel, ctxKey: rootKey, terms: entryTerminals, nameHint: entryLabel, inheritedSyncs: nil,
	}}

	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]

		for _, term := range p.terms {
			tr := buildTransition(
				term, p.nameHint, rules, tokenHash, hasher,
				ctxByKey, transitionsByID, metaByID, nestBodyIDs, &queue, p.inheritedSyncs,
			)
			if tr.TargetContextIDs != nil || tr.Operation == OpPop || tr.Operation == OpMatch {
				transitionsByID[p.ctxID] = append(transitionsByID[p.ctxID], tr)
			}
		}

		addRecoveryTransitions(p.terms, rules, p.ctxID, transitionsByID, p.inheritedSyncs)
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
	inheritedSyncs []TToken,
) Transition[TToken, TNodeKind] {
	isZeroOrMore, _ := outerFrameKind(term)

	if term.nestNode != nil {
		return buildNestTransition(term, isZeroOrMore, rules, tokenHash, hasher, ctxByKey, transitionsByID, metaByID, nestBodyIDs, queue, inheritedSyncs)
	}

	return buildStandardTransition(term, nameHint, isZeroOrMore, rules, tokenHash, hasher, ctxByKey, metaByID, queue, inheritedSyncs)
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
	inheritedSyncs []TToken,
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

	*queue = append(*queue, pendingEntry[TToken, TNodeKind]{
		ctxID: name, ctxKey: key, terms: terms, nameHint: effectiveLabel, inheritedSyncs: inheritedSyncs,
	})
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
	inheritedSyncs []TToken,
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

	newSyncs := append([]TToken(nil), inheritedSyncs...)
	if nestNode.CloseToken != nil {
		newSyncs = append(newSyncs, *nestNode.CloseToken)
	}

	if len(bodyTerminals) > 0 {
		*queue = append(*queue, pendingEntry[TToken, TNodeKind]{
			ctxID:          contentName,
			ctxKey:         contentKey,
			terms:          bodyTerminals,
			nameHint:       nestNode.GrammarLabel,
			inheritedSyncs: newSyncs,
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
	transitionsByID map[string][]Transition[TToken, TNodeKind],
	metaByID map[string]ContextMeta,
	nestBodyIDs map[syntaxa.GrammarLabel]string,
	queue *[]pendingEntry[TToken, TNodeKind],
	inheritedSyncs []TToken,
) Transition[TToken, TNodeKind] {
	bodyCtx := getOrCreateNestBody(term.nestNode, rules, tokenHash, hasher, ctxByKey, metaByID, queue, nestBodyIDs, inheritedSyncs)
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

	afterCtx := getOrCreateContext(advTerminals, ownerLabel, nestHint, tokenHash, hasher, ctxByKey, metaByID, queue, inheritedSyncs)

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
	tokenHash func(TToken) uint64,
	hasher *hash.XXH3Hasher,
	ctxByKey map[uint64]*Context,
	metaByID map[string]ContextMeta,
	queue *[]pendingEntry[TToken, TNodeKind],
	inheritedSyncs []TToken,
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
		next := getOrCreateContext(advTerminals, ownerLabel, nameHint, tokenHash, hasher, ctxByKey, metaByID, queue, inheritedSyncs)
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

	next := getOrCreateContext(advTerminals, ownerLabel, nameHint, tokenHash, hasher, ctxByKey, metaByID, queue, inheritedSyncs)
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

func addRecoveryTransitions[TToken, TNodeKind comparable](
	terms []gTerminal[TToken, TNodeKind],
	rules map[syntaxa.GrammarLabel]*syntaxa.Grammar[TToken, TNodeKind],
	ctxID string,
	transitionsByID map[string][]Transition[TToken, TNodeKind],
	inheritedSyncs []TToken,
) {
	seenLabels := make(map[syntaxa.GrammarLabel]struct{}, len(terms))
	for _, term := range terms {
		for _, entry := range term.stack {
			if entry.label != "" {
				seenLabels[entry.label] = struct{}{}
			}
		}
	}

	consumeSet := make(map[TToken]struct{}, 4)
	noConsumeSet := make(map[TToken]struct{}, 4+len(inheritedSyncs))

	// Treat parent boundaries as no-consume tokens so the parent state can execute the pop naturally
	for _, t := range inheritedSyncs {
		noConsumeSet[t] = struct{}{}
	}

	for label := range seenLabels {
		rule := rules[label]
		if rule == nil {
			continue
		}
		for _, t := range rule.RecoveryTokens {
			consumeSet[t] = struct{}{}
		}
		for _, t := range rule.NoConsumeOnRecoveryTokens {
			noConsumeSet[t] = struct{}{}
		}
	}

	if len(consumeSet) == 0 && len(noConsumeSet) == 0 {
		return
	}

	for t := range noConsumeSet {
		delete(consumeSet, t)
	}

	for t := range consumeSet {
		transitionsByID[ctxID] = append(transitionsByID[ctxID], Transition[TToken, TNodeKind]{
			Token:                t,
			Operation:            OpSyncToken,
			IsRecoveryTransition: true,
		})
	}
	for t := range noConsumeSet {
		transitionsByID[ctxID] = append(transitionsByID[ctxID], Transition[TToken, TNodeKind]{
			Token:                t,
			Operation:            OpSyncTokenNoConsume,
			IsRecoveryTransition: true,
		})
	}
}
