package syntaxa

import "cmp"

/*
SyntaxError represents a single syntax error.
*/
type SyntaxError[TObservation cmp.Ordered] struct {
	ProducedByLexer bool

	Message string

	Line   int
	Column int

	AbsolutePosition int
	TokenNumber      int

	Expected [][]TObservation
	Found    *TObservation
}

/*
SyntaxErrors aggregates syntax errors produced during parsing.
*/
type SyntaxErrors[TObservation cmp.Ordered] struct {
	Errors []SyntaxError[TObservation]

	stack []errorFrame[TObservation]
}

type errorFrame[TObservation cmp.Ordered] struct {
	best                 *SyntaxError[TObservation]
	bestAbsolutePosition int
}

func SyntaxErrorsCreate[TObservation cmp.Ordered]() *SyntaxErrors[TObservation] {
	return &SyntaxErrors[TObservation]{
		Errors: make([]SyntaxError[TObservation], 0),
		stack:  make([]errorFrame[TObservation], 0),
	}
}

func (s *SyntaxErrors[_]) HasErrors() bool {
	return len(s.Errors) > 0
}

func (s *SyntaxErrors[TObservation]) PushFrame() {
	s.stack = append(s.stack, errorFrame[TObservation]{})
}

func (s *SyntaxErrors[TObservation]) PopFrame(commit bool) {
	if len(s.stack) == 0 {
		panic("SyntaxErrors: PopFrame without PushFrame")
	}

	top := s.stack[len(s.stack)-1]
	s.stack = s.stack[:len(s.stack)-1]

	if commit {
		if top.best != nil {
			s.commitCandidate(*top.best)
		}
		return
	}

	if top.best != nil {
		s.commitCandidate(*top.best)
	}
}

func (s *SyntaxErrors[TObservation]) commitCandidate(err SyntaxError[TObservation]) {
	if len(s.stack) > 0 {
		f := &s.stack[len(s.stack)-1]

		if betterError(f, err) {
			f.best = &err
			f.bestAbsolutePosition = err.AbsolutePosition
		}
		return
	}

	s.Errors = append(s.Errors, err)
}

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
