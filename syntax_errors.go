package syntaxa

import (
	"cmp"
)

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

type errorFrame[TObservation cmp.Ordered] struct {
	best                 SyntaxError[TObservation]
	hasBest              bool
	bestAbsolutePosition int

	// Tracks where this frame's committed errors begin in the flight buffer
	commitStartIndex int
}

type SyntaxErrors[TObservation cmp.Ordered] struct {
	Errors []SyntaxError[TObservation]
	stack  []errorFrame[TObservation]

	// A single flat buffer for all speculative errors across all active frames
	flight []SyntaxError[TObservation]
}

func SyntaxErrorsCreate[TObservation cmp.Ordered]() *SyntaxErrors[TObservation] {
	return &SyntaxErrors[TObservation]{
		Errors: make([]SyntaxError[TObservation], 0, 16),
		// Pre-allocate generous capacities to prevent resize allocations during parsing
		stack:  make([]errorFrame[TObservation], 0, 64),
		flight: make([]SyntaxError[TObservation], 0, 64),
	}
}

func (s *SyntaxErrors[_]) HasErrors() bool {
	return len(s.Errors) > 0
}

func (s *SyntaxErrors[TObservation]) pushFrame() {
	s.stack = append(s.stack, errorFrame[TObservation]{
		commitStartIndex: len(s.flight),
	})
}

func (s *SyntaxErrors[TObservation]) popFrame(commit bool) {
	if len(s.stack) == 0 {
		panic("SyntaxErrors: PopFrame without PushFrame")
	}

	topIndex := len(s.stack) - 1
	top := s.stack[topIndex]
	s.stack = s.stack[:topIndex]

	if !commit {
		// Rollback: Discard all errors added to the flight buffer during this frame
		s.flight = s.flight[:top.commitStartIndex]
		return
	}

	// Commit: Append this frame's best error (if any) to the flight buffer
	if top.hasBest {
		s.flight = append(s.flight, top.best)
	}

	// If this was the last frame on the stack, flush the entire flight buffer to actual Errors
	if len(s.stack) == 0 {
		s.Errors = append(s.Errors, s.flight...)
		s.flight = s.flight[:0] // Reset for future use
	}
	// If there are still frames on the stack, we do nothing. The flight buffer
	// naturally retains these errors for the parent frame to claim.
}

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
	f.best = err
	f.hasBest = true
	f.bestAbsolutePosition = err.AbsolutePosition
	return true
}

func (s *SyntaxErrors[TObservation]) currentBestPosition() (int, bool) {
	if len(s.stack) == 0 {
		return 0, false
	}
	top := s.stack[len(s.stack)-1]
	if !top.hasBest {
		return 0, false
	}
	return top.bestAbsolutePosition, true
}

func (s *SyntaxErrors[TObservation]) currentFrameWouldBeEmptyOnPop() bool {
	if len(s.stack) == 0 {
		return true
	}
	top := s.stack[len(s.stack)-1]
	// Empty if no best error, AND nothing was added to the flight buffer since push
	return !top.hasBest && len(s.flight) == top.commitStartIndex
}

func (s *SyntaxErrors[TObservation]) report(err SyntaxError[TObservation]) {
	if len(s.stack) == 0 {
		s.Errors = append(s.Errors, err)
		return
	}

	f := &s.stack[len(s.stack)-1]
	if betterError(f, err) {
		f.best = err
		f.hasBest = true
		f.bestAbsolutePosition = err.AbsolutePosition
	}
}

func betterError[TObservation cmp.Ordered](
	cur *errorFrame[TObservation],
	err SyntaxError[TObservation],
) bool {
	if !cur.hasBest {
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
