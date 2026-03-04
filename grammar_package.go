package syntaxa

import (
	"cmp"
	"fmt"
	"foundation/formatting"
	"foundation/hash"
	"sort"
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
Produced by ProducePackage from a root Grammar node.

PathToGrammarLabel maps each node's path (NodeKey) to its GrammarLabel for debug display.
NodeByGrammarKey is 1:1 unique instance lookup. NodesByGrammarLabel is 1:N by semantic type.

EntryRuleParserRule is the executable rule for the entry production; set when producing
a package for the parser. Nil when the package is produced for analysis only (e.g. debug dumps).
*/
type GrammarPackage[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable] struct {
	Name                string
	Version             string
	EntryRule           GrammarLabel
	Rules               map[GrammarLabel]*Grammar[TToken]
	TokensUsed          []TToken
	Nests               []NestSpec[TToken]
	Analysis            *GrammarAnalysis[TToken]
	PathToGrammarLabel  map[NodeKey]GrammarLabel
	NodeByGrammarKey    map[GrammarKey]*Grammar[TToken]
	NodesByGrammarLabel map[GrammarLabel][]*Grammar[TToken]
	EntryRuleParserRule *ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind]
}

/*
NestSpec describes one bracketed (GNest) production: open token, close token, and owning rule.

Used by the parser for balanced delimiter matching and error recovery.
ID and OwnerRule are GrammarLabels (context-boundary nodes; unique per nest).
*/
type NestSpec[TToken comparable] struct {
	ID        GrammarLabel
	Open      TToken
	Close     TToken
	OwnerRule GrammarLabel
	Node      *Grammar[TToken]
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
ProducePackage builds a GrammarPackage from the root grammar tree.

Walks the tree to assign GrammarKey from NodePath (XXH3), collect rules, tokens, nest specs,
and populate NodeByGrammarKey and NodesByGrammarLabel. Then computes nullable, first, follow.
Panics if the root grammar is nil. Panics only on duplicate GrammarLabel among context-boundary nodes.

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
	root *Grammar[TToken],
	name string,
	version string,
	entryRule *ParserRule[TObservation, TToken, TTokenRole, TLexerState, TNodeKind],
) GrammarPackage[TObservation, TToken, TTokenRole, TNodeKind, TLexerState] {
	if root == nil {
		panic("ProducePackage: root grammar is nil")
	}

	if root.NodePath == nil {
		root.FinalizeNodePaths()
	}

	hasher := hash.XXH3HasherCreateWithSeed(0)
	rules := make(map[GrammarLabel]*Grammar[TToken])
	tokenSet := make(TokenSet[TToken])
	nests := make([]NestSpec[TToken], 0)
	nodeByGrammarKey := make(map[GrammarKey]*Grammar[TToken])
	nodesByGrammarLabel := make(map[GrammarLabel][]*Grammar[TToken])
	duplicateContextBoundaryLabels := make(map[GrammarLabel]struct{})

	collectAll(hasher, root, rules, tokenSet, &nests, nodeByGrammarKey, nodesByGrammarLabel, duplicateContextBoundaryLabels)

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

	tokensUsed := make([]TToken, 0, len(tokenSet))
	for t := range tokenSet {
		tokensUsed = append(tokensUsed, t)
	}

	analysis := ComputeAnalysisSingleTree(root)
	pathToGrammarLabel := buildPathToGrammarLabel(root)

	return GrammarPackage[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]{
		Name:                name,
		Version:             version,
		EntryRule:           root.GrammarLabel,
		Rules:               rules,
		TokensUsed:          tokensUsed,
		Nests:               nests,
		Analysis:            analysis,
		PathToGrammarLabel:  pathToGrammarLabel,
		NodeByGrammarKey:    nodeByGrammarKey,
		NodesByGrammarLabel: nodesByGrammarLabel,
		EntryRuleParserRule: entryRule,
	}
}

