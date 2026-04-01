package syntaxa

import (
	"fmt"
	"io"
	"lexarch"
	"sort"
	"strings"
)

// ============================================================
// FORMATTER (semantic layer)
// ============================================================

/*
GrammarPackageDebugFormatter supplies string rendering for grammar package debug dumps.

All hooks are optional; missing ones fall back to default formatting (e.g. fmt.Sprintf).
Use for custom token or rule ID display (e.g. to match a GrammarDebugFormatter).
*/
type GrammarPackageDebugFormatter[TNodeKind comparable] struct {
	FormatPackageName  func(name string) string
	FormatVersion      func(version string) string
	FormatGrammarLabel func(GrammarLabel) string
	FormatToken        func(lexarch.TokenKind) string
	FormatNodeKey      func(NodeKey) string
	FormatTokenSet     func(TokenSet) string
	FormatNestSpec     func(NestSpec[TNodeKind]) string
}

func (f GrammarPackageDebugFormatter[TNodeKind]) packageName(name string) string {
	if f.FormatPackageName != nil {
		return f.FormatPackageName(name)
	}
	return name
}

func (f GrammarPackageDebugFormatter[TNodeKind]) version(version string) string {
	if f.FormatVersion != nil {
		return f.FormatVersion(version)
	}
	return version
}

func (f GrammarPackageDebugFormatter[TNodeKind]) grammarLabel(label GrammarLabel) string {
	if f.FormatGrammarLabel != nil {
		return f.FormatGrammarLabel(label)
	}
	return string(label)
}

func (f GrammarPackageDebugFormatter[TNodeKind]) token(t lexarch.TokenKind) string {
	if f.FormatToken != nil {
		return f.FormatToken(t)
	}
	return fmt.Sprintf("%v", t)
}

func (f GrammarPackageDebugFormatter[TNodeKind]) nodeKey(k NodeKey) string {
	if f.FormatNodeKey != nil {
		return f.FormatNodeKey(k)
	}
	return string(k)
}

/*
nodeKeyForAnalysis returns a display string for an analysis map key: "GrammarLabel (path)" when
PathToGrammarLabel is available, otherwise the raw key (path) or FormatNodeKey result.
Call from the debugger when rendering nullable/first/follow so nodes are shown by name with path in parentheses.
*/
func (f GrammarPackageDebugFormatter[TNodeKind]) nodeKeyForAnalysis(pkg *GrammarPackage[TNodeKind], k NodeKey) string {
	if pkg != nil && pkg.PathToGrammarLabel != nil {
		if label, ok := pkg.PathToGrammarLabel[k]; ok {
			return f.grammarLabel(label) + " (" + string(k) + ")"
		}
	}
	return f.nodeKey(k)
}

func (f GrammarPackageDebugFormatter[TNodeKind]) tokenSet(ts TokenSet) string {
	if f.FormatTokenSet != nil {
		return f.FormatTokenSet(ts)
	}
	if len(ts) == 0 {
		return "{}"
	}
	tokens := make([]string, 0, len(ts))
	for t := range ts {
		tokens = append(tokens, f.token(t))
	}
	sort.Slice(tokens, func(i, j int) bool { return tokens[i] < tokens[j] })
	return "{" + strings.Join(tokens, ", ") + "}"
}

func (f GrammarPackageDebugFormatter[TNodeKind]) nestSpec(n NestSpec[TNodeKind]) string {
	if f.FormatNestSpec != nil {
		return f.FormatNestSpec(n)
	}
	return fmt.Sprintf("%s open=%s close=%s owner=%s",
		f.grammarLabel(n.ID), f.token(n.Open), f.token(n.Close), f.grammarLabel(n.OwnerRule))
}

// ============================================================
// RENDERER (layout + IO)
// ============================================================

/*
GrammarPackageDebugger renders a GrammarPackage to a human-readable dump.

Output includes package metadata, rule IDs, tokens used, nest specs, and
nullable/first/follow analysis. Use NewGrammarPackageDebugger to construct.
*/
type GrammarPackageDebugger[TNodeKind comparable] struct {
	Formatter GrammarPackageDebugFormatter[TNodeKind]
}

/*
NewGrammarPackageDebugger creates a GrammarPackageDebugger with the given formatter.
*/
func NewGrammarPackageDebugger[TNodeKind comparable](
	formatter GrammarPackageDebugFormatter[TNodeKind],
) *GrammarPackageDebugger[TNodeKind] {
	return &GrammarPackageDebugger[TNodeKind]{Formatter: formatter}
}

