package syntaxa

import (
	"autarch"
	"autarch/pattern"
	"cmp"
	"fmt"
	"memarch"
	"strings"
)

/*
PDAEngine holds the compiled pushdown automaton and the mapping from stack symbols back to grammar nodes.

After CompileEngine, exactly one of DPDA or NPDA is non-nil. IsDPDA is true when the grammar
was LL(1) and a deterministic engine was built; otherwise the NPDA fallback is used.
StackToNode maps autarch.StackSymbolID (from PDA transitions) to syntaxa.NodeKey so that
reductions during parsing can be attributed to the correct grammar node for IR construction.
*/
type PDAEngine[TToken comparable, TOutcome any] struct {
	IsDPDA         bool
	DPDA           *autarch.DPDA[TToken, pattern.AnnotatedOutcome[TOutcome]]
	NPDA           *autarch.NPDA[TToken, pattern.AnnotatedOutcome[TOutcome]]
	Transitions    []autarch.PDATransition
	StackToNode    map[autarch.StackSymbolID]NodeKey
	InputIDToToken map[uint64]TToken
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

It first tries to build an LL(1) deterministic PDA (DPDA) via pattern.CompileDPDA. If the
grammar has predictive set overlaps (not LL(1)), it falls back to a non-deterministic PDA
(NPDA) with the given config limits. The caller receives a PDAEngine with either DPDA or
NPDA set; StackToNode maps stack symbol IDs to NodeKeys for mapping reductions to grammar nodes.

Use cases:
- One-shot compilation of a GrammarPackage for a lexer/parser pipeline
- Syntax highlighting or incremental parsing where a single engine is reused
- Testing grammar LL(1)-ness (nil error and IsDPDA true means LL(1))

Time complexity: O(grammar size) for DPDA attempt (FIRST/FOLLOW analysis); O(grammar size) for NPDA fallback.
Space complexity: O(grammar size) for alphabet and transition table; NPDA adds config-dependent stack limits.

Prerequisites:
- pkg must be a valid GrammarPackage produced by ProducePackage with CoreCFG and TokensUsed set
- allocFn must be a valid memarch allocation function for autarch PDA construction
- config is used only when falling back to NPDA (MaxEpsilonSteps is used for DPDA as well)

Edge cases:
- Returns (engine, nil) when DPDA compilation succeeds; engine.IsDPDA is true
- Returns (engine, nil) when DPDA fails but NPDA compilation succeeds; engine.IsDPDA is false
- Returns (nil, err) only when NPDA compilation fails (e.g. invalid grammar or alloc failure)
*/
func CompileEngine[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable, TOutcome any](
	pkg *GrammarPackage[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	allocFn memarch.AllocationFn,
	config NPDAConfig,
) (*PDAEngine[TToken, TOutcome], error) {

	alphabet, indexer, reverseMap := buildAlphabetAndIndexer(pkg.TokensUsed)
	var acceptOutcome TOutcome

	dpda, transitions, debugMap, dpdaErr := pattern.CompileDPDA(
		pkg.CoreCFG, allocFn, alphabet, indexer, acceptOutcome, config.MaxEpsilonSteps,
	)

	if dpdaErr == nil {
		return buildEngine(true, dpda, nil, transitions, pkg, debugMap, reverseMap, nil), nil
	}

	translatedErr := translateGrammarError(dpdaErr, pkg, debugMap, reverseMap)

	engine, npdaErr := compileFallbackNPDA(pkg, allocFn, alphabet, indexer, acceptOutcome, config, reverseMap)
	if engine != nil {
		engine.DPDAError = translatedErr
	}
	return engine, npdaErr
}

func buildAlphabetAndIndexer[TToken comparable](
	tokens []TToken,
) ([]autarch.SymbolDefinition[TToken], autarch.SymbolIndexer[TToken], map[uint64]TToken) {

	alphabet := make([]autarch.SymbolDefinition[TToken], 0, len(tokens))
	tokenToID := make(map[TToken]uint64, len(tokens))
	idToToken := make(map[uint64]TToken, len(tokens))

	var nextID uint64 = 1

	for _, t := range tokens {
		tokenValue := t

		tokenToID[tokenValue] = nextID
		idToToken[nextID] = tokenValue

		alphabet = append(alphabet, autarch.SymbolDefinition[TToken]{
			ID:          nextID,
			Name:        fmt.Sprintf("%v", tokenValue),
			Match:       func(observation TToken) bool { return observation == tokenValue },
			Observation: &tokenValue,
		})
		nextID++
	}

	indexer := func(obs TToken) []autarch.Symbol[TToken] {
		if id, exists := tokenToID[obs]; exists {
			return []autarch.Symbol[TToken]{
				{
					SymbolID:          id,
					SymbolDescription: fmt.Sprintf("%v", obs),
				},
			}
		}
		return nil
	}

	return alphabet, indexer, idToToken
}

func compileFallbackNPDA[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable, TOutcome any](
	pkg *GrammarPackage[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	allocFn memarch.AllocationFn,
	alphabet []autarch.SymbolDefinition[TToken],
	indexer autarch.SymbolIndexer[TToken],
	acceptOutcome TOutcome,
	config NPDAConfig,
	reverseMap map[uint64]TToken,
) (*PDAEngine[TToken, TOutcome], error) {

	npda, transitions, debugMap, err := pattern.CompileNPDA(
		pkg.CoreCFG, allocFn, alphabet, indexer, acceptOutcome,
		config.MaxStackNodes, config.MaxStackDepth, config.MaxBranches, config.MaxEpsilonSteps,
	)

	if err != nil {
		return nil, fmt.Errorf("NPDA fallback compilation failed: %w", err)
	}

	return buildEngine(false, nil, npda, transitions, pkg, debugMap, reverseMap, nil), nil
}

func buildEngine[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable, TOutcome any](
	isDPDA bool,
	dpda *autarch.DPDA[TToken, pattern.AnnotatedOutcome[TOutcome]],
	npda *autarch.NPDA[TToken, pattern.AnnotatedOutcome[TOutcome]],
	transitions []autarch.PDATransition,
	pkg *GrammarPackage[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	debugMap map[autarch.StackSymbolID]string,
	reverseMap map[uint64]TToken,
	dpdaError error,
) *PDAEngine[TToken, TOutcome] {

	nodeMap := mapStackToNodes(pkg, debugMap)

	return &PDAEngine[TToken, TOutcome]{
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

func translateGrammarError[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable](
	err error,
	pkg *GrammarPackage[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	debugMap map[autarch.StackSymbolID]string,
	reverseMap map[uint64]TToken,
) error {
	if err == nil {
		return nil
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

			// Iterate through the strongly-typed conflicts
			for _, conflict := range autoErr.DPDAConflicts {

				// 1. Resolve the Token
				tokenStr := fmt.Sprintf("InputID(%d)", conflict.InputID)
				if tok, ok := reverseMap[conflict.InputID]; ok {
					tokenStr = fmt.Sprintf("'%v'", tok)
				} else if conflict.InputID == autarch.EpsilonSymbolID {
					tokenStr = "EPSILON"
				}

				// 2. Resolve the Grammar Rule
				stackStr := fmt.Sprintf("StackID(%d)", conflict.StackTop)
				if ruleName, ok := debugMap[conflict.StackTop]; ok {
					if nodeKey, ok2 := pkg.RuleNameToNodeKey[ruleName]; ok2 {
						label := pkg.PathToGrammarLabel[nodeKey]
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
	for ruleName, nodeKey := range pkg.RuleNameToNodeKey {
		if strings.Contains(errStr, ruleName) {
			label := pkg.PathToGrammarLabel[nodeKey]
			replacement := fmt.Sprintf("[%s] (NodePath: %s)", label, nodeKey)
			errStr = strings.ReplaceAll(errStr, ruleName, replacement)
		}
	}

	if strings.Contains(errStr, "empty predictive set") {
		errStr += "\n\nFix: The grammar likely lacks an explicit End-Of-File (EOF) expectation at the root, so trailing optional rules cannot resolve their FOLLOW sets."
	}

	return fmt.Errorf("%s", errStr)
}

func mapStackToNodes[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable](
	pkg *GrammarPackage[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
	debugMap map[autarch.StackSymbolID]string,
) map[autarch.StackSymbolID]NodeKey {

	result := make(map[autarch.StackSymbolID]NodeKey, len(debugMap))
	for stackID, ruleName := range debugMap {
		if key, exists := pkg.RuleNameToNodeKey[ruleName]; exists {
			result[stackID] = key
		}
	}
	return result
}
