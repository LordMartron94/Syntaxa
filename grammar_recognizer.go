package syntaxa

import (
	"autarch"
	"autarch/pattern"
	"cmp"
	"fmt"
	"foundation/bytes"
	"foundation/extensions"
	"foundation/formatting"
	"foundation/hash"
	"memarch"
	"memcore"
	"memstruct"
	"slices"
	"strings"
)

var xxh3Hasher = hash.XXH3HasherCreateWithSeed(42)

// -------------------------------------------------------------- TYPES

/*
GrammarToRecognitionStateMachineConfiguration encapsulates the necessary configuration for the
pipeline.
*/
type GrammarToRecognitionStateMachineConfiguration struct {
	scratchAllocationFn, mainAllocationFn memarch.AllocationFn

	minScratchMem, maxScratchMem memcore.MemoryUnitBytes
}

/*
GrammarToRecognitionStateMachineConfigurationCreate creates a default pipeline configuration.

The default (min, max) for the scratch allocators created by subrules is (1kb, 1gb).
These are thus used INDIVIDUALLY for separate Pattern -> Minimized DFA stages.
*/
func GrammarToRecognitionStateMachineConfigurationCreate(
	scratchAllocationFn, mainAllocationFn memarch.AllocationFn,
) *GrammarToRecognitionStateMachineConfiguration {
	return &GrammarToRecognitionStateMachineConfiguration{
		scratchAllocationFn: scratchAllocationFn,
		mainAllocationFn:    mainAllocationFn,
		minScratchMem:       memcore.KiloByte,
		maxScratchMem:       memcore.GigaByte,
	}
}

/*
WithScratchMemorySize sets the (min, max) memory used for the scratch allocators.

These are thus used INDIVIDUALLY for separate Pattern -> Minimized DFA stages.
*/
func (g *GrammarToRecognitionStateMachineConfiguration) WithScratchMemorySize(min, max memcore.MemoryUnitBytes) *GrammarToRecognitionStateMachineConfiguration {
	g.minScratchMem = min
	g.maxScratchMem = max
	return g
}

/* RecognitionAnnotation provides access into annotations for the recognition pattern. */
type RecognitionAnnotation struct {
	GrammarIDs []string
}

func (r *RecognitionAnnotation) Key() uint64 {
	sorted := extensions.SortedCopyShallow(r.GrammarIDs, func(a, b string) int { return cmp.Compare(a, b) })
	contentBytes := bytes.StringSliceToBytes(sorted, 0x00)
	return hash.XXH3HasherHash64(xxh3Hasher, contentBytes)
}

/* RecognitionStateTransition defines a transition from state X to state Y for token. */
type RecognitionStateTransition[TToken comparable] struct {
	To uint64
	On TToken
}

type TokenFormatter[TToken comparable] struct {
	patternFormatter pattern.ObservationFormatter[TToken]
	toString         func(token TToken) string
}

func TokenFormatterCreate[TToken comparable](
	patternFormatter pattern.ObservationFormatter[TToken],
	toString func(token TToken) string,
) TokenFormatter[TToken] {
	return TokenFormatter[TToken]{
		patternFormatter: patternFormatter,
		toString:         toString,
	}
}

// -------------------------------------------------------------- STATE MACHINE

/* RecognitionStateMachine is a type of DFA over the regular grammar language. */
type RecognitionStateMachine[TToken comparable] struct {
	nfa *autarch.NFA[TToken, pattern.AnnotatedOutcome[bool]]
	dfa *autarch.DFA[TToken, pattern.AnnotatedOutcome[bool]]

	minimizedDFA       *autarch.DFA[TToken, pattern.AnnotatedOutcome[bool]]
	minimizedDFACursor memstruct.ArrayCursor[uint64]
	annotationMap      map[pattern.AnnotationID]RecognitionAnnotation

	tokenFormatter TokenFormatter[TToken]
}

/*
Run runs a sequence of tokens and returns whether the resulting state is reachable and the associated annotation.
*/
func (r *RecognitionStateMachine[TToken]) Run(sequence ...TToken) (
	accepting bool,
	annotation *RecognitionAnnotation,
) {
	outcome, err := autarch.DFARun(r.minimizedDFA, sequence)
	if err != nil {
		panic(fmt.Errorf("engine error detected during dfa execution: %w", err))
	}

	if outcome.Annotation == nil {
		return outcome.Value, nil
	}

	ann := r.annotationMap[*outcome.Annotation]
	return outcome.Value, &ann
}

