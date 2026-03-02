package syntaxa

import (
	"io"
	"strings"
)

// ============================================================
// FORMATTER (semantic layer)
// ============================================================

/*
GrammarDebugFormatter supplies string rendering for grammar debug dumps.

FormatKind is required; the rest are optional. Color functions may be nil for plain text.
*/
type GrammarDebugFormatter[TToken comparable] struct {
	/* REQUIRED */
	FormatKind func(GrammarKind) string

	/* Optional render hooks */
	FormatGrammarLabel    func(GrammarLabel) string
	FormatToken           func(TToken) string
	FormatRange           func(min int, max *int) string
	FormatRecoveryTokens  func(recovery []TToken, noConsume []TToken) string
	ColorRecoveryTokens   func(string) string

	/* Coloring layer (nil = no color) */
	ColorKind      func(string) string
	ColorGrammarLabel func(string) string
	ColorToken     func(string) string
	ColorRange     func(string) string
}

func (f GrammarDebugFormatter[TToken]) validate() {
	if f.FormatKind == nil {
		panic("GrammarDebugFormatter: FormatKind is required")
	}
}

func (f GrammarDebugFormatter[TToken]) applyColor(s string, colorFn func(string) string) string {
	if colorFn == nil {
		return s
	}
	return colorFn(s)
}

// ============================================================
// ENUMERATION (structural layer)
// ============================================================

type grammarDebugEdge[TToken comparable] struct {
	label string
	node  *Grammar[TToken]
}

/*
GrammarEdgeEnumerator defines how child edges of a grammar node are enumerated for debug dumps.

Custom implementations can reorder or label edges; the default uses child order and "body" for repeat/optional.
*/
type GrammarEdgeEnumerator[TToken comparable] interface {
	EdgesOf(node *Grammar[TToken]) []grammarDebugEdge[TToken]
}

type defaultGrammarEdgeEnumerator[TToken comparable] struct{}

func (e defaultGrammarEdgeEnumerator[TToken]) EdgesOf(
	g *Grammar[TToken],
) []grammarDebugEdge[TToken] {

	if g == nil {
		return nil
	}

	switch g.Kind {

	case GConcat, GChoice, GNest:
		out := make([]grammarDebugEdge[TToken], len(g.Children))
		for i, ch := range g.Children {
			out[i] = grammarDebugEdge[TToken]{node: ch}
		}
		return out

	case GRepeat, GOptional:
		if len(g.Children) == 0 {
			return nil
		}
		return []grammarDebugEdge[TToken]{
			{label: "body", node: g.Children[0]},
		}

	default:
		return nil
	}
}

// ============================================================
// RENDERER (layout + IO)
// ============================================================

/*
GrammarDebugger renders a grammar tree to a human-readable dump (tree glyphs + formatted lines).

Use NewGrammarDebugger to construct; then DumpTo or DumpString to produce output.
*/
type GrammarDebugger[TToken comparable] struct {
	Formatter  GrammarDebugFormatter[TToken]
	Enumerator GrammarEdgeEnumerator[TToken]

	GlyphMid   string
	GlyphLast  string
	GlyphVert  string
	GlyphBlank string
}

/*
NewGrammarDebugger creates a GrammarDebugger with the given formatter and default tree glyphs.

Panics if formatter.FormatKind is nil.
*/
func NewGrammarDebugger[TToken comparable](
	formatter GrammarDebugFormatter[TToken],
) *GrammarDebugger[TToken] {

	formatter.validate()

	return &GrammarDebugger[TToken]{
		Formatter:  formatter,
		Enumerator: defaultGrammarEdgeEnumerator[TToken]{},

		GlyphMid:   "├─ ",
		GlyphLast:  "└─ ",
		GlyphVert:  "│  ",
		GlyphBlank: "   ",
	}
}

/*
DumpTo writes the full debug dump of the grammar tree to w.

Returns any write error. If root is nil, writes "<nil>\n".
*/
func (d *GrammarDebugger[TToken]) DumpTo(
	w io.Writer,
	root *Grammar[TToken],
) error {

	if root == nil {
		_, err := io.WriteString(w, "<nil>\n")
		return err
	}

	if err := d.writeNodeLine(w, root); err != nil {
		return err
	}

	edges := d.edgesOf(root)
	for i := range edges {
		last := i == len(edges)-1
		if err := d.walkEdge(w, edges[i], "", last, 1); err != nil {
			return err
		}
	}

	return nil
}

