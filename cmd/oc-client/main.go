//go:build darwin

// Command oc-client is a minimal candidate interface for development. Run it
// in a separate terminal, then start the agent with OFFLINE_COMPLETE_RULES.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"offline-complete/internal/agent"
	"offline-complete/internal/ipc"
)

type incoming struct {
	Type    string       `json:"type"`
	Version uint64       `json:"version"`
	Offer   uint64       `json:"offer"`
	Items   []agent.Item `json:"items"`
	OK      bool         `json:"ok"`
	Error   string       `json:"error"`
}

type currentOffer struct {
	mu      sync.Mutex
	version uint64
	offer   uint64
	items   []agent.Item
}

func (c *currentOffer) set(message incoming) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.version = message.Version
	c.offer = message.Offer
	c.items = append(c.items[:0], message.Items...)
}

func (c *currentOffer) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.version, c.offer, c.items = 0, 0, nil
}

func (c *currentOffer) selection(index int) (uint64, uint64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.offer == 0 || index < 0 || index >= len(c.items) {
		return 0, 0, false
	}
	return c.version, c.offer, true
}

func main() {
	socket := flag.String("socket", "", "agent socket path")
	session := flag.String("session", "", "agent session ID")
	flag.Parse()
	if *socket == "" || *session == "" {
		fmt.Fprintln(os.Stderr, "usage: oc-client -socket PATH -session ID")
		os.Exit(2)
	}

	conn, err := ipc.DialAuthenticated(*socket, *session, 5*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "oc-client: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	var writeMu sync.Mutex
	send := func(message any) error {
		data, err := json.Marshal(message)
		if err != nil {
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_, err = fmt.Fprintf(conn, "%s\n", data)
		_ = conn.SetWriteDeadline(time.Time{})
		return err
	}
	if err := send(map[string]any{"type": "focus", "focused": true}); err != nil {
		fmt.Fprintf(os.Stderr, "oc-client: %v\n", err)
		os.Exit(1)
	}

	var offers currentOffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(conn)
		scanner.Buffer(make([]byte, 4096), agent.MaxMessage)
		for scanner.Scan() {
			var message incoming
			if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
				fmt.Fprintf(os.Stderr, "\nincoming message: %v\n", err)
				return
			}
			switch message.Type {
			case "offer":
				offers.set(message)
				fmt.Printf("\n[%d/%d]\n", message.Version, message.Offer)
				for index, item := range message.Items {
					if item.Description != "" {
						fmt.Printf("  %d  %s  %s\n", index, item.Name, item.Description)
					} else {
						fmt.Printf("  %d  %s\n", index, item.Name)
					}
				}
			case "clear":
				offers.clear()
				fmt.Printf("\n(no candidates at version %d)\n", message.Version)
			case "result":
				if message.OK {
					fmt.Println("\ninserted")
				} else {
					fmt.Printf("\nselection rejected: %s\n", message.Error)
				}
			}
		}
	}()

	fmt.Println("oc-client connected; commands: <index>, f (focus), b (blur), q")
	input := make(chan string)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			select {
			case input <- scanner.Text():
			case <-done:
				return
			}
		}
		close(input)
	}()

	for {
		select {
		case <-done:
			return
		case line, ok := <-input:
			if !ok {
				return
			}
			command := strings.TrimSpace(line)
			switch command {
			case "":
			case "q":
				_ = send(map[string]any{"type": "focus", "focused": false})
				return
			case "f":
				_ = send(map[string]any{"type": "focus", "focused": true})
			case "b":
				_ = send(map[string]any{"type": "focus", "focused": false})
			default:
				index, err := strconv.Atoi(command)
				if err != nil {
					fmt.Println("enter an index, f, b or q")
					continue
				}
				version, offer, ok := offers.selection(index)
				if !ok {
					fmt.Println("no current offer for that index")
					continue
				}
				if err := send(map[string]any{"type": "select", "version": version, "offer": offer, "index": index}); err != nil {
					fmt.Fprintf(os.Stderr, "oc-client: %v\n", err)
					return
				}
			}
		}
	}
}
