//go:build darwin

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// ServeAuthenticated owns listener until it returns. It closes excess clients
// rather than queueing them in memory. Handshakes run concurrently and are
// bounded by timeout. The handler must return when its context is cancelled or
// its connection is closed; it must not retain the connection after returning.
// Cancellation closes the listener and all accepted connections before waiting
// for handlers. Rejected handshakes do not stop the server.
func ServeAuthenticated(ctx context.Context, listener *net.UnixListener, session string, timeout time.Duration, limit int, handler func(context.Context, *net.UnixConn)) error {
	if ctx == nil || listener == nil || session == "" || timeout <= 0 || limit <= 0 || handler == nil {
		return errors.New("invalid server parameters")
	}
	if err := validateSessionID(session); err != nil {
		return fmt.Errorf("invalid server session: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	var mu sync.Mutex
	active := make(map[*net.UnixConn]struct{})
	var workers sync.WaitGroup
	stopped := make(chan struct{})
	go func() {
		<-ctx.Done()
		_ = listener.Close()
		mu.Lock()
		for conn := range active {
			_ = conn.Close()
		}
		mu.Unlock()
		close(stopped)
	}()
	defer func() {
		cancel()
		<-stopped
		workers.Wait()
	}()

	// A per-listener burst budget bounds authentication work even when clients
	// disconnect quickly. Every accepted connection consumes a token.
	const burst = 32
	const refill = 100 * time.Millisecond
	tokens, last := burst, time.Now()
	for {
		conn, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		now := time.Now()
		elapsed := now.Sub(last)
		if elapsed >= burst*refill {
			tokens, last = burst, now
		} else if n := int(elapsed / refill); n > 0 {
			tokens += n
			if tokens > burst {
				tokens = burst
			}
			last = last.Add(time.Duration(n) * refill)
		}
		if tokens == 0 {
			_ = conn.Close()
			continue
		}
		tokens--
		mu.Lock()
		if ctx.Err() != nil || len(active) >= limit {
			mu.Unlock()
			_ = conn.Close()
			continue
		}
		active[conn] = struct{}{}
		workers.Add(1)
		mu.Unlock()
		go func() {
			defer workers.Done()
			defer func() {
				_ = conn.Close()
				mu.Lock()
				delete(active, conn)
				mu.Unlock()
			}()
			if err := AcceptHandshake(conn, session, timeout); err == nil && ctx.Err() == nil {
				handler(ctx, conn)
			}
		}()
	}
}
