package completion

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"
)

func TestGuardDoesNotRetainLargerSessionBuffer(t *testing.T) {
	large := strings.Repeat("x", 4*1024*1024)
	session := large[1024:1032]
	g := NewGuard(session)
	g.Update(1, true)
	offer, err := g.Offer(1, []string{"choice"})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := g.Select(session, 1, offer, 0); !ok || got != "choice" {
		t.Fatalf("unexpected selection: %q, %v", got, ok)
	}
	if unsafe.StringData(g.session) == unsafe.StringData(session) {
		t.Fatal("guard retains caller's larger session buffer")
	}
}

func TestClosedGuardReleasesSessionIdentifier(t *testing.T) {
	g := NewGuard("session-a")
	g.Close()
	if g.session != "" {
		t.Fatal("closed guard retains session identifier")
	}
}

func TestOfferDoesNotRetainLargerCandidateBuffer(t *testing.T) {
	large := strings.Repeat("x", 4*1024*1024)
	candidate := large[1024:1032]
	g := NewGuard("session-a")
	g.Update(1, true)
	offer, err := g.Offer(1, []string{candidate})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := g.Select("session-a", 1, offer, 0)
	if !ok || got != "xxxxxxxx" {
		t.Fatalf("unexpected selection: %q, %v", got, ok)
	}
	if unsafe.StringData(got) == unsafe.StringData(candidate) {
		t.Fatal("selection retains caller's larger candidate buffer")
	}
}

func TestClosedGuardInvalidatesAndRejectsFutureOffers(t *testing.T) {
	g := NewGuard("session-a")
	g.Update(1, true)
	offer, err := g.Offer(1, []string{"private"})
	if err != nil {
		t.Fatal(err)
	}
	g.Close()
	g.Close()
	if _, ok := g.Select("session-a", 1, offer, 0); ok {
		t.Fatal("closed guard accepted old selection")
	}
	g.Update(2, true)
	if _, err := g.Offer(2, []string{"new"}); err == nil {
		t.Fatal("closed guard accepted a new offer")
	}
}

