package syntaxa

import (
	"fmt"
	"foundation/extensions"
	"io"
	"sort"
	"strings"
)

// ============================================================
// FORMATTER (semantic layer)
// ============================================================

type LSTDebugFormatter[TKind comparable] struct {
	/* REQUIRED */
	FormatKind func(TKind) string

	/* Optional render hooks */
	FormatToken     func(Lexeme) string
	FormatAttribute func(key string, value any) string

	/* Coloring layer (nil = no color) */
	ColorKind      func(string) string
	ColorToken     func(string) string
	ColorAttribute func(string) string
	ColorSpan      func(string) string

	/* Position rendering */
	ShowByteSpan     bool
	ShowLineSpan     bool
	LineSpanSource   string
	LineSpanTabWidth int

	/* Structural extras */
	ShowTokens     bool
	ShowAttributes bool
	ShowNodeID     bool
	ShowRevision   bool

	/* Slot styling */
	SlotPrefix string // e.g. "@", "#", "slot:"
}

func (f LSTDebugFormatter[TKind]) validate() {
	if f.FormatKind == nil {
		panic("LSTDebugFormatter: FormatKind is required")
	}
	// SlotPrefix is optional; empty is allowed.
}

func (f LSTDebugFormatter[TKind]) applyColor(s string, colorFn func(string) string) string {
	if colorFn == nil {
		return s
	}
	return colorFn(s)
}

// ============================================================
// ENUMERATION (structural layer)
// ============================================================

type lstDebugEdge[TKind comparable] struct {
	isSlot bool
	name   string
	node   *SyntaxaLSTNode[TKind]
}

type LSTEdgeEnumerator[TKind comparable] interface {
	EdgesOf(node *SyntaxaLSTNode[TKind]) []lstDebugEdge[TKind]
}

// Default behavior: children in their existing order, slots sorted by key.
type defaultLSTEdgeEnumerator[TKind comparable] struct{}

func (e defaultLSTEdgeEnumerator[TKind]) EdgesOf(
	cur *SyntaxaLSTNode[TKind],
) []lstDebugEdge[TKind] {

	total := len(cur.children)
	if cur.slots != nil {
		total += len(cur.slots)
	}
	if total == 0 {
		return nil
	}

	out := make([]lstDebugEdge[TKind], 0, total)

	// Children (stable order as stored)
	for _, ch := range cur.children {
		out = append(out, lstDebugEdge[TKind]{node: ch})
	}

	// Slots (sorted by name)
	if cur.slots != nil {
		pairs := make([]extensions.KeyValuePair[string, *SyntaxaLSTNode[TKind]], 0, len(cur.slots))
		for k, v := range cur.slots {
			pairs = append(pairs, extensions.KeyValuePair[string, *SyntaxaLSTNode[TKind]]{Key: k, Value: v})
		}
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].Key < pairs[j].Key })

		for _, p := range pairs {
			if p.Value == nil {
				continue
			}
			out = append(out, lstDebugEdge[TKind]{
				isSlot: true,
				name:   p.Key,
				node:   p.Value,
			})
		}
	}

	return out
}

// ============================================================
// RENDERER (layout + IO)
// ============================================================

type LSTDebugger[TKind comparable] struct {
	Formatter  LSTDebugFormatter[TKind]
	Enumerator LSTEdgeEnumerator[TKind]

	// Override if you want different glyphs later.
	GlyphMid   string // "├─ "
	GlyphLast  string // "└─ "
	GlyphVert  string // "│  "
	GlyphBlank string // "   "
}

func NewLSTDebugger[TKind comparable](
	formatter LSTDebugFormatter[TKind],
) *LSTDebugger[TKind] {

	formatter.validate()

	return &LSTDebugger[TKind]{
		Formatter:  formatter,
		Enumerator: defaultLSTEdgeEnumerator[TKind]{},

		GlyphMid:   "├─ ",
		GlyphLast:  "└─ ",
		GlyphVert:  "│  ",
		GlyphBlank: "   ",
	}
}

