// Package lowering provides functionality to lower a syntaxa grammar tree into other representations.
//
//   - ToPatternGrammar: converts the grammar tree (and rule map) into a pattern.Grammar (Contexta IR).
//     Returns the CFG plus ruleNameToNodeKey and ruleNameToRecovery. Use when you need the CFG
//     (e.g. for debug dumps or PDA compilation).
//   - GetAnalysis: computes nullable, first, and follow analysis from the package (via ToPatternGrammar
//     and pattern.ComputeAnalysis). Use when creating a parser or when dumping analysis.
//   - CompileEngine: compiles a GrammarPackage into a PDA (DPDA or NPDA). Uses ToPatternGrammar
//     internally; the package does not store CoreCFG.
//   - BuildStateGraph: produces a generic, editor-agnostic state graph (Contexts and Transitions:
//     Match, Push, Pop, Set) from a GrammarPackage. Each transition carries Token and NodeKind
//     (semantic identity). PopAmount is the number of grammar stack frames exited. Transition
//     order is discovery order; the editor backend sorts by lexer priority.
//     ZeroOrMore nests emit OpPush with a single target [bodyCtxID]; after the body closes via
//     OpPop the machine returns directly to the calling context, which already holds all loop
//     re-entry and outer follow-set transitions. Optional/mandatory nests emit OpSet with two
//     targets [afterCtxID, bodyCtxID]. For all-optional contexts, explicit OpPop transitions
//     are emitted for follow-set tokens (derived from the First set of remaining grammar rules
//     after the optional block); unrecognised tokens are handled by the editor backend's
//     invalid fallback (e.g. a \S catch-all).
package lowering