/*
Step returns the next state for a (currentState, token) combination.
*/
func (r *RecognitionStateMachine[TToken]) Step(state uint64, token TToken) uint64 {
	outState, err := autarch.DFAStep(r.minimizedDFA, state, token, r.minimizedDFACursor)
	if err != nil {
		panic(fmt.Errorf("engine error detected during dfa execution: %w", err))
	}
	return outState
}

/* IsAccepting checks whether a state is accepting (outcome.Value is true). */
func (r *RecognitionStateMachine[TToken]) IsAccepting(state uint64) bool {
	outcome, _ := autarch.DFAStateOutcome(r.minimizedDFA, state)
	return outcome.Value
}

/* AnnotationForState retrieves the annotation for the current state. */
func (r *RecognitionStateMachine[TToken]) AnnotationForState(state uint64) *RecognitionAnnotation {
	outcome, _ := autarch.DFAStateOutcome(r.minimizedDFA, state)
	if outcome.Annotation == nil {
		return nil
	}

	annotation := r.annotationMap[*outcome.Annotation]
	return &annotation
}

/* ExpectedTokens returns the tokens that are available for a successful transition in the current state. */
func (r *RecognitionStateMachine[TToken]) ExpectedTokens(state uint64) []TToken {
	symbols := autarch.DFAAvailableSymbols(r.minimizedDFA, state)
	out := make([]TToken, len(symbols))

	for i, symbol := range symbols {
		if symbol.Observation == nil {
			panic("engine error detected during dfa execution: symbol must be non-nil")
		}

		out[i] = *symbol.Observation
	}

	return out
}

/* AvailableTransitionsFrom returns the transitions from this state that are possible. */
func (r *RecognitionStateMachine[TToken]) AvailableTransitionsFrom(state uint64) []RecognitionStateTransition[TToken] {
	transitions := autarch.DFATransitionsFrom(r.minimizedDFA, state)
	out := make([]RecognitionStateTransition[TToken], len(transitions))

	for i, transition := range transitions {
		if transition.Symbol.Observation == nil {
			panic("engine error detected during dfa execution: symbol must be non-nil")
		}

		out[i] = RecognitionStateTransition[TToken]{
			To: transition.Target,
			On: *transition.Symbol.Observation,
		}
	}

	return out
}

