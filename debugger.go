package syntaxa

import (
	"cmp"
	"fmt"
	"foundation/extensions"
	"io"
	"lexarch"
	"sort"
	"strings"
)

// ============================================================
// FORMATTER (semantic layer)
// ============================================================

type ASTDebugFormatter[TObs cmp.Ordered, TToken, TTokenRole, TKind comparable] struct {
	/* REQUIRED */
	FormatKind func(TKind) string

	/* Optional render hooks */
	FormatToken     func(lexarch.Lexeme[TObs, TToken, TTokenRole]) string
	FormatAttribute func(key string, value any) string

	/* Coloring layer (nil = no color) */
	ColorKind      func(string) string
	ColorToken     func(string) string
	ColorAttribute func(string) string
	ColorSpan      func(string) string

	/* Position rendering */
	ShowByteSpan bool
	ShowLineSpan bool

	/* Structural extras */
	ShowTokens     bool
	ShowAttributes bool
	ShowNodeID     bool
	ShowRevision   bool

	/* Slot styling */
	SlotPrefix string // e.g. "@", "#", "slot:"
}

func (f ASTDebugFormatter[TObs, TToken, TTokenRole, TKind]) validate() {
	if f.FormatKind == nil {
		panic("ASTDebugFormatter: FormatKind is required")
	}
	// SlotPrefix is optional; empty is allowed.
}

func (f ASTDebugFormatter[TObs, TToken, TTokenRole, TKind]) applyColor(s string, colorFn func(string) string) string {
	if colorFn == nil {
		return s
	}
	return colorFn(s)
}

// ============================================================
// ENUMERATION (structural layer)
// ============================================================

type astDebugEdge[TObs cmp.Ordered, TToken, TTokenRole, TKind comparable] struct {
	isSlot bool
	name   string
	node   *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]
}

type ASTEdgeEnumerator[TObs cmp.Ordered, TToken, TTokenRole, TKind comparable] interface {
	EdgesOf(node *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) []astDebugEdge[TObs, TToken, TTokenRole, TKind]
}

// Default behavior: children in their existing order, slots sorted by key.
type defaultASTEdgeEnumerator[TObs cmp.Ordered, TToken, TTokenRole, TKind comparable] struct{}

func (e defaultASTEdgeEnumerator[TObs, TToken, TTokenRole, TKind]) EdgesOf(
	cur *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
) []astDebugEdge[TObs, TToken, TTokenRole, TKind] {

	total := len(cur.children)
	if cur.slots != nil {
		total += len(cur.slots)
	}
	if total == 0 {
		return nil
	}

	out := make([]astDebugEdge[TObs, TToken, TTokenRole, TKind], 0, total)

	// Children (stable order as stored)
	for _, ch := range cur.children {
		out = append(out, astDebugEdge[TObs, TToken, TTokenRole, TKind]{node: ch})
	}

	// Slots (sorted by name)
	if cur.slots != nil {
		pairs := make([]extensions.KeyValuePair[string, *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]], 0, len(cur.slots))
		for k, v := range cur.slots {
			pairs = append(pairs, extensions.KeyValuePair[string, *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]]{Key: k, Value: v})
		}
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].Key < pairs[j].Key })

		for _, p := range pairs {
			if p.Value == nil {
				continue
			}
			out = append(out, astDebugEdge[TObs, TToken, TTokenRole, TKind]{
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

type ASTDebugger[TObs cmp.Ordered, TToken, TTokenRole, TKind comparable] struct {
	Formatter  ASTDebugFormatter[TObs, TToken, TTokenRole, TKind]
	Enumerator ASTEdgeEnumerator[TObs, TToken, TTokenRole, TKind]

	// Override if you want different glyphs later.
	GlyphMid   string // "├─ "
	GlyphLast  string // "└─ "
	GlyphVert  string // "│  "
	GlyphBlank string // "   "
}

func NewASTDebugger[TObs cmp.Ordered, TToken, TTokenRole, TKind comparable](
	formatter ASTDebugFormatter[TObs, TToken, TTokenRole, TKind],
) *ASTDebugger[TObs, TToken, TTokenRole, TKind] {

	formatter.validate()

	return &ASTDebugger[TObs, TToken, TTokenRole, TKind]{
		Formatter:  formatter,
		Enumerator: defaultASTEdgeEnumerator[TObs, TToken, TTokenRole, TKind]{},

		GlyphMid:   "├─ ",
		GlyphLast:  "└─ ",
		GlyphVert:  "│  ",
		GlyphBlank: "   ",
	}
}

func (d *ASTDebugger[TObs, TToken, TTokenRole, TKind]) DumpTo(
	w io.Writer,
	root *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
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

func (d *ASTDebugger[TObs, TToken, TTokenRole, TKind]) DumpString(
	root *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
) string {
	var b strings.Builder
	_ = d.DumpTo(&b, root)
	return b.String()
}

// ------------------------------------------------------------
// internal helpers
// ------------------------------------------------------------

func (d *ASTDebugger[TObs, TToken, TTokenRole, TKind]) edgesOf(
	n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
) []astDebugEdge[TObs, TToken, TTokenRole, TKind] {
	if d.Enumerator == nil {
		// Safe fallback
		return defaultASTEdgeEnumerator[TObs, TToken, TTokenRole, TKind]{}.EdgesOf(n)
	}
	return d.Enumerator.EdgesOf(n)
}

func (d *ASTDebugger[TObs, TToken, TTokenRole, TKind]) writePrefix(
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

func (d *ASTDebugger[TObs, TToken, TTokenRole, TKind]) nextPrefix(prefix string, isLast bool, depth int) string {
	if depth == 0 {
		return ""
	}
	if isLast {
		return prefix + d.GlyphBlank
	}
	return prefix + d.GlyphVert
}

func (d *ASTDebugger[TObs, TToken, TTokenRole, TKind]) walkEdge(
	w io.Writer,
	e astDebugEdge[TObs, TToken, TTokenRole, TKind],
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

func (d *ASTDebugger[TObs, TToken, TTokenRole, TKind]) walkChildren(
	w io.Writer,
	parent *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
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

func (d *ASTDebugger[TObs, TToken, TTokenRole, TKind]) writeNodeLine(
	w io.Writer,
	node *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
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

	if f.ShowByteSpan {
		s, e := node.Span()
		txt := fmt.Sprintf("[%d:%d]", s, e)
		txt = f.applyColor(txt, f.ColorSpan)
		if _, err := io.WriteString(w, " "+txt); err != nil {
			return err
		}
	}

	if f.ShowLineSpan {
		sl, sc, el, ec := node.LineSpan()
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
		sort.Slice(keys, func(i, j int) bool { return cmp.Compare(keys[i], keys[j]) < 0 })

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
// AST API convenience (thin wrapper)
// ============================================================

func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) DebugDump(
	formatter ASTDebugFormatter[TObs, TToken, TTokenRole, TKind],
) string {
	dbg := NewASTDebugger[TObs, TToken, TTokenRole, TKind](formatter)
	return dbg.DumpString(n)
}