func TestStaleCandidateResultCannotReplaceCurrentOffer(t *testing.T) {
	g := NewGuard("session-a")
	g.Update(1, true)
	g.Update(2, true)
	current, err := g.Offer(2, []string{"current"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Offer(1, []string{"stale"}); err == nil {
		t.Fatal("accepted stale candidate result")
	}
	if got, ok := g.Select("session-a", 2, current, 0); !ok || got != "current" {
		t.Fatalf("current offer lost: %q, %v", got, ok)
	}
}

func TestSelectionRequiresCurrentFocusedSnapshotAndConsumesOffer(t *testing.T) {
	g := NewGuard("session-a")
	g.Update(1, true)
	offer, err := g.Offer(1, []string{"alpha", "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := g.Select("session-a", 1, offer, 1); !ok || got != "beta" {
		t.Fatalf("selection = %q, %v", got, ok)
	}
	if _, ok := g.Select("session-a", 1, offer, 0); ok {
		t.Fatal("offer accepted twice")
	}
}

func TestSelectionRejectsWrongSessionVersionFocusAndIndex(t *testing.T) {
	for _, tc := range []struct {
		name     string
		selectFn func(*Guard, uint64) bool
	}{
		{"wrong session", func(g *Guard, offer uint64) bool { _, ok := g.Select("session-b", 1, offer, 0); return ok }},
		{"wrong version", func(g *Guard, offer uint64) bool { _, ok := g.Select("session-a", 2, offer, 0); return ok }},
		{"invalid index", func(g *Guard, offer uint64) bool { _, ok := g.Select("session-a", 1, offer, 1); return ok }},
		{"focus lost", func(g *Guard, offer uint64) bool {
			g.Update(2, false)
			_, ok := g.Select("session-a", 1, offer, 0)
			return ok
		}},
		{"buffer changed", func(g *Guard, offer uint64) bool {
			g.Update(2, true)
			_, ok := g.Select("session-a", 1, offer, 0)
			return ok
		}},
		{"focus regained", func(g *Guard, offer uint64) bool {
			g.Update(2, false)
			g.Update(3, true)
			_, ok := g.Select("session-a", 1, offer, 0)
			return ok
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGuard("session-a")
			g.Update(1, true)
			offer, err := g.Offer(1, []string{"alpha"})
			if err != nil {
				t.Fatal(err)
			}
			if tc.selectFn(g, offer) {
				t.Fatal("invalid selection accepted")
			}
		})
	}
}

func TestNewOfferInvalidatesPreviousAndCopiesCandidates(t *testing.T) {
	g := NewGuard("session-a")
	g.Update(1, true)
	first, err := g.Offer(1, []string{"old"})
	if err != nil {
		t.Fatal(err)
	}
	items := []string{"new"}
	second, err := g.Offer(1, items)
	if err != nil {
		t.Fatal(err)
	}
	items[0] = "mutated"
	if _, ok := g.Select("session-a", 1, first, 0); ok {
		t.Fatal("old offer accepted")
	}
	if got, ok := g.Select("session-a", 1, second, 0); !ok || got != "new" {
		t.Fatalf("selection = %q, %v", got, ok)
	}
}

func TestOfferLimitsAndFailureInvalidatesPreviousOffer(t *testing.T) {
	for _, items := range [][]string{make([]string, 257), {strings.Repeat("x", 65537)}} {
		g := NewGuard("session-a")
		g.Update(1, true)
		previous, err := g.Offer(1, []string{"safe"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := g.Offer(1, items); err == nil {
			t.Fatal("oversized offer accepted")
		}
		if _, ok := g.Select("session-a", 1, previous, 0); ok {
			t.Fatal("failed replacement retained old offer")
		}
	}
}

func TestConcurrentSelectionAcceptsOnlyOnce(t *testing.T) {
	g := NewGuard("session-a")
	g.Update(1, true)
	offer, err := g.Offer(1, []string{"alpha"})
	if err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Go(func() {
			if _, ok := g.Select("session-a", 1, offer, 0); ok {
				accepted.Add(1)
			}
		})
	}
	wg.Wait()
	if got := accepted.Load(); got != 1 {
		t.Fatalf("accepted %d selections, want 1", got)
	}
}

func TestGuardRejectsInvalidStateAndNonIncreasingVersion(t *testing.T) {
	g := NewGuard("")
	g.Update(1, true)
	if _, err := g.Offer(1, []string{"a"}); err == nil {
		t.Fatal("empty session accepted")
	}
	g = NewGuard("session-a")
	if _, err := g.Offer(1, []string{"a"}); err == nil {
		t.Fatal("unfocused offer accepted")
	}
	g.Update(1, true)
	if _, err := g.Offer(1, nil); err == nil {
		t.Fatal("empty offer accepted")
	}
	offer, err := g.Offer(1, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	g.Update(1, true)
	if _, ok := g.Select("session-a", 1, offer, 0); ok {
		t.Fatal("repeated version retained offer")
	}
	if _, err := g.Offer(1, []string{"a"}); err == nil {
		t.Fatal("non-increasing update remained usable")
	}
}

func TestSetFocusedDropsAndRestoresOffersAtTheSameVersion(t *testing.T) {
	g := NewGuard("session-a")
	g.Update(1, true)
	offer, err := g.Offer(1, []string{"choice"})
	if err != nil {
		t.Fatal(err)
	}

	g.SetFocused(false)
	if _, ok := g.Select("session-a", 1, offer, 0); ok {
		t.Fatal("unfocused guard accepted a selection")
	}
	if _, err := g.Offer(1, []string{"choice"}); err == nil {
		t.Fatal("unfocused guard accepted an offer")
	}

	g.SetFocused(true)
	offer, err = g.Offer(1, []string{"choice"})
	if err != nil {
		t.Fatalf("refocused guard rejected the same version: %v", err)
	}
	if got, ok := g.Select("session-a", 1, offer, 0); !ok || got != "choice" {
		t.Fatalf("refocused selection = %q, %v", got, ok)
	}
}
