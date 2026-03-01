package syntaxa

// ============================================================
// TYPES
// ============================================================

/*
NodeKey uniquely identifies a grammar node for analysis (nullable, first, follow).

Canonical key is the node's NodePath (path from tree root). Use nodeKeyFromPath to build keys
so that analysis maps are consistent regardless of GrammarID reuse across nodes.
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
Produced by Grammar.ProducePackage from a root Grammar node.

PathToGrammarID maps each node's path (NodeKey) to its GrammarID for debug display,
so analysis dumps can show node names with path in parentheses, e.g. "PROGRAM (0)".
*/
type GrammarPackage[TToken comparable] struct {
	Name            string
	Version         string
	EntryRule       GrammarID
	Rules           map[GrammarID]*Grammar[TToken]
	TokensUsed      []TToken
	Nests           []NestSpec[TToken]
	Analysis        *GrammarAnalysis[TToken]
	PathToGrammarID map[NodeKey]GrammarID
}

/*
NestSpec describes one bracketed (GNest) production: open token, close token, and owning rule.

Used by the parser for balanced delimiter matching and error recovery.
*/
type NestSpec[TToken comparable] struct {
	ID        GrammarID
	Open      TToken
	Close     TToken
	OwnerRule GrammarID
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
ProducePackage builds a GrammarPackage from the receiver grammar tree.

Walks the tree to collect rules, tokens, and nest specs; assigns node paths if needed;
then computes nullable, first, and follow sets. Panics if the root grammar is nil.
*/
func (g *Grammar[TToken]) ProducePackage(
	name string,
	version string,
) GrammarPackage[TToken] {
	if g == nil {
		panic("ProducePackage: root grammar is nil")
	}

	// Ensure node paths assigned once for entire tree
	if g.NodePath == nil {
		g.FinalizeNodePaths()
	}

	rules := make(map[GrammarID]*Grammar[TToken])
	tokenSet := make(TokenSet[TToken])
	nests := make([]NestSpec[TToken], 0)

	// Single unified walk
	collectAll(g, rules, tokenSet, &nests)

	tokensUsed := make([]TToken, 0, len(tokenSet))
	for t := range tokenSet {
		tokensUsed = append(tokensUsed, t)
	}

	analysis := computeAnalysisSingleTree(g)
	pathToGrammarID := buildPathToGrammarID(g)

	return GrammarPackage[TToken]{
		Name:            name,
		Version:         version,
		EntryRule:       g.GrammarID,
		Rules:           rules,
		TokensUsed:      tokensUsed,
		Nests:           nests,
		Analysis:        analysis,
		PathToGrammarID: pathToGrammarID,
	}
}

/*
buildPathToGrammarID walks the grammar tree and builds a map from each node's path (NodeKey) to its GrammarID.
Used by the debugger to display analysis entries as "GrammarID (path)" instead of path alone.
*/
func buildPathToGrammarID[TToken comparable](g *Grammar[TToken]) map[NodeKey]GrammarID {
	out := make(map[NodeKey]GrammarID)
	buildPathToGrammarIDRec(g, out)
	return out
}

func buildPathToGrammarIDRec[TToken comparable](g *Grammar[TToken], out map[NodeKey]GrammarID) {
	if g == nil || g.NodePath == nil {
		return
	}
	key := nodeKeyFromPath(*g.NodePath)
	out[key] = g.GrammarID
	for _, c := range g.Children {
		buildPathToGrammarIDRec(c, out)
	}
}

// ============================================================
// UNIFIED TREE WALK
// ============================================================

/*
collectAll walks the grammar tree and populates rules, tokenSet, and nests.

Only nodes marked as rule roots (Grammar.RuleRoot, e.g. from Rule.Root) are added to rules;
first occurrence of each GrammarID among rule roots is stored. Tokens from GToken and GNest
nodes are added to tokenSet; GNest nodes are appended to nests.
*/
func collectAll[TToken comparable](
	g *Grammar[TToken],
	rules map[GrammarID]*Grammar[TToken],
	tokenSet TokenSet[TToken],
	nests *[]NestSpec[TToken],
) {
	if g == nil {
		return
	}

	if g.IsContextBoundary {
		if _, exists := rules[g.GrammarID]; !exists {
			rules[g.GrammarID] = g
		}
	}

	switch g.Kind {

	case GToken:
		tokenSet[g.Token] = struct{}{}

	case GNest:
		tokenSet[*g.OpenToken] = struct{}{}
		tokenSet[*g.CloseToken] = struct{}{}

		*nests = append(*nests, NestSpec[TToken]{
			ID:        g.GrammarID,
			Open:      *g.OpenToken,
			Close:     *g.CloseToken,
			OwnerRule: g.GrammarID,
			Node:      g,
		})
	}

	for _, child := range g.Children {
		collectAll(child, rules, tokenSet, nests)
	}
}

// ============================================================
// ANALYSIS (SINGLE TREE)
// ============================================================

/*
computeAnalysisSingleTree runs nullable, first, and follow analysis on the grammar tree.

Returns a GrammarAnalysis with maps keyed by NodeKey. The root's GrammarID is used
as the current rule when descending; nested rule roots switch the current rule.
*/
func computeAnalysisSingleTree[TToken comparable](root *Grammar[TToken]) *GrammarAnalysis[TToken] {
	nullable := make(map[NodeKey]bool)
	first := make(map[NodeKey]TokenSet[TToken])
	follow := make(map[NodeKey]TokenSet[TToken])

	// 1. Nullable (Bottom-up pass)
	computeNullable(root, root.GrammarID, nullable)

	// 2. First Sets (Fixed-point iteration)
	// Must repeat until no more tokens can be added to any set
	for changed := true; changed; {
		changed = propagateFirst(root, root.GrammarID, nullable, first)
	}

	// 3. Follow Sets (Fixed-point iteration)
	initializeFollow(root, root.GrammarID, follow)
	for changed := true; changed; {
		changed = propagateFollow(root, root.GrammarID, nullable, first, follow)
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
func computeNullable[TToken comparable](g *Grammar[TToken], currentRule GrammarID, out map[NodeKey]bool) bool {
	key := nodeKeyFromPath(*g.NodePath)

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

func propagateFirst[TToken comparable](g *Grammar[TToken], currentRule GrammarID, nullable map[NodeKey]bool, out map[NodeKey]TokenSet[TToken]) bool {
	if g == nil {
		return false
	}
	if g.IsContextBoundary {
		currentRule = g.GrammarID
	}

	key := nodeKeyFromPath(*g.NodePath)
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
			if mergeInto(set, out[nodeKeyFromPath(*c.NodePath)]) {
				changed = true
			}
		}
	case GConcat:
		for _, c := range g.Children {
			childKey := nodeKeyFromPath(*c.NodePath)
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
	currentRule GrammarID,
	nullable map[NodeKey]bool,
	first map[NodeKey]TokenSet[TToken],
	follow map[NodeKey]TokenSet[TToken],
) bool {
	if g == nil {
		return false
	}

	// 1. Update context if this node is an explicit Rule root
	if g.IsContextBoundary {
		currentRule = g.GrammarID
	}

	changed := false
	parentKey := nodeKeyFromPath(*g.NodePath)

	// Ensure parent set exists to avoid nil checks in child merges
	if follow[parentKey] == nil {
		follow[parentKey] = make(TokenSet[TToken])
	}

	switch g.Kind {

	case GChoice:
		// Every alternative in a choice inherits the follow set of the choice itself
		// Choice ::= ( A | B | C ) Follow(Choice) -> Follow(A), Follow(B), Follow(C)
		for _, c := range g.Children {
			childKey := nodeKeyFromPath(*c.NodePath)
			if mergeInto(getOrInit(follow, childKey), follow[parentKey]) {
				changed = true
			}
		}

	case GConcat:
		for i := 0; i < len(g.Children); i++ {
			A := g.Children[i]
			AKey := nodeKeyFromPath(*A.NodePath)
			targetFollow := getOrInit(follow, AKey)

			// Rule 1: A is followed by FIRST of everything to its right
			for j := i + 1; j < len(g.Children); j++ {
				B := g.Children[j]
				BKey := nodeKeyFromPath(*B.NodePath)

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
				if !nullable[nodeKeyFromPath(*g.Children[j].NodePath)] {
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
		bodyKey := nodeKeyFromPath(*body.NodePath)
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
		bodyKey := nodeKeyFromPath(*body.NodePath)
		if mergeInto(getOrInit(follow, bodyKey), follow[parentKey]) {
			changed = true
		}

	case GNest:
		// Nest ::= Open Body Close
		// 1. Body is followed by the Close token
		// 2. The Close token (virtual or real) inherits the Nest's FOLLOW
		body := g.Children[0]
		bodyKey := nodeKeyFromPath(*body.NodePath)
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
	currentRule GrammarID,
	follow map[NodeKey]TokenSet[TToken],
) {
	if g.GrammarID != currentRule {
		currentRule = g.GrammarID
	}

	key := nodeKeyFromPath(*g.NodePath)
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
nodeKeyFromPath builds a NodeKey from a node path.

NodePath is unique per node in the tree. Using path as the sole key ensures analysis maps
(nullable, first, follow) are consistent when multiple nodes share the same GrammarID.
*/
func nodeKeyFromPath(path NodePath) NodeKey {
	return NodeKey(path)
}
