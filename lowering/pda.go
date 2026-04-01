package lowering

import (
		"lexarch"
	"fmt"
	"strings"

	"autarch"
	"autarch/pattern"
	"memarch"
	"syntaxa"
)

/*
PDAEngine holds the compiled pushdown automaton and the mapping from stack symbols back to grammar nodes.

After CompileEngine, exactly one of DPDA or NPDA is non-nil. IsDPDA is true when the grammar
was LL(1) and a deterministic engine was built; otherwise the NPDA fallback is used.
StackToNode maps autarch.StackSymbolID (from PDA transitions) to syntaxa.NodeKey so that
reductions during parsing can be attributed to the correct grammar node for IR construction.
*/
type PDAEngine[TOutcome any] struct {
	IsDPDA         bool
	DPDA           *autarch.DPDA[lexarch.TokenKind, pattern.AnnotatedOutcome[TOutcome]]
	NPDA           *autarch.NPDA[lexarch.TokenKind, pattern.AnnotatedOutcome[TOutcome]]
	Transitions    []autarch.PDATransition
	StackToNode    map[autarch.StackSymbolID]syntaxa.NodeKey
	InputIDToToken map[uint64]lexarch.TokenKind
	DebugMap       map[autarch.StackSymbolID]string
	DPDAError      error
}

/*
NPDAConfig holds execution limits for the non-deterministic pushdown automaton fallback.

Used when CompileEngine cannot build an LL(1) DPDA. Limits prevent unbounded memory and
infinite epsilon loops. Exceeding any limit causes the NPDA run to fail (e.g. branch limit).
*/
type NPDAConfig struct {
	MaxStackNodes   uint64
	MaxStackDepth   uint64
	MaxBranches     uint64
	MaxEpsilonSteps uint64
}

var (
	/*
		NPDAConfigSmall is for simple configurations, DSLs, or prototyping.

		Fails fast on ambiguity. Use when you expect minimal branching and small inputs.
	*/
	NPDAConfigSmall = NPDAConfig{
		MaxStackNodes:   2_000,
		MaxStackDepth:   100,
		MaxBranches:     50,
		MaxEpsilonSteps: 500,
	}

	/*
		NPDAConfigMedium is for standard languages with localized ambiguity.

		Typical for expression parsing without strict LL(1) factoring or moderate rule sets.
	*/
	NPDAConfigMedium = NPDAConfig{
		MaxStackNodes:   10_000,
		MaxStackDepth:   500,
		MaxBranches:     250,
		MaxEpsilonSteps: 2_000,
	}

	/*
		NPDAConfigLarge is for highly ambiguous or large grammars.

		Hitting these limits usually indicates the parser is exploring many dead paths;
		consider simplifying the grammar or using a different strategy.
	*/
	NPDAConfigLarge = NPDAConfig{
		MaxStackNodes:   50_000,
		MaxStackDepth:   2_000,
		MaxBranches:     1_000,
		MaxEpsilonSteps: 10_000,
	}
)

/*
CompileEngine compiles a Syntaxa grammar package into a pushdown automaton (PDA) for parsing.

It uses ToPatternGrammar internally; the package does not need CoreCFG set. It first tries
to build an LL(1) deterministic PDA (DPDA) via pattern.CompileDPDA. If the grammar has
predictive set overlaps (not LL(1)), it falls back to a non-deterministic PDA (NPDA) with
the given config limits. The caller receives a PDAEngine with either DPDA or NPDA set;
StackToNode maps stack symbol IDs to NodeKeys for mapping reductions to grammar nodes.

Prerequisites: pkg must be a valid GrammarPackage produced by ProducePackage with
TokensUsed set. allocFn must be a valid memarch allocation function. config is used only
when falling back to NPDA (MaxEpsilonSteps is used for DPDA as well).
*/
func CompileEngine[TNodeKind comparable, TOutcome any](
	pkg *syntaxa.GrammarPackage[TNodeKind],
	allocFn memarch.AllocationFn,
	config NPDAConfig,
) (*PDAEngine[TOutcome], error) {
	if pkg == nil || pkg.Root == nil {
		return nil, fmt.Errorf("CompileEngine: package or Root is nil")
	}
	cfg, ruleNameToNodeKey, _ := ToPatternGrammar(pkg.Root, pkg.AdditionalRules, pkg.Grammars)
	if cfg == nil {
		return nil, fmt.Errorf("CompileEngine: ToPatternGrammar returned nil")
	}

	alphabet, deterministic, nondeterministic, reverseMap := buildAlphabetAndIndexer(pkg.TokensUsed)
	var acceptOutcome TOutcome

	dpda, transitions, debugMap, dpdaErr := pattern.CompileDPDA(
		cfg, allocFn, alphabet, deterministic, acceptOutcome, config.MaxEpsilonSteps,
	)

	if dpdaErr == nil {
		return buildEngine(true, dpda, nil, transitions, ruleNameToNodeKey, pkg.PathToGrammarLabel, debugMap, reverseMap, nil), nil
	}

	translatedErr := translateGrammarError(dpdaErr, ruleNameToNodeKey, pkg.PathToGrammarLabel, debugMap, reverseMap)

	engine, npdaErr := compileFallbackNPDA(
		cfg,
		allocFn,
		alphabet,
		deterministic,
		nondeterministic,
		acceptOutcome,
		config,
		ruleNameToNodeKey,
		pkg.PathToGrammarLabel,
		debugMap,
		reverseMap,
	)
	if engine != nil {
		engine.DPDAError = translatedErr
	}
	return engine, npdaErr
}

