package syntaxa

import (
	"autarch/pattern"
	"cmp"
	"fmt"
	"foundation/formatting"
	"foundation/hash"
	"lexarch"
	"slices"
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
type TokenSet map[lexarch.TokenKind]struct{}

/*
GrammarPackage is the flattened form of a grammar tree produced by ProducePackage.

It contains the rule map, tokens used, nest specs, and path/label maps. It does not contain
the core CFG (pattern/Contexta IR) or nullable/first/follow analysis; those are produced
on demand via syntaxa/lowering (ToPatternGrammar, GetAnalysis).

PathToGrammarLabel maps each node's path (NodeKey) to its GrammarLabel for debug display.
NodeByGrammarKey is 1:1 unique instance lookup. NodesByGrammarLabel is 1:N by semantic type.

EntryRuleParserRule is the executable rule for the entry production; set when producing
a package for the parser. Nil when the package is produced for analysis-only use (e.g. debug dumps).

Root and AdditionalRules are kept for on-demand lowering (e.g. ToPatternGrammar, BuildStateGraph).
*/
type GrammarPackage[TNodeKind comparable] struct {
	Name                string
	Version             string
	EntryRule           GrammarLabel
	Root                *Grammar[lexarch.TokenKind, TNodeKind]
	AdditionalRules     []*Grammar[lexarch.TokenKind, TNodeKind]
	Grammars            map[GrammarLabel]*Grammar[lexarch.TokenKind, TNodeKind]
	SortedGrammarLabels []GrammarLabel
	TokensUsed          []lexarch.TokenKind
	Nests               []NestSpec[TNodeKind]
	PathToGrammarLabel  map[NodeKey]GrammarLabel
	NodeByGrammarKey    map[GrammarKey]*Grammar[lexarch.TokenKind, TNodeKind]
	NodesByGrammarLabel map[GrammarLabel][]*Grammar[lexarch.TokenKind, TNodeKind]
	// MergedRecoveryByGrammarLabel stores compile-time merged recovery metadata per rule label.
	// It includes inherited reference-chain recovery/no-consume tokens in stable order.
	MergedRecoveryByGrammarLabel map[GrammarLabel]RecoverySpec
	EntryRuleParserRule          *ParserRule[TNodeKind]
}

/*
NestSpec describes one bracketed (GNest) production: open token, close token, and owning rule.

Used by the parser for balanced delimiter matching and error recovery.
ID and OwnerRule are GrammarLabels (context-boundary nodes; unique per nest).
*/
type NestSpec[TNodeKind comparable] struct {
	ID        GrammarLabel
	Open      lexarch.TokenKind
	Close     lexarch.TokenKind
	OwnerRule GrammarLabel
	Node      *Grammar[lexarch.TokenKind, TNodeKind]
}

/*
GuardedArm holds per-production predictive data for a grammar node that is the single
non-terminal RHS of a Contexta rule production (e.g. one arm of a Syntaxa GChoice).

ArmPredict maps that child node’s NodeKey to its clause FIRST (including FOLLOW of the
parent when the arm is nullable) and the production guard (predict lookahead list).
*/
type GuardedArm struct {
	First TokenSet
	Guard []Lookahead[lexarch.TokenKind]
}

/*
GrammarAnalysis holds the results of nullable, first, and follow computation for a grammar.

Maps are keyed by NodeKey. Used by the parser for prediction and error reporting.

ArmPredict is populated from pattern ProductionClauses when lowering provides the Contexta
grammar; see GrammarAnalysisFromPattern.
*/
type GrammarAnalysis struct {
	Nullable   map[NodeKey]bool
	First      map[NodeKey]TokenSet
	Follow     map[NodeKey]TokenSet
	ArmPredict map[NodeKey]GuardedArm
}

/*
RecoverySpec holds recovery token sets for a rule, used by Editor IR to resync on error.

Tokens are sync tokens at which the engine resyncs (consumes until one is seen).
NoConsume are sync tokens where the token is left in the stream for the parent.
*/
type RecoverySpec struct {
	Tokens    []lexarch.TokenKind
	NoConsume []lexarch.TokenKind
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
func ProducePackage[TNodeKind comparable](
	root *Grammar[lexarch.TokenKind, TNodeKind],
	additionalRules []*Grammar[lexarch.TokenKind, TNodeKind],
	name string,
	version string,
	entryRule *ParserRule[TNodeKind],
) GrammarPackage[TNodeKind] {
	if root == nil {
		panic("ProducePackage: root grammar is nil")
	}

	// 1. Assign deterministic paths to all nodes in the global graph
	finalizeGraphPaths(root, additionalRules)

	hasher := hash.XXH3HasherCreateWithSeed(0)
	rules := make(map[GrammarLabel]*Grammar[lexarch.TokenKind, TNodeKind])
	tokenSet := make(TokenSet)
	nests := make([]NestSpec[TNodeKind], 0)
	nodeByGrammarKey := make(map[GrammarKey]*Grammar[lexarch.TokenKind, TNodeKind])
	nodesByGrammarLabel := make(map[GrammarLabel][]*Grammar[lexarch.TokenKind, TNodeKind])
	duplicateContextBoundaryLabels := make(map[GrammarLabel]struct{})
	visitedLabels := make(map[GrammarLabel]struct{}) // To prevent duplicate Rules/Nests
	visitedPaths := make(map[GrammarKey]struct{})    // To prevent infinite recursion in graph walks

	slices.SortFunc(additionalRules, func(a, b *Grammar[lexarch.TokenKind, TNodeKind]) int {
		return cmp.Compare(a.GrammarLabel, b.GrammarLabel)
	})

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
			Separator: "\n  - ",
			Prefix:    "\n  - ",
			Quote:     true,
		})

		panic(fmt.Sprintf("syntaxa: duplicate grammar labels among context-boundary nodes (engine error):%s", list))
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
	mergedRecoveryByGrammarLabel := RecoverySpecMergedByGrammarLabel(rules)

	tokensUsed := make([]lexarch.TokenKind, 0, len(tokenSet))
	for t := range tokenSet {
		tokensUsed = append(tokensUsed, t)
	}

	sortedGrammarLabels := make([]GrammarLabel, 0, len(rules))
	for label := range rules {
		sortedGrammarLabels = append(sortedGrammarLabels, label)
	}
	sort.Slice(sortedGrammarLabels, func(i, j int) bool {
		return sortedGrammarLabels[i] < sortedGrammarLabels[j]
	})

	pathToGrammarLabel := buildPathToGrammarLabel(root, additionalRules)

	additionalCopy := make([]*Grammar[lexarch.TokenKind, TNodeKind], len(additionalRules))
	copy(additionalCopy, additionalRules)

	return GrammarPackage[TNodeKind]{
		Name:                         name,
		Version:                      version,
		EntryRule:                    root.GrammarLabel,
		Root:                         root,
		AdditionalRules:              additionalCopy,
		Grammars:                     rules,
		SortedGrammarLabels:          sortedGrammarLabels,
		TokensUsed:                   tokensUsed,
		Nests:                        nests,
		PathToGrammarLabel:           pathToGrammarLabel,
		NodeByGrammarKey:             nodeByGrammarKey,
		NodesByGrammarLabel:          nodesByGrammarLabel,
		MergedRecoveryByGrammarLabel: mergedRecoveryByGrammarLabel,
		EntryRuleParserRule:          entryRule,
	}
}

