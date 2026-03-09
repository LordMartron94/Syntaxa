package syntaxa

import (
	"autarch/pattern"
	"cmp"
	"fmt"
	"foundation/formatting"
	"foundation/hash"
	"sort"
	"strconv"
	"strings"
)

// ============================================================
// TYPES
// ============================================================

/*
NodeKey uniquely identifies a grammar node for analysis (nullable, first, follow).

Canonical key is the node's NodePath (path from tree root). Use nodeKeyFromPath to build keys
so that analysis maps are consistent regardless of GrammarLabel reuse across nodes.
*/
type NodeKey string

/*
TokenSet is a set of terminal tokens used as first/follow sets in grammar analysis.

Implemented as map[TToken]struct{} for O(1) membership and easy merging.
*/
type TokenSet[TToken comparable] map[TToken]struct{}

/*
GrammarPackage is the flattened, analyzed form of a grammar tree.

It contains the rule map, tokens used, nest specs, and computed nullable/first/follow analysis.
CoreCFG is the canonical CFG in pattern/Contexta IR; analysis is derived from it via
pattern.ComputeAnalysis. Produced by ProducePackage from a root Grammar node.

PathToGrammarLabel maps each node's path (NodeKey) to its GrammarLabel for debug display.
NodeByGrammarKey is 1:1 unique instance lookup. NodesByGrammarLabel is 1:N by semantic type.

EntryRuleParserRule is the executable rule for the entry production; set when producing
a package for the parser. Nil when the package is produced for analysis only (e.g. debug dumps).
*/
type GrammarPackage[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable] struct {
	Name                string
	Version             string
	EntryRule           GrammarLabel
	Grammars            map[GrammarLabel]*Grammar[TToken, TNodeKind]
	CoreCFG             *pattern.Grammar[TToken]
	TokensUsed          []TToken
	Nests               []NestSpec[TToken, TNodeKind]
	Analysis            *GrammarAnalysis[TToken]
	PathToGrammarLabel  map[NodeKey]GrammarLabel
	NodeByGrammarKey    map[GrammarKey]*Grammar[TToken, TNodeKind]
	NodesByGrammarLabel map[GrammarLabel][]*Grammar[TToken, TNodeKind]
	EntryRuleParserRule *ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
}

/*
NestSpec describes one bracketed (GNest) production: open token, close token, and owning rule.

Used by the parser for balanced delimiter matching and error recovery.
ID and OwnerRule are GrammarLabels (context-boundary nodes; unique per nest).
*/
type NestSpec[TToken, TNodeKind comparable] struct {
	ID        GrammarLabel
	Open      TToken
	Close     TToken
	OwnerRule GrammarLabel
	Node      *Grammar[TToken, TNodeKind]
}

/*
GrammarAnalysis holds the results of nullable, first, and follow computation for a grammar.

Maps are keyed by NodeKey. Used by the parser for prediction and error reporting.
*/
type GrammarAnalysis[TToken comparable] struct {
	Nullable map[NodeKey]bool
	First    map[NodeKey]TokenSet[TToken]
	Follow   map[NodeKey]TokenSet[TToken]
}

// ============================================================
// PUBLIC ENTRY
// ============================================================