func buildAlphabetAndIndexer(
	tokens []lexarch.TokenKind,
) (
	[]autarch.SymbolDefinition[lexarch.TokenKind],
	autarch.DeterministicSymbolResolver[lexarch.TokenKind],
	autarch.NondeterministicSymbolResolver[lexarch.TokenKind],
	map[uint64]lexarch.TokenKind,
) {
	alphabet := make([]autarch.SymbolDefinition[lexarch.TokenKind], 0, len(tokens))
	tokenToID := make(map[lexarch.TokenKind]uint64, len(tokens))
	idToToken := make(map[uint64]lexarch.TokenKind, len(tokens))
	var nextID uint64 = 1
	for _, t := range tokens {
		tokenValue := t
		tokenToID[tokenValue] = nextID
		idToToken[nextID] = tokenValue
		alphabet = append(alphabet, autarch.SymbolDefinition[lexarch.TokenKind]{
			ID:          nextID,
			Name:        fmt.Sprintf("%v", tokenValue),
			Match:       func(observation lexarch.TokenKind) bool { return observation == tokenValue },
			Observation: &tokenValue,
		})
		nextID++
	}
	deterministic := func(obs lexarch.TokenKind) (uint64, bool) {
		if id, exists := tokenToID[obs]; exists {
			return id, true
		}
		return 0, false
	}
	nondeterministic := func(obs lexarch.TokenKind) []uint64 {
		id, ok := deterministic(obs)
		if !ok {
			return nil
		}
		return []uint64{id}
	}
	return alphabet, deterministic, nondeterministic, idToToken
}

func compileFallbackNPDA[TOutcome any](
	cfg *pattern.Grammar[lexarch.TokenKind, struct{}],
	allocFn memarch.AllocationFn,
	alphabet []autarch.SymbolDefinition[lexarch.TokenKind],
	deterministic autarch.DeterministicSymbolResolver[lexarch.TokenKind],
	nondeterministic autarch.NondeterministicSymbolResolver[lexarch.TokenKind],
	acceptOutcome TOutcome,
	config NPDAConfig,
	ruleNameToNodeKey map[string]syntaxa.NodeKey,
	pathToGrammarLabel map[syntaxa.NodeKey]syntaxa.GrammarLabel,
	debugMap map[autarch.StackSymbolID]string,
	reverseMap map[uint64]lexarch.TokenKind,
) (*PDAEngine[TOutcome], error) {
	npda, transitions, debugMapOut, err := pattern.CompileNPDA(
		cfg, allocFn, alphabet, deterministic, nondeterministic, acceptOutcome,
		config.MaxStackNodes, config.MaxStackDepth, config.MaxBranches, config.MaxEpsilonSteps,
	)
	if err != nil {
		return nil, fmt.Errorf("NPDA fallback compilation failed: %w", err)
	}
	return buildEngine(false, nil, npda, transitions, ruleNameToNodeKey, pathToGrammarLabel, debugMapOut, reverseMap, nil), nil
}