// ============================================================
// ANALYSIS (DELEGATED TO PATTERN)
// ============================================================

/*
GrammarAnalysisFromPattern builds Syntaxa's GrammarAnalysis from pattern's analysis.

grammar is the same Contexta grammar passed to ComputeAnalysis (required to attach
per-production clauses to child rule names). It may be nil; ArmPredict is left empty.

ruleNameToNodeKey maps pattern rule names (readable Label_hash) to NodeKey; when present
each pattern key is copied to the corresponding NodeKey. When nil, pattern keys are
used as NodeKey directly (backward compatibility). Exported for use by syntaxa/lowering.
*/
func GrammarAnalysisFromPattern(
	grammar *pattern.Grammar[lexarch.TokenKind, struct{}],
	pa *pattern.GrammarAnalysis[lexarch.TokenKind],
	ruleNameToNodeKey map[string]NodeKey,
) *GrammarAnalysis {
	empty := func() *GrammarAnalysis {
		return &GrammarAnalysis{
			Nullable:   make(map[NodeKey]bool),
			First:      make(map[NodeKey]TokenSet),
			Follow:     make(map[NodeKey]TokenSet),
			ArmPredict: make(map[NodeKey]GuardedArm),
		}
	}
	if pa == nil {
		return empty()
	}
	nullable := make(map[NodeKey]bool)
	first := make(map[NodeKey]TokenSet)
	follow := make(map[NodeKey]TokenSet)
	armPredict := make(map[NodeKey]GuardedArm)

	nodeKey := func(ruleName string) NodeKey {
		if ruleNameToNodeKey != nil {
			if k, ok := ruleNameToNodeKey[ruleName]; ok {
				return k
			}
		}
		return NodeKey(ruleName)
	}

	for k, v := range pa.Nullable {
		nullable[nodeKey(k)] = v
	}
	for k, s := range pa.First {
		dst := make(TokenSet)
		for t := range s {
			dst[t] = struct{}{}
		}
		first[nodeKey(k)] = dst
	}
	for k, s := range pa.Follow {
		dst := make(TokenSet)
		for t := range s {
			dst[t] = struct{}{}
		}
		follow[nodeKey(k)] = dst
	}

	if grammar != nil && pa.ProductionClauses != nil {
		for _, rule := range grammar.Rules {
			clauses := pa.ProductionClauses[rule.NonTerminal]
			if len(clauses) == 0 {
				continue
			}
			for i := range rule.Productions {
				if i >= len(clauses) {
					break
				}
				clause := clauses[i]
				if clause.ChildRuleName == "" {
					continue
				}
				childKey := nodeKey(clause.ChildRuleName)
				dstFirst := make(TokenSet)
				for t := range clause.First {
					dstFirst[t] = struct{}{}
				}
				var dstGuard []Lookahead[lexarch.TokenKind]
				if len(clause.Guard) > 0 {
					dstGuard = make([]Lookahead[lexarch.TokenKind], len(clause.Guard))
					for j, g := range clause.Guard {
						dstGuard[j] = Lookahead[lexarch.TokenKind]{Offset: g.Offset, Expected: g.Token}
					}
				}
				armPredict[childKey] = GuardedArm{First: dstFirst, Guard: dstGuard}
			}
		}
	}

	return &GrammarAnalysis{
		Nullable:   nullable,
		First:      first,
		Follow:     follow,
		ArmPredict: armPredict,
	}
}