/*
ProducePackage builds a GrammarPackage from the root grammar tree and any additional disconnected rules.

Walks the graphs to assign GrammarKey from NodePath (XXH3), collect rules, tokens, nest specs,
and populate NodeByGrammarKey and NodesByGrammarLabel. Then computes nullable, first, follow.
Panics if the root grammar is nil. Panics on duplicate GrammarLabel among context-boundary nodes.
Panics if any GReference node has a ReferenceTarget not present in the rules map; all unresolved
targets are reported at once.

entryRule is the executable parser rule for the entry production; pass it when the
package will be used to create a parser. Pass nil for analysis-only use (e.g. debug dumps).
*/
func ProducePackage[
	TObservation cmp.Ordered,
	TToken,
	TTokenRole,
	TNodeKind,
	TLexerState comparable,
](
	root *Grammar[TToken, TNodeKind],
	additionalRules []*Grammar[TToken, TNodeKind],
	name string,
	version string,
	entryRule *ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) GrammarPackage[TObservation, TToken, TTokenRole, TNodeKind, TLexerState] {
	if root == nil {
		panic("ProducePackage: root grammar is nil")
	}

	// 1. Assign deterministic paths to all nodes in the global graph
	finalizeGraphPaths(root, additionalRules)

	hasher := hash.XXH3HasherCreateWithSeed(0)
	rules := make(map[GrammarLabel]*Grammar[TToken, TNodeKind])
	tokenSet := make(TokenSet[TToken])
	nests := make([]NestSpec[TToken, TNodeKind], 0)
	nodeByGrammarKey := make(map[GrammarKey]*Grammar[TToken, TNodeKind])
	nodesByGrammarLabel := make(map[GrammarLabel][]*Grammar[TToken, TNodeKind])
	duplicateContextBoundaryLabels := make(map[GrammarLabel]struct{})
	visitedLabels := make(map[GrammarLabel]struct{}) // To prevent duplicate Rules/Nests
	visitedPaths := make(map[GrammarKey]struct{})    // To prevent infinite recursion in graph walks

	// 2. Collect everything from the entry point
	collectAll(hasher, root, rules, tokenSet, &nests, nodeByGrammarKey, nodesByGrammarLabel, duplicateContextBoundaryLabels, visitedLabels, visitedPaths)

	// 3. Collect everything from the disconnected sub-graphs
	for _, add := range additionalRules {
		collectAll(hasher, add, rules, tokenSet, &nests, nodeByGrammarKey, nodesByGrammarLabel, duplicateContextBoundaryLabels, visitedLabels, visitedPaths)
	}

	if len(duplicateContextBoundaryLabels) > 0 {
		ids := make([]string, 0, len(duplicateContextBoundaryLabels))
		for id := range duplicateContextBoundaryLabels {
			ids = append(ids, string(id))
		}
		sort.Strings(ids)
		list := formatting.FormatStringSlice(ids, formatting.FormatSliceOptions[string]{
			Separator: ", ",
			Quote:     true,
		})
		panic(fmt.Sprintf("syntaxa: duplicate grammar labels among context-boundary nodes (engine error): %s", list))
	}

	unresolvedRefs := collectUnresolvedReferences(root, additionalRules, rules)
	if len(unresolvedRefs) > 0 {
		sort.Strings(unresolvedRefs)
		list := formatting.FormatStringSlice(unresolvedRefs, formatting.FormatSliceOptions[string]{
			Separator: ", ",
			Quote:     true,
		})
		panic(fmt.Sprintf("syntaxa: unresolved reference target(s): %s", list))
	}

	setResolvedReferences(root, rules)
	for _, add := range additionalRules {
		setResolvedReferences(add, rules)
	}

	tokensUsed := make([]TToken, 0, len(tokenSet))
	for t := range tokenSet {
		tokensUsed = append(tokensUsed, t)
	}

	coreCFG := SyntaxaTreeToContextaGrammar(root, additionalRules, rules)
	patternAnalysis := pattern.ComputeAnalysis(coreCFG)
	analysis := grammarAnalysisFromPattern(patternAnalysis)

	pathToGrammarLabel := buildPathToGrammarLabel(root, additionalRules)

	return GrammarPackage[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]{
		Name:                name,
		Version:             version,
		EntryRule:           root.GrammarLabel,
		Grammars:            rules,
		CoreCFG:             coreCFG,
		TokensUsed:          tokensUsed,
		Nests:               nests,
		Analysis:            analysis,
		PathToGrammarLabel:  pathToGrammarLabel,
		NodeByGrammarKey:    nodeByGrammarKey,
		NodesByGrammarLabel: nodesByGrammarLabel,
		EntryRuleParserRule: entryRule,
	}
}

// ============================================================
// ANALYSIS (DELEGATED TO PATTERN)
// ============================================================

/*
grammarAnalysisFromPattern builds Syntaxa's GrammarAnalysis from pattern's analysis.
Rule names in pattern are NodePaths, so they map 1:1 to NodeKey.
*/
func grammarAnalysisFromPattern[TToken comparable](pa *pattern.GrammarAnalysis[TToken]) *GrammarAnalysis[TToken] {
	if pa == nil {
		return &GrammarAnalysis[TToken]{
			Nullable: make(map[NodeKey]bool),
			First:    make(map[NodeKey]TokenSet[TToken]),
			Follow:   make(map[NodeKey]TokenSet[TToken]),
		}
	}
	nullable := make(map[NodeKey]bool)
	first := make(map[NodeKey]TokenSet[TToken])
	follow := make(map[NodeKey]TokenSet[TToken])
	for k, v := range pa.Nullable {
		nullable[NodeKey(k)] = v
	}
	for k, s := range pa.First {
		dst := make(TokenSet[TToken])
		for t := range s {
			dst[t] = struct{}{}
		}
		first[NodeKey(k)] = dst
	}
	for k, s := range pa.Follow {
		dst := make(TokenSet[TToken])
		for t := range s {
			dst[t] = struct{}{}
		}
		follow[NodeKey(k)] = dst
	}
	return &GrammarAnalysis[TToken]{Nullable: nullable, First: first, Follow: follow}
}