/*
buildPathToGrammarLabel walks the grammar tree and builds a map from each node's path (NodeKey) to its GrammarLabel.
Used by the debugger to display analysis entries as "GrammarLabel (path)" instead of path alone.
*/
func buildPathToGrammarLabel[TToken comparable](g *Grammar[TToken]) map[NodeKey]GrammarLabel {
	out := make(map[NodeKey]GrammarLabel)
	buildPathToGrammarLabelRec(g, out)
	return out
}

func buildPathToGrammarLabelRec[TToken comparable](g *Grammar[TToken], out map[NodeKey]GrammarLabel) {
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
func NestSpecsOpenTokenCounts[TToken comparable](nests []NestSpec[TToken]) map[TToken]int {
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
func collectAll[TToken comparable](
	hasher *hash.XXH3Hasher,
	g *Grammar[TToken],
	rules map[GrammarLabel]*Grammar[TToken],
	tokenSet TokenSet[TToken],
	nests *[]NestSpec[TToken],
	nodeByGrammarKey map[GrammarKey]*Grammar[TToken],
	nodesByGrammarLabel map[GrammarLabel][]*Grammar[TToken],
	duplicateContextBoundaryLabels map[GrammarLabel]struct{},
) {
	if g == nil {
		return
	}

	if g.NodePath != nil {
		pathBytes := []byte(string(*g.NodePath))
		k := GrammarKey(hash.XXH3HasherHash64(hasher, pathBytes))
		g.GrammarKey = k
		nodeByGrammarKey[k] = g
	}
	if g.GrammarLabel != "" {
		nodesByGrammarLabel[g.GrammarLabel] = append(nodesByGrammarLabel[g.GrammarLabel], g)
	}
	if g.IsContextBoundary {
		if existing, seen := rules[g.GrammarLabel]; seen && existing != g {
			duplicateContextBoundaryLabels[g.GrammarLabel] = struct{}{}
		}
		rules[g.GrammarLabel] = g
	}

	switch g.Kind {
	case GToken:
		tokenSet[g.Token] = struct{}{}
	case GNest:
		tokenSet[*g.OpenToken] = struct{}{}
		tokenSet[*g.CloseToken] = struct{}{}
		*nests = append(*nests, NestSpec[TToken]{
			ID:        g.GrammarLabel,
			Open:      *g.OpenToken,
			Close:     *g.CloseToken,
			OwnerRule: g.GrammarLabel,
			Node:      g,
		})
	}

	for _, child := range g.Children {
		collectAll(hasher, child, rules, tokenSet, nests, nodeByGrammarKey, nodesByGrammarLabel, duplicateContextBoundaryLabels)
	}
}

// ============================================================
// ANALYSIS (SINGLE TREE)
// ============================================================

/*
ComputeAnalysisSingleTree runs nullable, first, and follow analysis on the grammar tree.

Returns a GrammarAnalysis with maps keyed by NodeKey. The root's GrammarLabel is used
as the current rule when descending; nested rule roots switch the current rule.
*/
func ComputeAnalysisSingleTree[TToken comparable](root *Grammar[TToken]) *GrammarAnalysis[TToken] {
	nullable := make(map[NodeKey]bool)
	first := make(map[NodeKey]TokenSet[TToken])
	follow := make(map[NodeKey]TokenSet[TToken])

	// 1. Nullable (Bottom-up pass)
	computeNullable(root, root.GrammarLabel, nullable)

	// 2. First Sets (Fixed-point iteration)
	// Must repeat until no more tokens can be added to any set
	for changed := true; changed; {
		changed = propagateFirst(root, root.GrammarLabel, nullable, first)
	}

	// 3. Follow Sets (Fixed-point iteration)
	initializeFollow(root, root.GrammarLabel, follow)
	for changed := true; changed; {
		changed = propagateFollow(root, root.GrammarLabel, nullable, first, follow)
	}

	return &GrammarAnalysis[TToken]{Nullable: nullable, First: first, Follow: follow}
}

// ============================================================
// NULLABLE
// ============================================================

/*
computeNullable computes whether each grammar node can derive the empty string.

Fills the out map keyed by NodeKey. Returns true if the current node is nullable.
GEpsilon and GOptional are nullable; GToken and GNest are not; GRepeat is nullable if Min is 0;
GConcat is nullable iff all children are; GChoice is nullable iff any child is.
*/
func computeNullable[TToken comparable](g *Grammar[TToken], currentRule GrammarLabel, out map[NodeKey]bool) bool {
	key := NodeKeyFromPath(*g.NodePath)

	var res bool
	switch g.Kind {
	case GEpsilon, GOptional:
		res = true
	case GToken, GNest:
		res = false
	case GRepeat:
		res = g.Min == 0 || computeNullable(g.Children[0], currentRule, out)
	case GChoice:
		for _, c := range g.Children {
			if computeNullable(c, currentRule, out) {
				res = true
			}
		}
	case GConcat:
		res = true
		for _, c := range g.Children {
			if !computeNullable(c, currentRule, out) {
				res = false
			}
		}
	}

	// Recurse for internal map population even if parent result is known
	if g.Kind == GOptional || g.Kind == GRepeat || g.Kind == GNest {
		computeNullable(g.Children[0], currentRule, out)
	}

	out[key] = res
	return res
}

// ============================================================
// FIRST
// ============================================================

func propagateFirst[TToken comparable](g *Grammar[TToken], currentRule GrammarLabel, nullable map[NodeKey]bool, out map[NodeKey]TokenSet[TToken]) bool {
	if g == nil {
		return false
	}
	if g.IsContextBoundary {
		currentRule = g.GrammarLabel
	}

	key := NodeKeyFromPath(*g.NodePath)
	changed := false
	set := getOrInit(out, key)

	switch g.Kind {
	case GToken:
		if _, exists := set[g.Token]; !exists {
			set[g.Token] = struct{}{}
			changed = true
		}
	case GChoice, GOptional, GRepeat:
		for _, c := range g.Children {
			if mergeInto(set, out[NodeKeyFromPath(*c.NodePath)]) {
				changed = true
			}
		}
	case GConcat:
		for _, c := range g.Children {
			childKey := NodeKeyFromPath(*c.NodePath)
			if mergeInto(set, out[childKey]) {
				changed = true
			}
			if !nullable[childKey] {
				break
			}
		}
	case GNest:
		if _, exists := set[*g.OpenToken]; !exists {
			set[*g.OpenToken] = struct{}{}
			changed = true
		}
	}

	for _, c := range g.Children {
		if propagateFirst(c, currentRule, nullable, out) {
			changed = true
		}
	}
	return changed
}

// ============================================================
// FOLLOW
// ============================================================

func propagateFollow[TToken comparable](
	g *Grammar[TToken],
	currentRule GrammarLabel,
	nullable map[NodeKey]bool,
	first map[NodeKey]TokenSet[TToken],
	follow map[NodeKey]TokenSet[TToken],
) bool {
	if g == nil {
		return false
	}

	// 1. Update context if this node is an explicit Rule root
	if g.IsContextBoundary {
		currentRule = g.GrammarLabel
	}

	changed := false
	parentKey := NodeKeyFromPath(*g.NodePath)

	// Ensure parent set exists to avoid nil checks in child merges
	if follow[parentKey] == nil {
		follow[parentKey] = make(TokenSet[TToken])
	}

	switch g.Kind {

	case GChoice:
		// Every alternative in a choice inherits the follow set of the choice itself
		// Choice ::= ( A | B | C ) Follow(Choice) -> Follow(A), Follow(B), Follow(C)
		for _, c := range g.Children {
			childKey := NodeKeyFromPath(*c.NodePath)
			if mergeInto(getOrInit(follow, childKey), follow[parentKey]) {
				changed = true
			}
		}

	case GConcat:
		for i := 0; i < len(g.Children); i++ {
			A := g.Children[i]
			AKey := NodeKeyFromPath(*A.NodePath)
			targetFollow := getOrInit(follow, AKey)

			// Rule 1: A is followed by FIRST of everything to its right
			for j := i + 1; j < len(g.Children); j++ {
				B := g.Children[j]
				BKey := NodeKeyFromPath(*B.NodePath)

				if mergeInto(targetFollow, first[BKey]) {
					changed = true
				}

				if !nullable[BKey] {
					break
				}
			}

			// Rule 2: If everything to the right is nullable, A inherits parent's FOLLOW
			allRightNullable := true
			for j := i + 1; j < len(g.Children); j++ {
				if !nullable[NodeKeyFromPath(*g.Children[j].NodePath)] {
					allRightNullable = false
					break
				}
			}

			if allRightNullable {
				if mergeInto(targetFollow, follow[parentKey]) {
					changed = true
				}
			}
		}

	case GRepeat:
		// A* -> body can be followed by its own FIRST (looping)
		// A* -> body also inherits parent's FOLLOW (exiting)
		body := g.Children[0]
		bodyKey := NodeKeyFromPath(*body.NodePath)
		bodyFollow := getOrInit(follow, bodyKey)

		if mergeInto(bodyFollow, first[bodyKey]) {
			changed = true
		}
		if mergeInto(bodyFollow, follow[parentKey]) {
			changed = true
		}

	case GOptional:
		// Optional(A) -> A inherits parent's FOLLOW
		body := g.Children[0]
		bodyKey := NodeKeyFromPath(*body.NodePath)
		if mergeInto(getOrInit(follow, bodyKey), follow[parentKey]) {
			changed = true
		}

	case GNest:
		// Nest ::= Open Body Close
		// 1. Body is followed by the Close token
		// 2. The Close token (virtual or real) inherits the Nest's FOLLOW
		body := g.Children[0]
		bodyKey := NodeKeyFromPath(*body.NodePath)
		bodyFollow := getOrInit(follow, bodyKey)

		if _, exists := bodyFollow[*g.CloseToken]; !exists {
			bodyFollow[*g.CloseToken] = struct{}{}
			changed = true
		}

		// Note: The Nest node itself is treated like an atom by its parent Concat,
		// so its Follow set is already correctly populated in the Concat logic.
	}

	// 3. Recurse to children
	for _, c := range g.Children {
		if propagateFollow(c, currentRule, nullable, first, follow) {
			changed = true
		}
	}

	return changed
}

// Helper to clean up map initialization
func getOrInit[TToken comparable](m map[NodeKey]TokenSet[TToken], key NodeKey) TokenSet[TToken] {
	if m[key] == nil {
		m[key] = make(TokenSet[TToken])
	}
	return m[key]
}

func initializeFollow[TToken comparable](
	g *Grammar[TToken],
	currentRule GrammarLabel,
	follow map[NodeKey]TokenSet[TToken],
) {
	if g.GrammarLabel != currentRule {
		currentRule = g.GrammarLabel
	}

	key := NodeKeyFromPath(*g.NodePath)
	if follow[key] == nil {
		follow[key] = make(TokenSet[TToken])
	}

	for _, c := range g.Children {
		initializeFollow(c, currentRule, follow)
	}
}

func mergeInto[TToken comparable](
	dst TokenSet[TToken],
	src TokenSet[TToken],
) bool {
	changed := false
	for t := range src {
		if _, exists := dst[t]; !exists {
			dst[t] = struct{}{}
			changed = true
		}
	}
	return changed
}

// ============================================================
// HELPERS
// ============================================================

/*
NodeKeyFromPath builds a NodeKey from a node path.

NodePath is unique per node in the tree. Using path as the sole key ensures analysis maps
(nullable, first, follow) are consistent when multiple nodes share the same GrammarLabel.
*/
func NodeKeyFromPath(path NodePath) NodeKey {
	return NodeKey(path)
}
