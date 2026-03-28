package syntaxa

/*
ParseStreamStats counts raw token-stream operations at the lexer boundary.

RawConsumeCalls increments once per underlying consumeRaw invocation (including those
later undone by parser restore/backtrack). RawPeekCalls increments once per peekRaw.
*/
type ParseStreamStats struct {
	RawConsumeCalls uint64
	RawPeekCalls    uint64
}