/* DebugStateMachine provides a dump of the DFA recognizer. */
func (r *RecognitionStateMachine[TToken]) DebugStateMachine() string {
	formatSymbol := func(id uint64, def autarch.SymbolDefinition[TToken]) string {
		if def.Observation != nil {
			return fmt.Sprintf("symbol{id=%d,token=%s}", id, r.tokenFormatter.toString(*def.Observation))
		}

		return fmt.Sprintf("symbol{id=%d,name=%s}", id, def.Name)
	}

	nfaFormatter := &autarch.NFADebugFormatter[TToken, pattern.AnnotatedOutcome[bool]]{
		FormatSymbolName: formatSymbol,
		FormatSymbolID: func(symbolID uint64) string {
			return fmt.Sprintf("%3d", symbolID)
		},
		FormatStateID: func(stateID uint64) string {
			return fmt.Sprintf("%3d", stateID)
		},
		FormatStateOutcome: func(outcome pattern.AnnotatedOutcome[bool]) string {
			return formatStateOutcome(r.annotationMap, outcome)
		},
		FormatStateIndicator: func(_ uint64, outcome pattern.AnnotatedOutcome[bool]) string {
			return formatStateIndicator(outcome, false)
		},
	}

	dfaFormatter := &autarch.DFADebugFormatter[TToken, pattern.AnnotatedOutcome[bool]]{
		FormatSymbolName: formatSymbol,
		FormatSymbolID: func(symbolID uint64) string {
			return fmt.Sprintf("%3d", symbolID)
		},
		FormatStateID: func(stateID uint64) string {
			return fmt.Sprintf("%3d", stateID)
		},
		FormatStateOutcome: func(outcome pattern.AnnotatedOutcome[bool]) string {
			return formatStateOutcome(r.annotationMap, outcome)
		},
		FormatStateIndicator: func(state uint64, outcome pattern.AnnotatedOutcome[bool], isDead bool) string {
			return formatStateIndicator(outcome, isDead)
		},
	}

	builder := &strings.Builder{}

	builder.WriteString("------ NFA: ------\n\n")

	nfaOut := autarch.NFAOutcomesGet(r.nfa)
	nfaOutcomeState1, _ := memstruct.ArrayItemGetAt[pattern.AnnotatedOutcome[TToken]](nfaOut, 1)

	var stateString string
	if nfaOutcomeState1.Annotation != nil {
		stateString = fmt.Sprintf("State 1 Annotation ID: %d\n", *nfaOutcomeState1.Annotation)
	} else {
		stateString = "State 1 Annotation is actually nil in memory.\n"
	}

	builder.WriteString(stateString)
	builder.WriteString("\n")

	nfaDebug := autarch.NFADebugPrint(r.nfa, nfaFormatter)
	builder.WriteString(nfaDebug)
	builder.WriteString("\n")

	builder.WriteString("------ Pre-Minimized DFA: ------\n\n")
	dfaDebug := autarch.DFADebugPrint(r.dfa, dfaFormatter)
	builder.WriteString(dfaDebug)
	builder.WriteString("\n")

	builder.WriteString("------ Minimized DFA: ------\n\n")
	minimizedDebug := autarch.DFADebugPrint(r.minimizedDFA, dfaFormatter)
	builder.WriteString(minimizedDebug)

	return builder.String()
}

// -------------------------------------------------------------- RECOGNIZER

/*
ToRecognitionStateMachine produces a DFA that recognizes this syntax (provided the grammar is regular)

Note that this is NOT a replacement for a runtime AST generator.
*/
func (g *Grammar[TToken]) ToRecognitionStateMachine(
	config *GrammarToRecognitionStateMachineConfiguration,
	cmpFn func(a, b TToken) int,
	tokenFormatter TokenFormatter[TToken],
) (*RecognitionStateMachine[TToken], error) {
	grammarPattern, annotationMap, err := g.ToRecognitionPattern(cmpFn)
	if err != nil {
		return nil, err
	}

	return g.ToRecognitionStateMachineWithPattern(config, cmpFn, tokenFormatter, grammarPattern, annotationMap), nil
}

/*
ToRecognitionStateMachineWithPattern produces a DFA that recognizes this syntax (provided the grammar is regular)

Note that this is NOT a replacement for a runtime AST generator.
*/
func (g *Grammar[TToken]) ToRecognitionStateMachineWithPattern(
	config *GrammarToRecognitionStateMachineConfiguration,
	cmpFn func(a, b TToken) int,
	tokenFormatter TokenFormatter[TToken],
	grammarPattern *pattern.RegulaAST[TToken],
	annotationMap map[pattern.AnnotationID]RecognitionAnnotation,
) *RecognitionStateMachine[TToken] {
	instructions := []pattern.RegulaNFAInstruction[TToken, bool]{
		{
			Pattern: grammarPattern,
			Outcome: true,
		},
	}

	tokenSet := make(map[TToken]struct{})
	g.buildTokenSet(tokenSet)

	possibleTokens := make([]TToken, 0)
	for tk := range tokenSet {
		possibleTokens = append(possibleTokens, tk)
	}
	sorted := extensions.SortedCopyShallow(possibleTokens, cmpFn)

	successorFn := func(current TToken) (next TToken, exists bool) {
		idx, found := slices.BinarySearchFunc(sorted, current, cmpFn)
		if !found {
			panic("unknown token provided for successor")
		}

		if idx == len(sorted)-1 {
			var zero TToken
			return zero, false
		}

		nextTk := sorted[idx+1]
		return nextTk, true
	}

	bundleKeyToID := make(map[uint64]pattern.AnnotationID)
	var nextBundleID = pattern.AnnotationID(len(annotationMap))

	ctx := pattern.RegulaCreateSharedCompilationContext(successorFn, cmpFn, tokenFormatter.patternFormatter)
	nfas, _ := pattern.RegulaCompileToNFAGlushkov(
		config.scratchAllocationFn,
		instructions,
		ctx,
		false, // nonTerminalOutcome: not accepting
	)
	nfa := nfas[0]
	dfa := autarch.NFAToDFA(
		nfa,
		config.minScratchMem, config.maxScratchMem,
		config.scratchAllocationFn,
		func(states []uint64, outcomes []pattern.AnnotatedOutcome[bool]) (outcome pattern.AnnotatedOutcome[bool], ok bool) {
			return resolveStateOutcomes(
				states,
				outcomes,
				bundleKeyToID,
				&nextBundleID,
				annotationMap,
			)
		},
	)

	minimized := autarch.DFAMinimize(
		dfa,
		config.mainAllocationFn, config.minScratchMem,
		config.maxScratchMem,
		func(out pattern.AnnotatedOutcome[bool]) struct {
			annotationKey uint64
			accepting     bool
		} {
			if out.Annotation == nil {
				return struct {
					annotationKey uint64
					accepting     bool
				}{
					annotationKey: hash.XXH3HasherHash64(xxh3Hasher, []byte{0x00}),
					accepting:     out.Value,
				}
			}

			annotation := annotationMap[*out.Annotation]
			annotationKey := annotation.Key()
			return struct {
				annotationKey uint64
				accepting     bool
			}{
				annotationKey: annotationKey,
				accepting:     out.Value,
			}
		},
	)

	return &RecognitionStateMachine[TToken]{
		nfa:                nfa,
		dfa:                dfa,
		minimizedDFA:       minimized,
		minimizedDFACursor: autarch.DFACursorGet(minimized),
		annotationMap:      annotationMap,
		tokenFormatter:     tokenFormatter,
	}
}