func buildEngine[TOutcome any](
	isDPDA bool,
	dpda *autarch.DPDA[lexarch.TokenKind, pattern.AnnotatedOutcome[TOutcome]],
	npda *autarch.NPDA[lexarch.TokenKind, pattern.AnnotatedOutcome[TOutcome]],
	transitions []autarch.PDATransition,
	ruleNameToNodeKey map[string]syntaxa.NodeKey,
	pathToGrammarLabel map[syntaxa.NodeKey]syntaxa.GrammarLabel,
	debugMap map[autarch.StackSymbolID]string,
	reverseMap map[uint64]lexarch.TokenKind,
	dpdaError error,
) *PDAEngine[TOutcome] {
	nodeMap := mapStackToNodes(ruleNameToNodeKey, debugMap)
	return &PDAEngine[TOutcome]{
		IsDPDA:         isDPDA,
		DPDA:           dpda,
		NPDA:           npda,
		Transitions:    transitions,
		StackToNode:    nodeMap,
		InputIDToToken: reverseMap,
		DPDAError:      dpdaError,
		DebugMap:       debugMap,
	}
}

func translateGrammarError(
	err error,
	ruleNameToNodeKey map[string]syntaxa.NodeKey,
	pathToGrammarLabel map[syntaxa.NodeKey]syntaxa.GrammarLabel,
	debugMap map[autarch.StackSymbolID]string,
	reverseMap map[uint64]lexarch.TokenKind,
) error {
	if err == nil {
		return nil
	}
	if pathToGrammarLabel == nil {
		pathToGrammarLabel = make(map[syntaxa.NodeKey]syntaxa.GrammarLabel)
	}
	if ctxErr, ok := err.(*pattern.ContextaGrammarError); ok && ctxErr.Automaton != nil {
		autoErr := ctxErr.Automaton
		if autoErr.Kind == autarch.AutomatonErrorNondeterminismDPDA || autoErr.Kind == autarch.AutomatonErrorEpsilonConflictDPDA {
			var builder strings.Builder
			if autoErr.Kind == autarch.AutomatonErrorNondeterminismDPDA {
				builder.WriteString("Grammar is not LL(1) compliant (FIRST/FIRST or FIRST/FOLLOW conflict):\n")
			} else {
				builder.WriteString("Grammar is not LL(1) compliant (Epsilon vs Consuming conflict):\n")
			}
			for _, conflict := range autoErr.DPDAConflicts {
				tokenStr := fmt.Sprintf("InputID(%d)", conflict.InputID)
				if tok, ok := reverseMap[conflict.InputID]; ok {
					tokenStr = fmt.Sprintf("'%v'", tok)
				} else if conflict.InputID == autarch.EpsilonSymbolID {
					tokenStr = "EPSILON"
				}
				stackStr := fmt.Sprintf("StackID(%d)", conflict.StackTop)
				if ruleName, ok := debugMap[conflict.StackTop]; ok {
					if nodeKey, ok2 := ruleNameToNodeKey[ruleName]; ok2 {
						label := pathToGrammarLabel[nodeKey]
						stackStr = fmt.Sprintf("[%s] (NodePath: %s)", label, nodeKey)
					} else {
						stackStr = ruleName
					}
				}
				builder.WriteString(fmt.Sprintf("- Ambiguous Rule: %s (Conflicting Token: %s)\n", stackStr, tokenStr))
			}
			return fmt.Errorf("%s\n\nFix: Ensure rules starting with the same token are left-factored, and optional loops have clear terminators", builder.String())
		}
	}
	errStr := err.Error()
	for ruleName, nodeKey := range ruleNameToNodeKey {
		if strings.Contains(errStr, ruleName) {
			label := pathToGrammarLabel[nodeKey]
			replacement := fmt.Sprintf("[%s] (NodePath: %s)", label, nodeKey)
			errStr = strings.ReplaceAll(errStr, ruleName, replacement)
		}
	}
	if strings.Contains(errStr, "empty predictive set") {
		errStr += "\n\nFix: The grammar likely lacks an explicit End-Of-File (EOF) expectation at the root, so trailing optional rules cannot resolve their FOLLOW sets."
	}
	return fmt.Errorf("%s", errStr)
}

func mapStackToNodes(
	ruleNameToNodeKey map[string]syntaxa.NodeKey,
	debugMap map[autarch.StackSymbolID]string,
) map[autarch.StackSymbolID]syntaxa.NodeKey {
	result := make(map[autarch.StackSymbolID]syntaxa.NodeKey, len(debugMap))
	for stackID, ruleName := range debugMap {
		if key, exists := ruleNameToNodeKey[ruleName]; exists {
			result[stackID] = key
		}
	}
	return result
}
