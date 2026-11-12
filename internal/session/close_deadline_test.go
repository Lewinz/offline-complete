package session

import (
	"errors"
	"testing"
	"time"
)

func TestCloseResultReadyAtDeadline(t *testing.T) {
	want := errors.New("close failed")
	deadline := make(chan time.Time)
	close(deadline)
	for i := 0; i < 100; i++ {
		result := make(chan error, 1)
		result <- want
		got, finished := waitCloseResult(result, deadline)
		if !finished || !errors.Is(got, want) {
			t.Fatalf("ready close result lost: finished=%v, error=%v", finished, got)
		}
	}
}

func TestCloseResultTimesOutWhenStillPending(t *testing.T) {
	deadline := make(chan time.Time)
	close(deadline)
	if err, finished := waitCloseResult(make(chan error), deadline); finished || err != nil {
		t.Fatalf("pending close: finished=%v, error=%v", finished, err)
	}
}
