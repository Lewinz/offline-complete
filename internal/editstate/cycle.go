package editstate

import (
	"errors"
	"math"
	"sync"
)

// CyclePublisher serializes events from an explicitly enabled editing source.
// The caller owns the publisher and must not write to it independently. Tokens
// only order events supplied by that source; they do not authenticate it.
// Methods can block up to the publisher's write deadline.
type CyclePublisher struct {
	gate      chan struct{}
	stop      chan struct{}
	stopOnce  sync.Once
	publisher *Publisher
	last      uint64
	active    uint64
	closed    bool
}

// NewCyclePublisher wraps an already authenticated publisher. A nil publisher
// cannot start a cycle. It does not acquire or authenticate a connection.
func NewCyclePublisher(publisher *Publisher) *CyclePublisher {
	c := &CyclePublisher{publisher: publisher, gate: make(chan struct{}, 1), stop: make(chan struct{})}
	c.gate <- struct{}{}
	return c
}

func (c *CyclePublisher) acquire() bool {
	var publisherClosed <-chan struct{}
	if c.publisher != nil {
		publisherClosed = c.publisher.closedCh
	}
	select {
	case <-c.stop:
		return false
	case <-publisherClosed:
		return false
	case <-c.gate:
	}
	select {
	case <-c.stop:
		c.gate <- struct{}{}
		return false
	case <-publisherClosed:
		c.gate <- struct{}{}
		return false
	default:
		return true
	}
}

// Begin invalidates any previous snapshot and starts a distinct editing cycle.
func (c *CyclePublisher) Begin() (uint64, error) {
	if !c.acquire() {
		return 0, errors.New("editing cycle publisher closed")
	}
	defer func() { c.gate <- struct{}{} }()
	if c.closed || c.publisher == nil {
		return 0, errors.New("editing cycle publisher closed")
	}
	if c.last == math.MaxUint64 {
		c.closed, c.active = true, 0
		return 0, errors.Join(errors.New("editing cycle exhausted"), c.publisher.Close())
	}
	if err := c.publisher.Clear(); err != nil {
		c.closed, c.active = true, 0
		return 0, err
	}
	c.last++
	c.active = c.last
	return c.active, nil
}

// Update publishes a complete ZLE snapshot only for the active cycle.
func (c *CyclePublisher) Update(cycle uint64, line string, cursor int, multibyte bool) error {
	if !c.acquire() {
		return errors.New("editing cycle is stale")
	}
	defer func() { c.gate <- struct{}{} }()
	if c.closed || cycle == 0 || cycle != c.active {
		return errors.New("editing cycle is stale")
	}
	if err := c.publisher.UpdateZsh(line, cursor, multibyte); err != nil {
		c.closed, c.active = true, 0
		return err
	}
	return nil
}

// Close ends the stream and interrupts an active send without waiting for the
// cycle lock first. Connection Close itself may block. Close is idempotent.
func (c *CyclePublisher) Close() error {
	c.stopOnce.Do(func() { close(c.stop) })
	if c.publisher != nil {
		return c.publisher.Close()
	}
	return nil
}

// End invalidates the active snapshot on execution or cancellation.
func (c *CyclePublisher) End(cycle uint64) error {
	if !c.acquire() {
		return errors.New("editing cycle is stale")
	}
	defer func() { c.gate <- struct{}{} }()
	if c.closed || cycle == 0 || cycle != c.active {
		return errors.New("editing cycle is stale")
	}
	c.active = 0
	if err := c.publisher.Clear(); err != nil {
		c.closed = true
		return err
	}
	return nil
}
