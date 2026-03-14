package lowering

import (
	"fmt"
	"sort"
	"strings"
	"syntaxa"
)

func lookaheadKey[TToken, TNodeKind comparable](
	terms []gTerminal[TToken, TNodeKind],
	tokenFmt func(TToken) string,
) string {
	keys := make([]string, len(terms))
	for i, t := range terms {
		keys[i] = terminalKey(t, tokenFmt)
	}
	sort.Strings(keys)
	return strings.Join(keys, "|")
}

func terminalKey[TToken, TNodeKind comparable](
	t gTerminal[TToken, TNodeKind],
	tokenFmt func(TToken) string,
) string {
	var sb strings.Builder
	sb.WriteString(tokenFmt(t.token))
	if t.nestNode != nil {
		sb.WriteString(":NEST(")
		sb.WriteString(string(t.nestNode.GrammarLabel))
		sb.WriteString(")")
	}
	sb.WriteString(":(")
	sb.WriteString(grammarNodesKey(t.remaining, tokenFmt))
	sb.WriteString(")")

	seenLabels := make(map[syntaxa.GrammarLabel]bool)
	seenReps := make(map[*syntaxa.Grammar[TToken, TNodeKind]]bool)

	for _, e := range t.stack {
		if e.isRepetition {
			if seenReps[e.repeatNode] {
				break
			}
			seenReps[e.repeatNode] = true
			sb.WriteString(":R[")
			sb.WriteString(grammarNodesKey(e.remaining, tokenFmt))
			sb.WriteString("]")
		} else {
			if e.label != "" && seenLabels[e.label] {
				break
			}
			if e.label != "" {
				seenLabels[e.label] = true
			}
			sb.WriteString(":V(")
			sb.WriteString(string(e.label))
			sb.WriteString(",")
			sb.WriteString(grammarNodesKey(e.remaining, tokenFmt))
			sb.WriteString(")")
		}
	}
	if t.popOffset > 0 {
		fmt.Fprintf(&sb, ":PO%d", t.popOffset)
	}
	return sb.String()
}

func grammarNodesKey[TToken, TNodeKind comparable](
	nodes []*syntaxa.Grammar[TToken, TNodeKind],
	tokenFmt func(TToken) string,
) string {
	parts := make([]string, len(nodes))
	for i, n := range nodes {
		parts[i] = grammarNodeKey(n, tokenFmt)
	}
	return strings.Join(parts, ",")
}

func grammarNodeKey[TToken, TNodeKind comparable](
	n *syntaxa.Grammar[TToken, TNodeKind],
	tokenFmt func(TToken) string,
) string {
	if n == nil {
		return "nil"
	}
	if n.NodePath != nil {
		return string(*n.NodePath)
	}
	if n.Kind == syntaxa.GToken {
		return "ST:" + tokenFmt(n.Token)
	}
	return string(n.GrammarLabel)
}

func owningRule[TToken, TNodeKind comparable](term gTerminal[TToken, TNodeKind]) syntaxa.GrammarLabel {
	for i := 0; i < len(term.stack); i++ {
		if !term.stack[i].isRepetition && term.stack[i].label != "" {
			return term.stack[i].label
		}
	}
	for i := 0; i < len(term.stack); i++ {
		if term.stack[i].isRepetition && term.stack[i].label != "" {
			return term.stack[i].label
		}
	}
	return "anon"
}

func outerFrameKind[TToken, TNodeKind comparable](term gTerminal[TToken, TNodeKind]) (zeroOrMore bool, optional bool) {
	if len(term.stack) == 0 {
		return false, false
	}
	outer := term.stack[len(term.stack)-1]
	if outer.repeatNode == nil {
		return false, false
	}
	if outer.repeatNode.Kind == syntaxa.GRepeat {
		return true, false
	}
	if outer.repeatNode.Kind == syntaxa.GOptional {
		return false, true
	}
	return false, false
}

func allOptionalTerminals[TToken, TNodeKind comparable](terminals []gTerminal[TToken, TNodeKind]) bool {
	if len(terminals) == 0 {
		return false
	}
	for _, t := range terminals {
		_, isOpt := outerFrameKind(t)
		if !isOpt {
			return false
		}
	}
	return true
}
