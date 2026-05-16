package syntaxa

type SyntaxError struct {
	ProducedByLexer bool

	Rule    string
	Message string

	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int

	AbsolutePosition int
	AbsoluteEnd      int
	TokenNumber      int

	Expected [][]rune
	Found    *rune
}

type errorFrame struct {
	best                 SyntaxError
	hasBest              bool
	bestAbsolutePosition int

	// Tracks where this frame's committed errors begin in the flight buffer
	commitStartIndex int
}

type delimiterBoundary struct {
	regionStart  int
	flightAtOpen int
}

type SyntaxErrors struct {
	Errors []SyntaxError
	stack  []errorFrame

	// A single flat buffer for all speculative errors across all active frames
	flight []SyntaxError

	delimiterBoundaries     []delimiterBoundary
	entryRuleScopeDepth     int
	entryRuleFlightBaseline int
}

func SyntaxErrorsCreate() *SyntaxErrors {
	return &SyntaxErrors{
		Errors: make([]SyntaxError, 0, 16),
		// Pre-allocate generous capacities to prevent resize allocations during parsing
		stack:  make([]errorFrame, 0, 64),
		flight: make([]SyntaxError, 0, 64),
	}
}

func (s *SyntaxErrors) HasErrors() bool {
	return len(s.Errors) > 0
}

func (s *SyntaxErrors) pushFrame() {
	s.stack = append(s.stack, errorFrame{
		commitStartIndex: len(s.flight),
	})
}

func (s *SyntaxErrors) popFrame(commit bool) {
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

func (s *SyntaxErrors) FlushFramesCommitAll() {
	for len(s.stack) > 0 {
		s.popFrame(true)
	}
}

func (s *SyntaxErrors) replaceBest(err SyntaxError) bool {
	if len(s.stack) == 0 {
		return false
	}
	f := &s.stack[len(s.stack)-1]
	f.best = err
	f.hasBest = true
	f.bestAbsolutePosition = err.AbsolutePosition
	return true
}

func (s *SyntaxErrors) currentBestPosition() (int, bool) {
	if len(s.stack) == 0 {
		return 0, false
	}
	top := s.stack[len(s.stack)-1]
	if !top.hasBest {
		return 0, false
	}
	return top.bestAbsolutePosition, true
}

func (s *SyntaxErrors) currentFrameWouldBeEmptyOnPop() bool {
	if len(s.stack) == 0 {
		return true
	}
	top := s.stack[len(s.stack)-1]
	// Empty if no best error, AND nothing was added to the flight buffer since push
	return !top.hasBest && len(s.flight) == top.commitStartIndex
}

func (s *SyntaxErrors) report(err SyntaxError) {
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

/*
PushDelimiterBoundary marks the start of a nest/open-delimited region.

Completion diagnostics for the matching close must not be reported once a syntax
error has already been committed inside this region.
*/
func (s *SyntaxErrors) PushDelimiterBoundary(regionStart int) {
	s.delimiterBoundaries = append(s.delimiterBoundaries, delimiterBoundary{
		regionStart:  regionStart,
		flightAtOpen: len(s.flight),
	})
}

func (s *SyntaxErrors) PopDelimiterBoundary() {
	if len(s.delimiterBoundaries) == 0 {
		panic("SyntaxErrors: PopDelimiterBoundary without PushDelimiterBoundary")
	}
	s.delimiterBoundaries = s.delimiterBoundaries[:len(s.delimiterBoundaries)-1]
}

func (s *SyntaxErrors) HasCommittedErrorInsideCurrentDelimiterBoundary() bool {
	if len(s.delimiterBoundaries) == 0 {
		return false
	}

	boundary := s.delimiterBoundaries[len(s.delimiterBoundaries)-1]
	for i := boundary.flightAtOpen; i < len(s.flight); i++ {
		if s.flight[i].AbsolutePosition >= boundary.regionStart {
			return true
		}
	}
	for i := range s.stack {
		if s.stack[i].hasBest && s.stack[i].bestAbsolutePosition >= boundary.regionStart {
			return true
		}
	}
	return false
}

func (s *SyntaxErrors) BeginEntryRuleScope() {
	if s.entryRuleScopeDepth == 0 {
		s.entryRuleFlightBaseline = len(s.flight)
	}
	s.entryRuleScopeDepth++
}

func (s *SyntaxErrors) EndEntryRuleScope() {
	if s.entryRuleScopeDepth == 0 {
		panic("SyntaxErrors: EndEntryRuleScope without BeginEntryRuleScope")
	}
	s.entryRuleScopeDepth--
}

func (s *SyntaxErrors) HasCommittedErrorSinceEntryRuleScope() bool {
	return len(s.flight) > s.entryRuleFlightBaseline
}

func (s *SyntaxErrors) HasCommittedSyntaxErrors() bool {
	if len(s.Errors) > 0 || len(s.flight) > 0 {
		return true
	}
	for i := range s.stack {
		if s.stack[i].hasBest {
			return true
		}
	}
	return false
}

func betterError(
	cur *errorFrame,
	err SyntaxError,
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
