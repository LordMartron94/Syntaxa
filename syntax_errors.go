package syntaxa

import (
	"cmp"
)

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

	committed []SyntaxError[TObservation]
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
	s.stack = append(s.stack, errorFrame[TObservation]{
		committed: make([]SyntaxError[TObservation], 0, 4),
	})
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

	if !commit {
		return
	}

	// Build the list of errors this frame contributes upward:
	// 1) all committed child errors
	// 2) plus this frame's own best error (if any)
	out := make([]SyntaxError[TObservation], 0, len(top.committed)+1)
	out = append(out, top.committed...)
	if top.best != nil {
		out = append(out, *top.best)
	}

	if len(out) == 0 {
		return
	}

	if len(s.stack) > 0 {
		parent := &s.stack[len(s.stack)-1]
		parent.committed = append(parent.committed, out...)
		return
	}

	s.Errors = append(s.Errors, out...)
}

/*
FlushFramesCommitAll pops all frames with commit true so that every error buffered in the
stack is merged into s.Errors.

Use when the top-level (program) rule fails: only one failure path runs (the program rule's),
so only one popFrame happens and descendant errors remain stuck in the stack. Flushing
ensures they are committed and visible to the caller.
*/
func (s *SyntaxErrors[TObservation]) FlushFramesCommitAll() {
	for len(s.stack) > 0 {
		s.popFrame(true)
	}
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
currentFrameWouldBeEmptyOnPop returns true when the top frame has no committed
errors and no best error, so it would contribute nothing when popped with commit.
Used to set a fallback error only on the innermost failing rule.
*/
func (s *SyntaxErrors[TObservation]) currentFrameWouldBeEmptyOnPop() bool {
	if len(s.stack) == 0 {
		return true
	}
	top := s.stack[len(s.stack)-1]
	return top.best == nil && len(top.committed) == 0
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