/*
DumpTo writes the full debug dump of the grammar package to w.

Sections: package info, rules, tokens, nests, analysis (nullable, first, follow).
getAnalysis is optional; when nil or when it returns nil, the analysis section shows "(none)".
Use syntaxa/lowering.GetAnalysis(pkg) to supply analysis on demand.
*/
func (d *GrammarPackageDebugger[TNodeKind]) DumpTo(
	w io.Writer,
	pkg *GrammarPackage[TNodeKind],
	getAnalysis func() *GrammarAnalysis,
) error {
	if pkg == nil {
		_, err := io.WriteString(w, "<nil>\n")
		return err
	}

	f := d.Formatter

	if _, err := io.WriteString(w, "=== Package ===\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  name: %s\n", f.packageName(pkg.Name)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  version: %s\n", f.version(pkg.Version)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  entry: %s\n\n", f.grammarLabel(pkg.EntryRule)); err != nil {
		return err
	}

	if _, err := io.WriteString(w, "=== Rules ===\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  count: %d\n", len(pkg.Grammars)); err != nil {
		return err
	}
	ruleLabels := make([]GrammarLabel, 0, len(pkg.Grammars))
	for label := range pkg.Grammars {
		ruleLabels = append(ruleLabels, label)
	}
	sort.Slice(ruleLabels, func(i, j int) bool { return ruleLabels[i] < ruleLabels[j] })
	for _, label := range ruleLabels {
		if _, err := fmt.Fprintf(w, "  - %s\n", f.grammarLabel(label)); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}

	if _, err := io.WriteString(w, "=== Tokens used ===\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  count: %d\n", len(pkg.TokensUsed)); err != nil {
		return err
	}
	for _, t := range pkg.TokensUsed {
		if _, err := fmt.Fprintf(w, "  - %s\n", f.token(t)); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}

	if _, err := io.WriteString(w, "=== Nests ===\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  count: %d\n", len(pkg.Nests)); err != nil {
		return err
	}
	for _, n := range pkg.Nests {
		if _, err := fmt.Fprintf(w, "  - %s\n", f.nestSpec(n)); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}

	var analysis *GrammarAnalysis
	if getAnalysis != nil {
		analysis = getAnalysis()
	}
	if analysis == nil {
		if _, err := io.WriteString(w, "=== Analysis ===\n  (none)\n"); err != nil {
			return err
		}
		return nil
	}

	if _, err := io.WriteString(w, "=== Analysis (nullable) ===\n"); err != nil {
		return err
	}
	nullableKeys := sortedNodeKeys(analysis.Nullable)
	for _, k := range nullableKeys {
		v := analysis.Nullable[k]
		if _, err := fmt.Fprintf(w, "  %s -> %v\n", f.nodeKeyForAnalysis(pkg, k), v); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}

	if _, err := io.WriteString(w, "=== Analysis (first) ===\n"); err != nil {
		return err
	}
	firstKeys := sortedNodeKeysFirst(analysis.First)
	for _, k := range firstKeys {
		ts := analysis.First[k]
		if _, err := fmt.Fprintf(w, "  %s -> %s\n", f.nodeKeyForAnalysis(pkg, k), f.tokenSet(ts)); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}

	if _, err := io.WriteString(w, "=== Analysis (follow) ===\n"); err != nil {
		return err
	}
	followKeys := sortedNodeKeysFirst(analysis.Follow)
	for _, k := range followKeys {
		ts := analysis.Follow[k]
		if _, err := fmt.Fprintf(w, "  %s -> %s\n", f.nodeKeyForAnalysis(pkg, k), f.tokenSet(ts)); err != nil {
			return err
		}
	}

	return nil
}

/*
DumpString returns the full debug dump of the grammar package as a string.
getAnalysis is optional; pass nil to omit analysis or lowering.GetAnalysis(pkg).
*/
func (d *GrammarPackageDebugger[TNodeKind]) DumpString(
	pkg *GrammarPackage[TNodeKind],
	getAnalysis func() *GrammarAnalysis,
) string {
	var b strings.Builder
	_ = d.DumpTo(&b, pkg, getAnalysis)
	return b.String()
}

func sortedNodeKeys(m map[NodeKey]bool) []NodeKey {
	keys := make([]NodeKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func sortedNodeKeysFirst(m map[NodeKey]TokenSet) []NodeKey {
	keys := make([]NodeKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// ============================================================
// GrammarPackage API convenience
// ============================================================

/*
DebugDump produces a human-readable dump of the grammar package using the given formatter.

getAnalysis is optional; when nil the analysis section shows "(none)". Use
syntaxa/lowering.GetAnalysis(pkg) to include nullable/first/follow.
*/
func (p *GrammarPackage[TNodeKind]) DebugDump(
	formatter GrammarPackageDebugFormatter[TNodeKind],
	getAnalysis func() *GrammarAnalysis,
) string {
	dbg := NewGrammarPackageDebugger(formatter)
	return dbg.DumpString(p, getAnalysis)
}
