package syntaxa

import (
	"cmp"
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
type GrammarPackageDebugFormatter[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable] struct {
	FormatPackageName func(name string) string
	FormatVersion     func(version string) string
	FormatGrammarLabel func(GrammarLabel) string
	FormatToken       func(TToken) string
	FormatNodeKey     func(NodeKey) string
	FormatTokenSet    func(TokenSet[TToken]) string
	FormatNestSpec    func(NestSpec[TToken]) string
}

func (f GrammarPackageDebugFormatter[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]) packageName(name string) string {
	if f.FormatPackageName != nil {
		return f.FormatPackageName(name)
	}
	return name
}

func (f GrammarPackageDebugFormatter[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]) version(version string) string {
	if f.FormatVersion != nil {
		return f.FormatVersion(version)
	}
	return version
}

func (f GrammarPackageDebugFormatter[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]) grammarLabel(label GrammarLabel) string {
	if f.FormatGrammarLabel != nil {
		return f.FormatGrammarLabel(label)
	}
	return string(label)
}

func (f GrammarPackageDebugFormatter[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]) token(t TToken) string {
	if f.FormatToken != nil {
		return f.FormatToken(t)
	}
	return fmt.Sprintf("%v", t)
}

func (f GrammarPackageDebugFormatter[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]) nodeKey(k NodeKey) string {
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
func (f GrammarPackageDebugFormatter[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]) nodeKeyForAnalysis(pkg *GrammarPackage[TObservation, TToken, TTokenRole, TNodeKind, TLexerState], k NodeKey) string {
	if pkg != nil && pkg.PathToGrammarLabel != nil {
		if label, ok := pkg.PathToGrammarLabel[k]; ok {
			return f.grammarLabel(label) + " (" + string(k) + ")"
		}
	}
	return f.nodeKey(k)
}

func (f GrammarPackageDebugFormatter[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]) tokenSet(ts TokenSet[TToken]) string {
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

func (f GrammarPackageDebugFormatter[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]) nestSpec(n NestSpec[TToken]) string {
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
type GrammarPackageDebugger[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable] struct {
	Formatter GrammarPackageDebugFormatter[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]
}

/*
NewGrammarPackageDebugger creates a GrammarPackageDebugger with the given formatter.
*/
func NewGrammarPackageDebugger[TObservation cmp.Ordered, TToken, TTokenRole, TNodeKind, TLexerState comparable](
	formatter GrammarPackageDebugFormatter[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
) *GrammarPackageDebugger[TObservation, TToken, TTokenRole, TNodeKind, TLexerState] {
	return &GrammarPackageDebugger[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]{Formatter: formatter}
}

/*
DumpTo writes the full debug dump of the grammar package to w.

Sections: package info, rules, tokens, nests, analysis (nullable, first, follow).
Returns any write error.
*/
func (d *GrammarPackageDebugger[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]) DumpTo(
	w io.Writer,
	pkg *GrammarPackage[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
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
	if _, err := fmt.Fprintf(w, "  count: %d\n", len(pkg.Rules)); err != nil {
		return err
	}
	ruleLabels := make([]GrammarLabel, 0, len(pkg.Rules))
	for label := range pkg.Rules {
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
func (d *GrammarPackageDebugger[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]) DumpString(pkg *GrammarPackage[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]) string {
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
func (p *GrammarPackage[TObservation, TToken, TTokenRole, TNodeKind, TLexerState]) DebugDump(
	formatter GrammarPackageDebugFormatter[TObservation, TToken, TTokenRole, TNodeKind, TLexerState],
) string {
	dbg := NewGrammarPackageDebugger(formatter)
	return dbg.DumpString(p)
}
