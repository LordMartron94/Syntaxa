// Package syntaxa provides a generic parsing engine. The engine is top-down recursive
// and highly flexible but not paradigm-agnostic: rule factories can target PEG, LL(k),
// Pratt, etc., but LR and other bottom-up paradigms are not supported.
package syntaxa
