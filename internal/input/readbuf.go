package input

import "bytes"

// readBuffer keeps unconsumed terminal bytes contiguous.
type readBuffer struct {
	storage []byte
}

func (b *readBuffer) slice() []byte { return b.storage }

func (b *readBuffer) len() int { return len(b.storage) }

func (b *readBuffer) extend(data []byte) { b.storage = append(b.storage, data...) }

// advance consumes n bytes and moves the remainder to the front.
func (b *readBuffer) advance(n int) {
	if n <= 0 {
		return
	}
	if n >= len(b.storage) {
		clear(b.storage)
		b.storage = b.storage[:0]
		return
	}
	remainder := len(b.storage) - n
	copy(b.storage, b.storage[n:])
	clear(b.storage[remainder:])
	b.storage = b.storage[:remainder]
}

// findSubsequence returns the absolute index of needle at or after offset, or
// -1 when it is not present.
func (b *readBuffer) findSubsequence(offset int, needle []byte) int {
	if offset < 0 || offset > len(b.storage) {
		return -1
	}
	index := bytes.Index(b.storage[offset:], needle)
	if index < 0 {
		return -1
	}
	return index + offset
}
