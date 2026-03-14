package lowering

import (
	"foundation/hash"
	"sort"
	"syntaxa"
	"unsafe"
)

func lookaheadKey[TToken, TNodeKind comparable](
	terms []gTerminal[TToken, TNodeKind],
	tokenHash func(TToken) uint64,
	hasher *hash.XXH3Hasher,
) uint64 {
	if len(terms) == 0 {
		return 0
	}

	keys := make([]uint64, len(terms))
	for i, t := range terms {
		keys[i] = terminalKey(t, tokenHash, hasher)
	}

	sort.Slice(keys, func(i, j int) bool {
		return keys[i] < keys[j]
	})

	byteLen := len(keys) * 8
	byteData := unsafe.Slice((*byte)(unsafe.Pointer(&keys[0])), byteLen)

	return hash.XXH3HasherHash64(hasher, byteData)
}

func terminalKey[TToken, TNodeKind comparable](
	t gTerminal[TToken, TNodeKind],
	tokenHash func(TToken) uint64,
	hasher *hash.XXH3Hasher,
) uint64 {
	var buf [128]uint64
	b := buf[:0]

	b = append(b, tokenHash(t.token))
	b = appendNestMarker(b, t.nestNode, hasher)
	b = appendRemaining(b, t.remaining, tokenHash, hasher)
	b = appendStackData(b, t.stack, tokenHash, hasher)
	b = append(b, uint64(t.popOffset))

	byteLen := len(b) * 8
	byteData := unsafe.Slice((*byte)(unsafe.Pointer(&b[0])), byteLen)

	return hash.XXH3HasherHash64(hasher, byteData)
}

func appendNestMarker[TToken, TNodeKind comparable](
	b []uint64,
	nestNode *syntaxa.Grammar[TToken, TNodeKind],
	hasher *hash.XXH3Hasher,
) []uint64 {
	if nestNode == nil {
		return append(b, 0)
	}
	b = append(b, 1)
	return append(b, strHash(string(nestNode.GrammarLabel), hasher))
}

func appendRemaining[TToken, TNodeKind comparable](
	b []uint64,
	remaining []*syntaxa.Grammar[TToken, TNodeKind],
	tokenHash func(TToken) uint64,
	hasher *hash.XXH3Hasher,
) []uint64 {
	b = append(b, uint64(len(remaining)))
	for _, n := range remaining {
		b = append(b, grammarNodeKey(n, tokenHash, hasher))
	}
	return b
}

func appendStackData[TToken, TNodeKind comparable](
	b []uint64,
	stack []gStackEntry[TToken, TNodeKind],
	tokenHash func(TToken) uint64,
	hasher *hash.XXH3Hasher,
) []uint64 {
	var seenLabels [16]syntaxa.GrammarLabel
	var seenReps [16]*syntaxa.Grammar[TToken, TNodeKind]
	labelsCount, repsCount := 0, 0

	b = append(b, uint64(len(stack)))
	for _, e := range stack {
		var stop bool
		b, labelsCount, repsCount, stop = processStackEntry(
			b, e, seenLabels[:], labelsCount, seenReps[:], repsCount, tokenHash, hasher,
		)
		if stop {
			break
		}
	}
	return b
}

func processStackEntry[TToken, TNodeKind comparable](
	b []uint64,
	e gStackEntry[TToken, TNodeKind],
	seenLabels []syntaxa.GrammarLabel, labelsCount int,
	seenReps []*syntaxa.Grammar[TToken, TNodeKind], repsCount int,
	tokenHash func(TToken) uint64,
	hasher *hash.XXH3Hasher,
) ([]uint64, int, int, bool) {
	if e.isRepetition {
		if containsRep(seenReps[:repsCount], e.repeatNode) {
			return b, labelsCount, repsCount, true
		}
		repsCount = trackRep(seenReps, repsCount, e.repeatNode)
		b = append(b, 2)
		b = appendRemaining(b, e.remaining, tokenHash, hasher)

		return b, labelsCount, repsCount, false
	}

	if e.label != "" && containsLabel(seenLabels[:labelsCount], e.label) {
		return b, labelsCount, repsCount, true
	}
	labelsCount = trackLabel(seenLabels, labelsCount, e.label)
	b = append(b, 3)
	b = append(b, strHash(string(e.label), hasher))
	b = appendRemaining(b, e.remaining, tokenHash, hasher)

	return b, labelsCount, repsCount, false
}

func containsLabel(list []syntaxa.GrammarLabel, label syntaxa.GrammarLabel) bool {
	for _, l := range list {
		if l == label {
			return true
		}
	}
	return false
}

func trackLabel(seen []syntaxa.GrammarLabel, count int, label syntaxa.GrammarLabel) int {
	if label != "" && count < len(seen) {
		seen[count] = label
		return count + 1
	}
	return count
}

func containsRep[TToken, TNodeKind comparable](
	list []*syntaxa.Grammar[TToken, TNodeKind],
	node *syntaxa.Grammar[TToken, TNodeKind],
) bool {
	for _, n := range list {
		if n == node {
			return true
		}
	}
	return false
}

func trackRep[TToken, TNodeKind comparable](
	seen []*syntaxa.Grammar[TToken, TNodeKind],
	count int,
	node *syntaxa.Grammar[TToken, TNodeKind],
) int {
	if count < len(seen) {
		seen[count] = node
		return count + 1
	}
	return count
}

func grammarNodeKey[TToken, TNodeKind comparable](
	n *syntaxa.Grammar[TToken, TNodeKind],
	tokenHash func(TToken) uint64,
	hasher *hash.XXH3Hasher,
) uint64 {
	if n == nil {
		return 0
	}
	if n.NodePath != nil {
		return strHash(string(*n.NodePath), hasher)
	}
	if n.Kind == syntaxa.GToken {
		return tokenHash(n.Token)
	}
	return strHash(string(n.GrammarLabel), hasher)
}

func strHash(s string, hasher *hash.XXH3Hasher) uint64 {
	if len(s) == 0 {
		return 0
	}
	b := unsafe.Slice(unsafe.StringData(s), len(s))
	return hash.XXH3HasherHash64(hasher, b)
}
