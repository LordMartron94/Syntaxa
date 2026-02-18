package syntaxa

import (
	"cmp"
	"fmt"
	"foundation/extensions"
	"lexarch"
	"strings"
)

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

/*
DebugDump returns a human-readable structural representation of the AST.

All semantic formatting is injected through ASTDebugFormatter to keep
Syntaxa independent of user enum meanings.

Output example:

Root
├─ Stmt
│  └─ BinaryExpr
│     ├─ CallExpr
│     │  └─ Ident(foo)
│     └─ BinaryExpr
│        ├─ Number(2)
│        └─ Number(3)
*/
func (n *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) DebugDump(
	formatter ASTDebugFormatter[TObs, TToken, TTokenRole, TKind],
) string {

	var out strings.Builder

	applyColor := func(s string, f func(string) string) string {
		if f != nil {
			return f(s)
		}
		return s
	}

	formatLine := func(node *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) {

		kind := formatter.FormatKind(node.kind)
		kind = applyColor(kind, formatter.ColorKind)
		out.WriteString(kind)

		if formatter.ShowNodeID {
			out.WriteString(fmt.Sprintf(" #%d", node.id))
		}

		if formatter.ShowRevision {
			out.WriteString(fmt.Sprintf(" r%d", node.revision))
		}

		if formatter.ShowByteSpan {
			s, e := node.Span()
			txt := fmt.Sprintf("[%d:%d]", s, e)
			txt = applyColor(txt, formatter.ColorSpan)
			out.WriteString(" " + txt)
		}

		if formatter.ShowLineSpan {
			sl, sc, el, ec := node.LineSpan()
			txt := fmt.Sprintf("(%d:%d → %d:%d)", sl, sc, el, ec)
			txt = applyColor(txt, formatter.ColorSpan)
			out.WriteString(" " + txt)
		}

		if formatter.ShowTokens && formatter.FormatToken != nil && len(node.tokens) > 0 {
			out.WriteString(" {")
			for i, t := range node.tokens {
				if i > 0 {
					out.WriteString(", ")
				}
				txt := formatter.FormatToken(t)
				txt = applyColor(txt, formatter.ColorToken)
				out.WriteString(txt)
			}
			out.WriteString("}")
		}

		if formatter.ShowAttributes && formatter.FormatAttribute != nil && len(node.attributes) > 0 {
			out.WriteString(" <")
			first := true

			attributes := extensions.MapSortFunc(node.attributes, func(pairA, pairB extensions.KeyValuePair[string, any]) int {
				return cmp.Compare(pairA.Key, pairB.Key)
			})

			for _, attribute := range attributes {
				if !first {
					out.WriteString(", ")
				}
				first = false
				txt := formatter.FormatAttribute(attribute.Key, attribute.Value)
				txt = applyColor(txt, formatter.ColorAttribute)
				out.WriteString(txt)
			}
			out.WriteString(">")
		}

		out.WriteByte('\n')
	}

	// ------------------------------------------------------------
	// Prefix helpers
	// ------------------------------------------------------------

	writePrefix := func(prefix string, isLast bool, depth int) {
		// Root itself is printed without tree glyphs.
		if depth == 0 {
			return
		}
		if isLast {
			out.WriteString(prefix + "└─ ")
		} else {
			out.WriteString(prefix + "├─ ")
		}
	}

	nextPrefix := func(prefix string, isLast bool, depth int) string {
		// After the root level, we maintain the vertical guides.
		// For the immediate children of root (depth==1), prefix is "".
		if depth == 0 {
			return ""
		}
		if isLast {
			return prefix + "   "
		}
		return prefix + "│  "
	}

	// ------------------------------------------------------------
	// Unified child enumeration (children + slots)
	// ------------------------------------------------------------

	type edge struct {
		isSlot bool
		name   string
		node   *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]
	}

	collectEdges := func(cur *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]) []edge {
		total := len(cur.children)
		if cur.slots != nil {
			total += len(cur.slots)
		}
		if total == 0 {
			return nil
		}

		edges := make([]edge, 0, total)

		for _, ch := range cur.children {
			edges = append(edges, edge{node: ch})
		}

		if cur.slots != nil {
			slots := extensions.MapSortFunc(cur.slots, func(pairA, pairB extensions.KeyValuePair[string, *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind]]) int {
				return cmp.Compare(pairA.Key, pairB.Key)
			})

			for _, slot := range slots {
				if slot.Value == nil {
					continue
				}
				edges = append(edges, edge{isSlot: true, name: slot.Key, node: slot.Value})
			}
		}

		return edges
	}

	// ------------------------------------------------------------
	// Recursive walker
	// ------------------------------------------------------------

	var walk func(
		node *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
		prefix string,
		isLast bool,
		depth int,
	)

	walk = func(
		node *SyntaxaASTNode[TObs, TToken, TTokenRole, TKind],
		prefix string,
		isLast bool,
		depth int,
	) {
		writePrefix(prefix, isLast, depth)
		formatLine(node)

		edges := collectEdges(node)
		if len(edges) == 0 {
			return
		}

		childPrefix := nextPrefix(prefix, isLast, depth)

		for i, e := range edges {
			last := i == len(edges)-1

			if !e.isSlot {
				walk(e.node, childPrefix, last, depth+1)
				continue
			}

			// Slot edge: render label + node line, then recurse into slot node's children.
			writePrefix(childPrefix, last, depth+1)

			label := formatter.SlotPrefix + e.name
			label = applyColor(label, formatter.ColorAttribute)
			out.WriteString(label + " → ")

			// Slot target printed on same line (no extra prefix)
			formatLine(e.node)

			// Recurse into the slot node's children with appropriate prefix.
			grand := collectEdges(e.node)
			if len(grand) == 0 {
				continue
			}

			grandPrefix := childPrefix
			if last {
				grandPrefix += "   "
			} else {
				grandPrefix += "│  "
			}

			for j, g := range grand {
				gLast := j == len(grand)-1
				if !g.isSlot {
					walk(g.node, grandPrefix, gLast, depth+2)
				} else {
					// nested slot-of-slot
					writePrefix(grandPrefix, gLast, depth+2)
					lbl := formatter.SlotPrefix + g.name
					lbl = applyColor(lbl, formatter.ColorAttribute)
					out.WriteString(lbl + " → ")
					formatLine(g.node)
				}
			}
		}
	}

	// ------------------------------------------------------------
	// Emit root + children
	// ------------------------------------------------------------

	formatLine(n)

	rootEdges := collectEdges(n)
	for i, e := range rootEdges {
		last := i == len(rootEdges)-1

		if !e.isSlot {
			// Root's children are depth==1 so they get connectors.
			walk(e.node, "", last, 1)
			continue
		}

		// Root slot
		writePrefix("", last, 1)

		label := formatter.SlotPrefix + e.name
		label = applyColor(label, formatter.ColorAttribute)
		out.WriteString(label + " → ")
		formatLine(e.node)
	}

	return out.String()
}