// ============================================================
// GLOBAL PATH ASSIGNMENT
// ============================================================

/*
finalizeGraphPaths assigns unique topological addresses to all nodes in the namespace.
The root tree starts at "0". Disconnected subgraphs start at "ext0", "ext1", etc.
*/
func finalizeGraphPaths[TNodeKind comparable](root *Grammar[lexarch.TokenKind, TNodeKind], additionals []*Grammar[lexarch.TokenKind, TNodeKind]) {
	if root.NodePath == nil {
		assignPathRec(root, "0")
	}
	for i, add := range additionals {
		if add.NodePath == nil {
			assignPathRec(add, fmt.Sprintf("ext%d", i))
		}
	}
}

func assignPathRec[TNodeKind comparable](g *Grammar[lexarch.TokenKind, TNodeKind], current string) {
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
func buildPathToGrammarLabel[TNodeKind comparable](root *Grammar[lexarch.TokenKind, TNodeKind], additionals []*Grammar[lexarch.TokenKind, TNodeKind]) map[NodeKey]GrammarLabel {
	out := make(map[NodeKey]GrammarLabel)
	buildPathToGrammarLabelRec(root, out)
	for _, add := range additionals {
		buildPathToGrammarLabelRec(add, out)
	}
	return out
}

func buildPathToGrammarLabelRec[TNodeKind comparable](g *Grammar[lexarch.TokenKind, TNodeKind], out map[NodeKey]GrammarLabel) {
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
func NestSpecsOpenTokenCounts[TNodeKind comparable](nests []NestSpec[TNodeKind]) map[lexarch.TokenKind]int {
	counts := make(map[lexarch.TokenKind]int)
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
func collectAll[TNodeKind comparable](
	hasher *hash.XXH3Hasher,
	g *Grammar[lexarch.TokenKind, TNodeKind],
	rules map[GrammarLabel]*Grammar[lexarch.TokenKind, TNodeKind],
	tokenSet TokenSet,
	nests *[]NestSpec[TNodeKind],
	nodeByGrammarKey map[GrammarKey]*Grammar[lexarch.TokenKind, TNodeKind],
	nodesByGrammarLabel map[GrammarLabel][]*Grammar[lexarch.TokenKind, TNodeKind],
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

	_, labelSeen := visitedLabels[g.GrammarLabel]

	if g.IsContextBoundary {
		if existing, seen := rules[g.GrammarLabel]; seen {
			if existing != g {
				duplicateContextBoundaryLabels[g.GrammarLabel] = struct{}{}
			}
		} else {
			rules[g.GrammarLabel] = g
		}
	}

	switch g.Kind {
	case GToken:
		tokenSet[g.Token] = struct{}{}
	case GNest:
		tokenSet[*g.OpenToken] = struct{}{}
		tokenSet[*g.CloseToken] = struct{}{}

		if !labelSeen {
			*nests = append(*nests, NestSpec[TNodeKind]{
				ID:        g.GrammarLabel,
				Open:      *g.OpenToken,
				Close:     *g.CloseToken,
				OwnerRule: g.GrammarLabel,
				Node:      g,
			})
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
func collectUnresolvedReferences[TNodeKind comparable](
	root *Grammar[lexarch.TokenKind, TNodeKind],
	additionals []*Grammar[lexarch.TokenKind, TNodeKind],
	rules map[GrammarLabel]*Grammar[lexarch.TokenKind, TNodeKind],
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

func collectUnresolvedReferencesRec[TNodeKind comparable](
	g *Grammar[lexarch.TokenKind, TNodeKind],
	rules map[GrammarLabel]*Grammar[lexarch.TokenKind, TNodeKind],
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
func setResolvedReferences[TNodeKind comparable](
	g *Grammar[lexarch.TokenKind, TNodeKind],
	rules map[GrammarLabel]*Grammar[lexarch.TokenKind, TNodeKind],
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

/*
RecoverySpecMergedByGrammarLabel computes merged recovery specs for each rule label.

Each merged spec includes the rule's own recovery/no-consume tokens plus inherited tokens
from its reference-target chain (when the rule root is GReference), preserving stable append order.
*/
func RecoverySpecMergedByGrammarLabel[TNodeKind comparable](
	rules map[GrammarLabel]*Grammar[lexarch.TokenKind, TNodeKind],
) map[GrammarLabel]RecoverySpec {
	out := make(map[GrammarLabel]RecoverySpec, len(rules))
	for label := range rules {
		spec := recoverySpecMergedForLabel(label, rules)
		out[label] = spec
	}
	return out
}

func recoverySpecMergedForLabel[TNodeKind comparable](
	label GrammarLabel,
	rules map[GrammarLabel]*Grammar[lexarch.TokenKind, TNodeKind],
) RecoverySpec {
	var merged RecoverySpec

	currentLabel := label
	visited := make(map[GrammarLabel]struct{})

	for {
		if currentLabel == "" {
			break
		}
		if _, seen := visited[currentLabel]; seen {
			break
		}
		visited[currentLabel] = struct{}{}

		currentRule, exists := rules[currentLabel]
		if !exists || currentRule == nil {
			break
		}

		recoveryAppendUniqueStable(&merged.Tokens, currentRule.RecoveryTokens)
		recoveryAppendUniqueStable(&merged.NoConsume, currentRule.NoConsumeOnRecoveryTokens)

		if currentRule.Kind != GReference {
			break
		}

		nextLabel := currentRule.ReferenceTarget
		if currentRule.ResolvedReference != nil {
			nextLabel = currentRule.ResolvedReference.GrammarLabel
		}
		if nextLabel == "" {
			break
		}
		currentLabel = nextLabel
	}

	return merged
}

func recoveryAppendUniqueStable(dst *[]lexarch.TokenKind, src []lexarch.TokenKind) {
	for _, token := range src {
		if recoveryContainsToken(*dst, token) {
			continue
		}
		*dst = append(*dst, token)
	}
}

func recoveryContainsToken(tokens []lexarch.TokenKind, token lexarch.TokenKind) bool {
	for _, current := range tokens {
		if current == token {
			return true
		}
	}
	return false
}

// ============================================================
// HELPERS
// ============================================================

func mergeInto(dst TokenSet, src TokenSet) bool {
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