/*
ToRecognitionPattern produces a Regula Pattern for the grammar (provided the grammar is regular)
*/
func (g *Grammar[TToken]) ToRecognitionPattern(
	cmpFn func(a, b TToken) int,
) (*pattern.RegulaAST[TToken], map[pattern.AnnotationID]RecognitionAnnotation, error) {
	if !g.IsRegular() {
		return nil, nil, fmt.Errorf("cannot build Regula Pattern for non-regular grammar")
	}

	factory := pattern.RegulaASTFactoryCreate(cmpFn)

	// Registry for our annotations
	registry := make(map[pattern.AnnotationID]RecognitionAnnotation)
	var nextID uint32 = 0

	ast := g.produceRegulaAST(factory, &nextID, registry)
	return &ast, registry, nil
}

func (g *Grammar[TToken]) produceRegulaAST(
	ruleFactory *pattern.RegulaASTFactory[TToken],
	nextID *uint32,
	registry map[pattern.AnnotationID]RecognitionAnnotation,
) pattern.RegulaAST[TToken] {
	if g == nil {
		return ruleFactory.Literal()
	}

	var result pattern.RegulaAST[TToken]

	switch g.Kind {
	case GToken:
		result = ruleFactory.Literal(g.Token)

	case GEpsilon:
		result = ruleFactory.Literal()

	case GConcat:
		if len(g.Children) == 0 {
			result = ruleFactory.Literal()
		} else {
			exprs := make([]pattern.RegulaAST[TToken], len(g.Children))
			for i, child := range g.Children {
				exprs[i] = child.produceRegulaAST(ruleFactory, nextID, registry)
			}
			result = ruleFactory.Sequence(exprs...)
		}

	case GChoice:
		if len(g.Children) == 0 {
			result = ruleFactory.Class()
		} else {
			exprs := make([]pattern.RegulaAST[TToken], len(g.Children))
			for i, child := range g.Children {
				exprs[i] = child.produceRegulaAST(ruleFactory, nextID, registry)
			}
			result = ruleFactory.AnyOf(exprs...)
		}

	case GRepeat:
		body := g.Children[0].produceRegulaAST(ruleFactory, nextID, registry)
		maxVal := -1
		if g.Max != nil {
			maxVal = *g.Max
		}
		result = body.Repeat(g.Min, maxVal)

	case GOptional:
		result = g.Children[0].produceRegulaAST(ruleFactory, nextID, registry).Optional()

	default:
		panic(fmt.Errorf("unsupported or unknown grammar kind: %v", g.Kind))
	}

	if g.GrammarID != "" {
		id := pattern.AnnotationID(*nextID)
		*nextID++

		registry[id] = RecognitionAnnotation{
			GrammarIDs: []string{g.GrammarID},
		}

		result = *result.WithAnnotationID(id)
	}

	return result
}

