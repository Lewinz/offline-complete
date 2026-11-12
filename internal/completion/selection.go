// Package completion contains local, in-memory completion coordination.
package completion

import (
	"errors"
	"strings"
	"sync"
)

// Guard gates a selection against caller-supplied session, focus and version
// state. The caller must supply trustworthy state and validate it again when
// applying an edit; this type neither authenticates peers nor inserts text.
type Guard struct {
	mu         sync.Mutex
	session    string
	version    uint64
	focused    bool
	closed     bool
	nextOffer  uint64
	offer      uint64
	candidates []string
}

func NewGuard(session string) *Guard { return &Guard{session: strings.Clone(session)} }

// Update invalidates every outstanding offer. Versions must increase for the
// lifetime of a guard; a rejected update leaves it unfocused until a newer one.
func (g *Guard) Update(version uint64, focused bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.offer, g.candidates = 0, nil
	if g.closed || version <= g.version {
		g.focused = false
		return
	}
	g.version, g.focused = version, focused
}

// SetFocused changes focus without advancing the editing version. Losing focus
// drops every outstanding offer; regaining focus at the same version permits a
// fresh offer of the same state version.
func (g *Guard) SetFocused(focused bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return
	}
	g.focused = focused
	if !focused {
		g.offer, g.candidates = 0, nil
	}
}

// Close permanently invalidates outstanding candidates when the session ends.
// It does not erase copies held by callers or guarantee memory erasure.
func (g *Guard) Close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed = true
	g.focused = false
	g.session = ""
	g.offer, g.candidates = 0, nil
}

// Offer replaces the previous offer for the supplied state version, copying its
// candidate values. A stale result cannot invalidate a newer offer.
func (g *Guard) Offer(version uint64, candidates []string) (uint64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if version != g.version {
		return 0, errors.New("candidate state is stale")
	}
	g.offer, g.candidates = 0, nil
	if g.session == "" || !g.focused || len(candidates) == 0 || len(candidates) > 256 || g.nextOffer == ^uint64(0) {
		return 0, errors.New("cannot offer candidates")
	}
	total := 0
	for _, candidate := range candidates {
		total += len(candidate)
		if total > 65536 {
			return 0, errors.New("candidate offer too large")
		}
	}
	g.nextOffer++
	g.offer = g.nextOffer
	g.candidates = make([]string, len(candidates))
	for i, candidate := range candidates {
		g.candidates[i] = strings.Clone(candidate)
	}
	return g.offer, nil
}

// Select accepts a single explicit index only while the offer is current.
func (g *Guard) Select(session string, version, offer uint64, index int) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.focused || session != g.session || version != g.version || offer == 0 || offer != g.offer || index < 0 || index >= len(g.candidates) {
		return "", false
	}
	selected := g.candidates[index]
	g.offer, g.candidates = 0, nil
	return selected, true
}
