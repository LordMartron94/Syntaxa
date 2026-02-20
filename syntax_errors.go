package syntaxa

import "cmp"

/*
SyntaxError represents a single syntax error produced during parsing or lexing.
*/
type SyntaxError[TObservation cmp.Ordered] struct {
	ProducedByLexer bool

	Rule    string
	Message string

	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int

	AbsolutePosition int
	TokenNumber      int

	Expected [][]TObservation
	Found    *TObservation
}

/*
SyntaxErrors aggregates syntax errors using transactional frames.

Errors are collected speculatively during rule execution and are only
committed when the grammar commits a branch. Failed speculative paths
discard their diagnostics automatically.

This mirrors transactional parsing state and prevents ghost errors
from optional rules, backtracking, and ordered choice.
*/
type SyntaxErrors[TObservation cmp.Ordered] struct {
	Errors []SyntaxError[TObservation]
	stack  []errorFrame[TObservation]
}

type errorFrame[TObservation cmp.Ordered] struct {
	best                 *SyntaxError[TObservation]
	bestAbsolutePosition int
}

/*
SyntaxErrorsCreate constructs a new error collector.
*/
func SyntaxErrorsCreate[TObservation cmp.Ordered]() *SyntaxErrors[TObservation] {
	return &SyntaxErrors[TObservation]{
		Errors: make([]SyntaxError[TObservation], 0),
		stack:  make([]errorFrame[TObservation], 0),
	}
}

func (s *SyntaxErrors[_]) HasErrors() bool {
	return len(s.Errors) > 0
}

/*
pushFrame begins a speculative error scope.

All reported errors are buffered until the frame is either committed
or discarded.
*/
func (s *SyntaxErrors[TObservation]) pushFrame() {
	s.stack = append(s.stack, errorFrame[TObservation]{})
}

/*
popFrame closes the current error scope.

If commit is true, the best error from the frame is committed upward.
If commit is false, all buffered errors are discarded.
*/
func (s *SyntaxErrors[TObservation]) popFrame(commit bool) {
	if len(s.stack) == 0 {
		panic("SyntaxErrors: PopFrame without PushFrame")
	}

	top := s.stack[len(s.stack)-1]
	s.stack = s.stack[:len(s.stack)-1]

	if !commit || top.best == nil {
		return
	}

	if len(s.stack) > 0 {
		parent := &s.stack[len(s.stack)-1]
		if betterError(parent, *top.best) {
			parent.best = top.best
			parent.bestAbsolutePosition = top.best.AbsolutePosition
		}
		return
	}

	s.Errors = append(s.Errors, *top.best)
}

func (s *SyntaxErrors[TObservation]) replaceBest(err SyntaxError[TObservation]) bool {
	if len(s.stack) == 0 {
		return false
	}
	f := &s.stack[len(s.stack)-1]
	f.best = &err
	f.bestAbsolutePosition = err.AbsolutePosition
	return true
}

func (s *SyntaxErrors[TObservation]) currentBestPosition() (int, bool) {
	if len(s.stack) == 0 {
		return 0, false
	}
	top := s.stack[len(s.stack)-1]
	if top.best == nil {
		return 0, false
	}
	return top.best.AbsolutePosition, true
}

/*
report records a candidate syntax error in the current frame.

The most relevant error (furthest progress, lexer priority) is retained.
*/
func (s *SyntaxErrors[TObservation]) report(err SyntaxError[TObservation]) {
	if len(s.stack) == 0 {
		s.Errors = append(s.Errors, err)
		return
	}

	f := &s.stack[len(s.stack)-1]

	if betterError(f, err) {
		f.best = &err
		f.bestAbsolutePosition = err.AbsolutePosition
	}
}

func betterError[TObservation cmp.Ordered](
	cur *errorFrame[TObservation],
	err SyntaxError[TObservation],
) bool {
	if cur.best == nil {
		return true
	}

	if err.AbsolutePosition > cur.bestAbsolutePosition {
		return true
	}

	if err.AbsolutePosition == cur.bestAbsolutePosition &&
		cur.best.ProducedByLexer &&
		!err.ProducedByLexer {
		return true
	}

	return false
}
