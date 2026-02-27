package syntaxa

import (
	"fmt"
	"io"
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
type GrammarPackageDebugFormatter[TToken comparable] struct {
	FormatPackageName func(name string) string
	FormatVersion     func(version string) string
	FormatGrammarID   func(GrammarID) string
	FormatToken       func(TToken) string
	FormatNodeKey     func(NodeKey) string
	FormatTokenSet    func(TokenSet[TToken]) string
	FormatNestSpec    func(NestSpec[TToken]) string
}

func (f GrammarPackageDebugFormatter[TToken]) packageName(name string) string {
	if f.FormatPackageName != nil {
		return f.FormatPackageName(name)
	}
	return name
}

func (f GrammarPackageDebugFormatter[TToken]) version(version string) string {
	if f.FormatVersion != nil {
		return f.FormatVersion(version)
	}
	return version
}

func (f GrammarPackageDebugFormatter[TToken]) grammarID(id GrammarID) string {
	if f.FormatGrammarID != nil {
		return f.FormatGrammarID(id)
	}
	return string(id)
}

func (f GrammarPackageDebugFormatter[TToken]) token(t TToken) string {
	if f.FormatToken != nil {
		return f.FormatToken(t)
	}
	return fmt.Sprintf("%v", t)
}

func (f GrammarPackageDebugFormatter[TToken]) nodeKey(k NodeKey) string {
	if f.FormatNodeKey != nil {
		return f.FormatNodeKey(k)
	}
	return string(k)
}

/*
nodeKeyForAnalysis returns a display string for an analysis map key: "GrammarID (path)" when
PathToGrammarID is available, otherwise the raw key (path) or FormatNodeKey result.
Call from the debugger when rendering nullable/first/follow so nodes are shown by name with path in parentheses.
*/
func (f GrammarPackageDebugFormatter[TToken]) nodeKeyForAnalysis(pkg *GrammarPackage[TToken], k NodeKey) string {
	if pkg != nil && pkg.PathToGrammarID != nil {
		if id, ok := pkg.PathToGrammarID[k]; ok {
			return f.grammarID(id) + " (" + string(k) + ")"
		}
	}
	return f.nodeKey(k)
}

func (f GrammarPackageDebugFormatter[TToken]) tokenSet(ts TokenSet[TToken]) string {
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

func (f GrammarPackageDebugFormatter[TToken]) nestSpec(n NestSpec[TToken]) string {
	if f.FormatNestSpec != nil {
		return f.FormatNestSpec(n)
	}
	return fmt.Sprintf("%s open=%s close=%s owner=%s",
		f.grammarID(n.ID), f.token(n.Open), f.token(n.Close), f.grammarID(n.OwnerRule))
}

// ============================================================
// RENDERER (layout + IO)
// ============================================================

/*
GrammarPackageDebugger renders a GrammarPackage to a human-readable dump.

Output includes package metadata, rule IDs, tokens used, nest specs, and
nullable/first/follow analysis. Use NewGrammarPackageDebugger to construct.
*/
type GrammarPackageDebugger[TToken comparable] struct {
	Formatter GrammarPackageDebugFormatter[TToken]
}

/*
NewGrammarPackageDebugger creates a GrammarPackageDebugger with the given formatter.
*/
func NewGrammarPackageDebugger[TToken comparable](
	formatter GrammarPackageDebugFormatter[TToken],
) *GrammarPackageDebugger[TToken] {
	return &GrammarPackageDebugger[TToken]{Formatter: formatter}
}

/*
DumpTo writes the full debug dump of the grammar package to w.

Sections: package info, rules, tokens, nests, analysis (nullable, first, follow).
Returns any write error.
*/
func (d *GrammarPackageDebugger[TToken]) DumpTo(
	w io.Writer,
	pkg *GrammarPackage[TToken],
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
	if _, err := fmt.Fprintf(w, "  entry: %s\n\n", f.grammarID(pkg.EntryRule)); err != nil {
		return err
	}

	if _, err := io.WriteString(w, "=== Rules ===\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  count: %d\n", len(pkg.Rules)); err != nil {
		return err
	}
	ruleIDs := make([]GrammarID, 0, len(pkg.Rules))
	for id := range pkg.Rules {
		ruleIDs = append(ruleIDs, id)
	}
	sort.Slice(ruleIDs, func(i, j int) bool { return ruleIDs[i] < ruleIDs[j] })
	for _, id := range ruleIDs {
		if _, err := fmt.Fprintf(w, "  - %s\n", f.grammarID(id)); err != nil {
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

	if pkg.Analysis == nil {
		if _, err := io.WriteString(w, "=== Analysis ===\n  (none)\n"); err != nil {
			return err
		}
		return nil
	}

	if _, err := io.WriteString(w, "=== Analysis (nullable) ===\n"); err != nil {
		return err
	}
	nullableKeys := sortedNodeKeys(pkg.Analysis.Nullable)
	for _, k := range nullableKeys {
		v := pkg.Analysis.Nullable[k]
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
	firstKeys := sortedNodeKeysFirst(pkg.Analysis.First)
	for _, k := range firstKeys {
		ts := pkg.Analysis.First[k]
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
	followKeys := sortedNodeKeysFirst(pkg.Analysis.Follow)
	for _, k := range followKeys {
		ts := pkg.Analysis.Follow[k]
		if _, err := fmt.Fprintf(w, "  %s -> %s\n", f.nodeKeyForAnalysis(pkg, k), f.tokenSet(ts)); err != nil {
			return err
		}
	}

	return nil
}

/*
DumpString returns the full debug dump of the grammar package as a string.
*/
func (d *GrammarPackageDebugger[TToken]) DumpString(pkg *GrammarPackage[TToken]) string {
	var b strings.Builder
	_ = d.DumpTo(&b, pkg)
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

func sortedNodeKeysFirst[TToken comparable](m map[NodeKey]TokenSet[TToken]) []NodeKey {
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

Convenience wrapper around NewGrammarPackageDebugger and DumpString.
*/
func (p *GrammarPackage[TToken]) DebugDump(
	formatter GrammarPackageDebugFormatter[TToken],
) string {
	dbg := NewGrammarPackageDebugger(formatter)
	return dbg.DumpString(p)
}
