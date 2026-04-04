package syntaxa

/*
CloneLSTSubtreeDetached returns a deep copy of root with fresh IDs and parent links.
Used for macro-style template expansion where the tree is compiled without going through LSTEditor.
Diagnostic caches and revision are cleared on the copy.
*/
func CloneLSTSubtreeDetached[TNodeKind comparable](root *SyntaxaLSTNode[TNodeKind]) *SyntaxaLSTNode[TNodeKind] {
	if root == nil {
		return nil
	}
	var nextID uint64
	var clone func(*SyntaxaLSTNode[TNodeKind]) *SyntaxaLSTNode[TNodeKind]
	clone = func(n *SyntaxaLSTNode[TNodeKind]) *SyntaxaLSTNode[TNodeKind] {
		if n == nil {
			return nil
		}
		nextID++
		out := &SyntaxaLSTNode[TNodeKind]{}
		out.id = nextID
		out.kind = n.kind
		out.start = n.start
		out.end = n.end
		out.spanValid = n.spanValid
		if len(n.tokens) > 0 {
			out.tokens = append([]Lexeme(nil), n.tokens...)
		}
		if len(n.attributes) > 0 {
			out.attributes = make(map[string]any, len(n.attributes))
			for k, v := range n.attributes {
				out.attributes[k] = v
			}
		}
		if len(n.slots) > 0 {
			out.slots = make(map[string]*SyntaxaLSTNode[TNodeKind], len(n.slots))
			for k, v := range n.slots {
				c := clone(v)
				if c != nil {
					c.parent = out
				}
				out.slots[k] = c
			}
		}
		if len(n.children) > 0 {
			out.children = make([]*SyntaxaLSTNode[TNodeKind], len(n.children))
			for i, ch := range n.children {
				c := clone(ch)
				if c != nil {
					c.parent = out
				}
				out.children[i] = c
			}
		}
		return out
	}
	return clone(root)
}

/*
ReplaceChildInTree replaces oldChild with newChild in oldChild's parent (children or slots).
newChild must be detached (parent == nil). oldChild must have a non-nil parent.
Returns false if oldChild has no parent or is not found under parent.
*/
func ReplaceChildInTree[TNodeKind comparable](oldChild, newChild *SyntaxaLSTNode[TNodeKind]) bool {
	if oldChild == nil || newChild == nil || newChild.parent != nil {
		return false
	}
	parent := oldChild.parent
	if parent == nil {
		return false
	}
	for i, ch := range parent.children {
		if ch == oldChild {
			newChild.parent = parent
			oldChild.parent = nil
			parent.children[i] = newChild
			return true
		}
	}
	for k, v := range parent.slots {
		if v == oldChild {
			newChild.parent = parent
			oldChild.parent = nil
			parent.slots[k] = newChild
			return true
		}
	}
	return false
}
