package input

// keyMap is a byte trie with prefix-aware lookup. Terminal sequences overlap,
// so a lookup reports how confident the match is instead of a plain hit.
type keyMap struct {
	root node
}

type node struct {
	children map[byte]*node
	hasValue bool
	value    InputEvent
}

type foundKind uint8

const (
	foundNone foundKind = iota
	foundExact
	foundAmbiguous
	foundNeedData
)

type found struct {
	kind   foundKind
	length int
	value  InputEvent
}

func newKeyMap() *keyMap { return &keyMap{} }

func (m *keyMap) insert(key []byte, value InputEvent) {
	current := &m.root
	for _, b := range key {
		if current.children == nil {
			current.children = make(map[byte]*node)
		}
		child := current.children[b]
		if child == nil {
			child = &node{}
			current.children[b] = child
		}
		current = child
	}
	current.hasValue = true
	current.value = value
}

func (m *keyMap) lookup(key []byte) found {
	return lookupNode(&m.root, key, 0)
}

func lookupNode(current *node, key []byte, depth int) found {
	if len(key) == 0 {
		if len(current.children) == 0 {
			if current.hasValue {
				return found{kind: foundExact, length: depth, value: current.value}
			}
			return found{kind: foundNeedData}
		}
		if current.hasValue {
			return found{kind: foundAmbiguous, length: depth, value: current.value}
		}
		return found{kind: foundNeedData}
	}

	child := current.children[key[0]]
	if child == nil {
		if depth == 0 {
			return found{kind: foundNone}
		}
		if current.hasValue {
			return found{kind: foundExact, length: depth, value: current.value}
		}
		return found{kind: foundNeedData}
	}

	result := lookupNode(child, key[1:], depth+1)
	if result.kind == foundNeedData {
		if current.hasValue {
			return found{kind: foundAmbiguous, length: depth, value: current.value}
		}
		return found{kind: foundNeedData}
	}
	return result
}