// ============================================================
// GLOBAL PATH ASSIGNMENT
// ============================================================

/*
finalizeGraphPaths assigns unique topological addresses to all nodes in the namespace.
The root tree starts at "0". Disconnected subgraphs start at "ext0", "ext1", etc.
*/
func finalizeGraphPaths[TToken, TNodeKind comparable](root *Grammar[TToken, TNodeKind], additionals []*Grammar[TToken, TNodeKind]) {
	if root.NodePath == nil {
		assignPathRec(root, "0")
	}
	for i, add := range additionals {
		if add.NodePath == nil {
			assignPathRec(add, fmt.Sprintf("ext%d", i))
		}
	}
}

func assignPathRec[TToken, TNodeKind comparable](g *Grammar[TToken, TNodeKind], current string) {
	if g == nil || g.NodePath != nil {
		return
	}

	p := NodePath(current)
	g.NodePath = &p

	if len(g.Children) == 0 {
		return
	}

	prefix := current + "."
	var sb strings.Builder
	sb.Grow(len(prefix) + 20)

	for i, child := range g.Children {
		if child == nil {
			continue
		}
		sb.Reset()
		sb.WriteString(prefix)
		sb.WriteString(strconv.Itoa(i))
		assignPathRec(child, sb.String())
	}
}

/*
buildPathToGrammarLabel walks all graphs and builds a map from each node's path to its GrammarLabel.
*/
func buildPathToGrammarLabel[TToken, TNodeKind comparable](root *Grammar[TToken, TNodeKind], additionals []*Grammar[TToken, TNodeKind]) map[NodeKey]GrammarLabel {
	out := make(map[NodeKey]GrammarLabel)
	buildPathToGrammarLabelRec(root, out)
	for _, add := range additionals {
		buildPathToGrammarLabelRec(add, out)
	}
	return out
}

func buildPathToGrammarLabelRec[TToken, TNodeKind comparable](g *Grammar[TToken, TNodeKind], out map[NodeKey]GrammarLabel) {
	if g == nil || g.NodePath == nil {
		return
	}
	key := NodeKeyFromPath(*g.NodePath)
	out[key] = g.GrammarLabel
	for _, c := range g.Children {
		buildPathToGrammarLabelRec(c, out)
	}
}

/*
NestSpecsOpenTokenCounts returns the count of each open token across the given nest specs.
*/
func NestSpecsOpenTokenCounts[TToken comparable, TNodeKind comparable](nests []NestSpec[TToken, TNodeKind]) map[TToken]int {
	counts := make(map[TToken]int)
	for _, nest := range nests {
		counts[nest.Open]++
	}
	return counts
}

// ============================================================
// UNIFIED TREE WALK
// ============================================================