func (d *LSTDebugger[TKind]) DumpTo(
	w io.Writer,
	root *SyntaxaLSTNode[TKind],
) error {

	if root == nil {
		_, err := io.WriteString(w, "<nil>\n")
		return err
	}

	// Root line (no tree glyph prefix)
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

func (d *LSTDebugger[TKind]) DumpString(
	root *SyntaxaLSTNode[TKind],
) string {
	var b strings.Builder
	_ = d.DumpTo(&b, root)
	return b.String()
}

// ------------------------------------------------------------
// internal helpers
// ------------------------------------------------------------

func (d *LSTDebugger[TKind]) edgesOf(
	n *SyntaxaLSTNode[TKind],
) []lstDebugEdge[TKind] {
	if d.Enumerator == nil {
		// Safe fallback
		return defaultLSTEdgeEnumerator[TKind]{}.EdgesOf(n)
	}
	return d.Enumerator.EdgesOf(n)
}

func (d *LSTDebugger[TKind]) writePrefix(
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

func (d *LSTDebugger[TKind]) nextPrefix(prefix string, isLast bool, depth int) string {
	if depth == 0 {
		return ""
	}
	if isLast {
		return prefix + d.GlyphBlank
	}
	return prefix + d.GlyphVert
}

func (d *LSTDebugger[TKind]) walkEdge(
	w io.Writer,
	e lstDebugEdge[TKind],
	prefix string,
	isLast bool,
	depth int,
) error {

	// Normal child edge: prefix + node line + recurse
	if !e.isSlot {
		if err := d.writePrefix(w, prefix, isLast, depth); err != nil {
			return err
		}
		if err := d.writeNodeLine(w, e.node); err != nil {
			return err
		}

		childPrefix := d.nextPrefix(prefix, isLast, depth)
		return d.walkChildren(w, e.node, childPrefix, depth+1)
	}

	// Slot edge: prefix + label + " → " + target node line (same line)
	if err := d.writePrefix(w, prefix, isLast, depth); err != nil {
		return err
	}

	label := d.Formatter.SlotPrefix + e.name
	label = d.Formatter.applyColor(label, d.Formatter.ColorAttribute)

	if _, err := io.WriteString(w, label+" → "); err != nil {
		return err
	}
	if err := d.writeNodeLine(w, e.node); err != nil {
		return err
	}

	// Slot target's children must continue with appropriate vertical guides
	childPrefix := d.nextPrefix(prefix, isLast, depth)
	return d.walkChildren(w, e.node, childPrefix, depth+1)
}

func (d *LSTDebugger[TKind]) walkChildren(
	w io.Writer,
	parent *SyntaxaLSTNode[TKind],
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

func (d *LSTDebugger[TKind]) writeNodeLine(
	w io.Writer,
	node *SyntaxaLSTNode[TKind],
) error {

	f := d.Formatter

	kind := f.FormatKind(node.kind)
	kind = f.applyColor(kind, f.ColorKind)

	if _, err := io.WriteString(w, kind); err != nil {
		return err
	}

	if f.ShowNodeID {
		if _, err := io.WriteString(w, fmt.Sprintf(" #%d", node.id)); err != nil {
			return err
		}
	}

	if f.ShowRevision {
		if _, err := io.WriteString(w, fmt.Sprintf(" r%d", node.revision)); err != nil {
			return err
		}
	}

	if f.ShowByteSpan && node.spanValid {
		s, e := node.Span()
		txt := fmt.Sprintf("[%d:%d]", s, e)
		txt = f.applyColor(txt, f.ColorSpan)
		if _, err := io.WriteString(w, " "+txt); err != nil {
			return err
		}
	}

	if f.ShowLineSpan && node.spanValid {
		sl, sc, el, ec, ok := LSTNodeLineSpanFromSource(node, f.LineSpanSource, f.LineSpanTabWidth)
		if !ok {
			sl, sc, el, ec = node.LineSpan()
		}
		txt := fmt.Sprintf("(%d:%d → %d:%d)", sl, sc, el, ec)
		txt = f.applyColor(txt, f.ColorSpan)
		if _, err := io.WriteString(w, " "+txt); err != nil {
			return err
		}
	}

	if f.ShowTokens && f.FormatToken != nil && len(node.tokens) > 0 {
		if _, err := io.WriteString(w, " {"); err != nil {
			return err
		}
		for i, t := range node.tokens {
			if i > 0 {
				if _, err := io.WriteString(w, ", "); err != nil {
					return err
				}
			}
			txt := f.FormatToken(t)
			txt = f.applyColor(txt, f.ColorToken)
			if _, err := io.WriteString(w, txt); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "}"); err != nil {
			return err
		}
	}

	if f.ShowAttributes && f.FormatAttribute != nil && len(node.attributes) > 0 {
		// Sort by key to make output stable.
		keys := make([]string, 0, len(node.attributes))
		for k := range node.attributes {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

		if _, err := io.WriteString(w, " <"); err != nil {
			return err
		}
		for i, k := range keys {
			if i > 0 {
				if _, err := io.WriteString(w, ", "); err != nil {
					return err
				}
			}
			txt := f.FormatAttribute(k, node.attributes[k])
			txt = f.applyColor(txt, f.ColorAttribute)
			if _, err := io.WriteString(w, txt); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, ">"); err != nil {
			return err
		}
	}

	_, err := io.WriteString(w, "\n")
	return err
}

// ============================================================
// LST API convenience (thin wrapper)
// ============================================================

func (n *SyntaxaLSTNode[TKind]) DebugDump(
	formatter LSTDebugFormatter[TKind],
) string {
	dbg := NewLSTDebugger[TKind](formatter)
	return dbg.DumpString(n)
}