// ----------------------------------------------------- PRIVATE HELPERS

func (g *Grammar[TToken]) buildTokenSet(set map[TToken]struct{}) {
	if g.Kind == GToken {
		set[g.Token] = struct{}{}
	}

	for _, child := range g.Children {
		child.buildTokenSet(set)
	}
}

func resolveStateOutcomes(
	_ []uint64,
	outcomes []pattern.AnnotatedOutcome[bool],
	bundleKeyToID map[uint64]pattern.AnnotationID,
	nextBundleID *pattern.AnnotationID,
	annotationMap map[pattern.AnnotationID]RecognitionAnnotation,
) (pattern.AnnotatedOutcome[bool], bool) {
	isAccepting := false
	ids := make([]pattern.AnnotationID, 0, len(outcomes))
	for _, o := range outcomes {
		if o.Value {
			isAccepting = true
		}
		if o.Annotation != nil {
			ids = append(ids, *o.Annotation)
		}
	}

	var annID *pattern.AnnotationID
	if len(ids) > 0 {
		merged := mergeAnnotationIDs(ids, bundleKeyToID, nextBundleID, annotationMap)
		annID = &merged
	}

	return pattern.AnnotatedOutcome[bool]{
		Value:      isAccepting,
		Annotation: annID,
	}, true
}

func formatStateOutcome(annotationMap map[pattern.AnnotationID]RecognitionAnnotation, outcome pattern.AnnotatedOutcome[bool]) string {
	if outcome.Annotation == nil {
		return fmt.Sprintf("outcome{accepting=%v,ids=<nil>", outcome.Value)
	}

	annotation := annotationMap[*outcome.Annotation]
	grammarIDs := annotation.GrammarIDs
	sortedIDs := extensions.SortedCopyShallow(grammarIDs, func(a, b string) int {
		return cmp.Compare(a, b)
	})
	grammarString := formatting.FormatStringSlice(sortedIDs, formatting.FormatSliceOptions[string]{
		Prefix:    "[",
		Suffix:    "]",
		Separator: ", ",
		Quote:     true,
	})

	return fmt.Sprintf("outcome{accepting=%v,ids=%s}", outcome.Value, grammarString)
}

func formatStateIndicator(outcome pattern.AnnotatedOutcome[bool], isDead bool) string {
	if isDead {
		return "D"
	}

	if outcome.Value {
		return "A"
	}

	return ""
}

func mergeAnnotationIDs(
	ids []pattern.AnnotationID,
	bundleKeyToID map[uint64]pattern.AnnotationID,
	nextBundleID *pattern.AnnotationID,
	annotationMap map[pattern.AnnotationID]RecognitionAnnotation,
) pattern.AnnotationID {
	slices.Sort(ids)
	ids = slices.Compact(ids)

	key := hashIDs(ids)
	if annID, ok := bundleKeyToID[key]; ok {
		return annID
	}

	merged := RecognitionAnnotation{
		GrammarIDs: make([]string, 0),
	}

	seen := make(map[uint64]struct{})
	for _, id := range ids {
		base := annotationMap[id]
		baseKey := base.Key()

		if _, ok := seen[baseKey]; ok {
			continue
		}
		seen[baseKey] = struct{}{}
		merged.GrammarIDs = append(merged.GrammarIDs, base.GrammarIDs...)
	}

	newID := *nextBundleID
	*nextBundleID++

	annotationMap[newID] = merged
	bundleKeyToID[key] = newID

	return newID
}

func hashIDs(ids []pattern.AnnotationID) uint64 {
	buf := make([]byte, 0, len(ids)*4)
	for _, id := range ids {
		v := uint32(id)
		buf = append(buf,
			byte(v),
			byte(v>>8),
			byte(v>>16),
			byte(v>>24),
		)
	}
	return hash.XXH3HasherHash64(xxh3Hasher, buf)
}