/*
DumpString returns the full debug dump of the grammar tree as a string.
*/
func (d *GrammarDebugger[TToken]) DumpString(root *Grammar[TToken]) string {
	var b strings.Builder
	_ = d.DumpTo(&b, root)
	return b.String()
}

// ------------------------------------------------------------
// internal helpers
// ------------------------------------------------------------

func (d *GrammarDebugger[TToken]) edgesOf(
	g *Grammar[TToken],
) []grammarDebugEdge[TToken] {
	if d.Enumerator == nil {
		return defaultGrammarEdgeEnumerator[TToken]{}.EdgesOf(g)
	}
	return d.Enumerator.EdgesOf(g)
}

func (d *GrammarDebugger[TToken]) writePrefix(
	w io.Writer,
	prefix string,
	isLast bool,
	depth int,
) error {
	if depth == 0 {
		return nil
	}
	if isLast {
		_, err := io.WriteString(w, prefix+d.GlyphLast)
		return err
	}
	_, err := io.WriteString(w, prefix+d.GlyphMid)
	return err
}

func (d *GrammarDebugger[TToken]) nextPrefix(prefix string, isLast bool, depth int) string {
	if depth == 0 {
		return ""
	}
	if isLast {
		return prefix + d.GlyphBlank
	}
	return prefix + d.GlyphVert
}

func (d *GrammarDebugger[TToken]) walkEdge(
	w io.Writer,
	e grammarDebugEdge[TToken],
	prefix string,
	isLast bool,
	depth int,
) error {

	if err := d.writePrefix(w, prefix, isLast, depth); err != nil {
		return err
	}

	if e.label != "" {
		if _, err := io.WriteString(w, e.label+": "); err != nil {
			return err
		}
	}

	if err := d.writeNodeLine(w, e.node); err != nil {
		return err
	}

	childPrefix := d.nextPrefix(prefix, isLast, depth)
	return d.walkChildren(w, e.node, childPrefix, depth+1)
}

func (d *GrammarDebugger[TToken]) walkChildren(
	w io.Writer,
	parent *Grammar[TToken],
	prefix string,
	depth int,
) error {

	edges := d.edgesOf(parent)
	for i := range edges {
		last := i == len(edges)-1
		if err := d.walkEdge(w, edges[i], prefix, last, depth); err != nil {
			return err
		}
	}
	return nil
}

func (d *GrammarDebugger[TToken]) writeNodeLine(
	w io.Writer,
	g *Grammar[TToken],
) error {

	f := d.Formatter

	// ---- Kind ----

	kind := f.FormatKind(g.Kind)
	kind = f.applyColor(kind, f.ColorKind)

	if _, err := io.WriteString(w, kind); err != nil {
		return err
	}

	// ---- Grammar ID ----

	if g.GrammarLabel != "" && f.FormatGrammarLabel != nil {
		id := f.FormatGrammarLabel(g.GrammarLabel)
		id = f.applyColor(id, f.ColorGrammarLabel)

		if _, err := io.WriteString(w, " "+id); err != nil {
			return err
		}
	}

	// ---- Kind-specific payload ----

	switch g.Kind {

	case GToken:
		if f.FormatToken != nil {
			txt := f.FormatToken(g.Token)
			txt = f.applyColor(txt, f.ColorToken)

			if _, err := io.WriteString(w, " "+txt); err != nil {
				return err
			}
		}

	case GRepeat:
		if f.FormatRange != nil {
			txt := f.FormatRange(g.Min, g.Max)
			txt = f.applyColor(txt, f.ColorRange)

			if _, err := io.WriteString(w, " "+txt); err != nil {
				return err
			}
		}
	}

	if len(g.RecoveryTokens) > 0 || len(g.NoConsumeOnRecoveryTokens) > 0 {
		if f.FormatRecoveryTokens != nil {
			txt := f.FormatRecoveryTokens(g.RecoveryTokens, g.NoConsumeOnRecoveryTokens)
			txt = f.applyColor(txt, f.ColorRecoveryTokens)
			if _, err := io.WriteString(w, " "+txt); err != nil {
				return err
			}
		}
	}

	_, err := io.WriteString(w, "\n")
	return err
}

// ============================================================
// Grammar API convenience
// ============================================================

/*
DebugDump produces a human-readable tree dump of the grammar using the given formatter.

Convenience wrapper around NewGrammarDebugger and DumpString.
*/
func (g *Grammar[TToken]) DebugDump(
	formatter GrammarDebugFormatter[TToken],
) string {
	dbg := NewGrammarDebugger(formatter)
	return dbg.DumpString(g)
}