/*
collectAll walks the grammar tree: assigns GrammarKey from NodePath (XXH3), populates rules,
tokenSet, nests, NodeByGrammarKey, and NodesByGrammarLabel. When two distinct context-boundary
nodes share the same GrammarLabel, that label is recorded in duplicateContextBoundaryLabels (caller panics).
*/
/*
collectAll walks the grammar tree. It uses a visited map for GrammarLabels to ensure
that context-boundary nodes (rules) and Nests are only processed once, preventing
duplicates in the package even if a rule is referenced multiple times.
*/
func collectAll[TToken, TNodeKind comparable](
	hasher *hash.XXH3Hasher,
	g *Grammar[TToken, TNodeKind],
	rules map[GrammarLabel]*Grammar[TToken, TNodeKind],
	tokenSet TokenSet[TToken],
	nests *[]NestSpec[TToken, TNodeKind],
	nodeByGrammarKey map[GrammarKey]*Grammar[TToken, TNodeKind],
	nodesByGrammarLabel map[GrammarLabel][]*Grammar[TToken, TNodeKind],
	duplicateContextBoundaryLabels map[GrammarLabel]struct{},
	visitedLabels map[GrammarLabel]struct{},
	visitedPaths map[GrammarKey]struct{},
) {
	if g == nil {
		return
	}

	// 1. Structural Identity (Mandatory for all nodes)
	var k GrammarKey
	if g.NodePath != nil {
		pathBytes := []byte(string(*g.NodePath))
		k = GrammarKey(hash.XXH3HasherHash64(hasher, pathBytes))
		g.GrammarKey = k

		// If we've already walked THIS specific path, stop to avoid infinite loops
		if _, seen := visitedPaths[k]; seen {
			return
		}
		visitedPaths[k] = struct{}{}
		nodeByGrammarKey[k] = g
	}

	if g.GrammarLabel != "" {
		nodesByGrammarLabel[g.GrammarLabel] = append(nodesByGrammarLabel[g.GrammarLabel], g)
	}

	// 2. Semantic Registration (Rules & Nests)
	// We only add to the 'Rules' map or 'Nests' slice if we haven't seen this LABEL before.
	_, labelSeen := visitedLabels[g.GrammarLabel]

	if g.IsContextBoundary && !labelSeen {
		if existing, seen := rules[g.GrammarLabel]; seen && existing != g {
			duplicateContextBoundaryLabels[g.GrammarLabel] = struct{}{}
		}
		rules[g.GrammarLabel] = g
		// Note: We don't mark visitedLabels here yet because GNest logic below needs to see it too
	}

	switch g.Kind {
	case GToken:
		tokenSet[g.Token] = struct{}{}
	case GNest:
		tokenSet[*g.OpenToken] = struct{}{}
		tokenSet[*g.CloseToken] = struct{}{}

		if !labelSeen {
			*nests = append(*nests, NestSpec[TToken, TNodeKind]{
				ID:        g.GrammarLabel,
				Open:      *g.OpenToken,
				Close:     *g.CloseToken,
				OwnerRule: g.GrammarLabel,
				Node:      g,
			})
			visitedLabels[g.GrammarLabel] = struct{}{}
		}
	}

	// Mark the label as visited after processing context boundaries
	if g.IsContextBoundary && g.GrammarLabel != "" {
		visitedLabels[g.GrammarLabel] = struct{}{}
	}

	for _, child := range g.Children {
		collectAll(hasher, child, rules, tokenSet, nests, nodeByGrammarKey,
			nodesByGrammarLabel, duplicateContextBoundaryLabels,
			visitedLabels, visitedPaths)
	}
}

/*
collectUnresolvedReferences walks the root and additional graphs to find any ReferenceTarget
labels that are not present in the global rules map.
*/
func collectUnresolvedReferences[TToken, TNodeKind comparable](
	root *Grammar[TToken, TNodeKind],
	additionals []*Grammar[TToken, TNodeKind],
	rules map[GrammarLabel]*Grammar[TToken, TNodeKind],
) []string {
	unresolved := make(map[string]struct{})

	collectUnresolvedReferencesRec(root, rules, unresolved)
	for _, add := range additionals {
		collectUnresolvedReferencesRec(add, rules, unresolved)
	}

	out := make([]string, 0, len(unresolved))
	for s := range unresolved {
		out = append(out, s)
	}
	return out
}

func collectUnresolvedReferencesRec[TToken, TNodeKind comparable](
	g *Grammar[TToken, TNodeKind],
	rules map[GrammarLabel]*Grammar[TToken, TNodeKind],
	unresolved map[string]struct{},
) {
	if g == nil {
		return
	}
	if g.Kind == GReference {
		if _, ok := rules[g.ReferenceTarget]; !ok && g.ReferenceTarget != "" {
			unresolved[string(g.ReferenceTarget)] = struct{}{}
		}
	}
	for _, child := range g.Children {
		collectUnresolvedReferencesRec(child, rules, unresolved)
	}
}

/*
setResolvedReferences walks the grammar tree via structural Children only and sets
ResolvedReference on each GReference node from the rules map. Call after collectAll
and after validating no unresolved references, so the grammar walker can follow refs.
*/
func setResolvedReferences[TToken, TNodeKind comparable](
	g *Grammar[TToken, TNodeKind],
	rules map[GrammarLabel]*Grammar[TToken, TNodeKind],
) {
	if g == nil {
		return
	}
	if g.Kind == GReference && g.ReferenceTarget != "" {
		if target, ok := rules[g.ReferenceTarget]; ok {
			g.ResolvedReference = target
		}
	}
	for _, child := range g.Children {
		setResolvedReferences(child, rules)
	}
}

// ============================================================
// HELPERS
// ============================================================

func mergeInto[TToken comparable](dst TokenSet[TToken], src TokenSet[TToken]) bool {
	changed := false
	for t := range src {
		if _, exists := dst[t]; !exists {
			dst[t] = struct{}{}
			changed = true
		}
	}
	return changed
}

/*
NodeKeyFromPath builds a NodeKey from a node path.

NodePath is unique per node in the tree. Using path as the sole key ensures analysis maps
(nullable, first, follow) are consistent when multiple nodes share the same GrammarLabel.
*/
func NodeKeyFromPath(path NodePath) NodeKey {
	return NodeKey(path)
}
