package agent_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"offline-complete/internal/agent"
	"offline-complete/internal/editstate"
)

type wireMessage struct {
	Type     string       `json:"type"`
	Version  uint64       `json:"version"`
	Offer    uint64       `json:"offer"`
	Selected int          `json:"selected"`
	Index    int          `json:"index"`
	Items    []agent.Item `json:"items"`
	OK       bool         `json:"ok"`
	Error    string       `json:"error"`
}

func writeNUL(t *testing.T, writer *io.PipeWriter, fields ...string) {
	t.Helper()
	if len(fields) == 5 {
		fields = append(fields, "")
	}
	var data []byte
	for _, field := range fields {
		data = append(data, field...)
		data = append(data, 0)
	}
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
}

func readWire(t *testing.T, reader *bufio.Reader) wireMessage {
	t.Helper()
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var message wireMessage
	if err := json.Unmarshal([]byte(line), &message); err != nil {
		t.Fatalf("decode %q: %v", line, err)
	}
	return message
}

func TestServicePushesOffersAppliesSelectionAndRefocuses(t *testing.T) {
	state := &editstate.State{}
	var shell bytes.Buffer
	completion, err := agent.NewSession("session-1", state, testCompleter(), &shell)
	if err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	defer client.Close()
	service, err := agent.NewService(completion, state, server)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()

	inputReader, inputWriter := io.Pipe()
	consumeDone := make(chan error, 1)
	go func() { consumeDone <- editstate.ConsumeNULNotify(inputReader, state, service.Refresh) }()
	serveDone := make(chan error, 1)
	go func() { serveDone <- service.Serve() }()

	reader := bufio.NewReader(client)
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))

	writeNUL(t, inputWriter, "begin", "1", "0", "1", "")
	if message := readWire(t, reader); message.Type != "clear" || message.Version != 1 {
		t.Fatalf("begin message = %+v", message)
	}

	writeNUL(t, inputWriter, "update", "2", "6", "1", "git co")
	offer := readWire(t, reader)
	if offer.Type != "offer" || offer.Version != 2 || offer.Offer == 0 || len(offer.Items) != 1 || offer.Items[0].Name != "commit" {
		t.Fatalf("offer = %+v", offer)
	}

	if _, err := fmt.Fprintf(client, `{"type":"focus","focused":false}`+"\n"); err != nil {
		t.Fatal(err)
	}
	if message := readWire(t, reader); message.Type != "clear" || message.Version != 2 {
		t.Fatalf("focus false = %+v", message)
	}
	if _, err := fmt.Fprintf(client, `{"type":"select","version":2,"offer":%d,"index":0}`+"\n", offer.Offer); err != nil {
		t.Fatal(err)
	}
	if message := readWire(t, reader); message.Type != "result" || message.OK {
		t.Fatalf("unfocused select = %+v", message)
	}
	if shell.Len() != 0 {
		t.Fatalf("unfocused selection wrote %q", shell.String())
	}

	if _, err := fmt.Fprintf(client, `{"type":"focus","focused":true}`+"\n"); err != nil {
		t.Fatal(err)
	}
	refocused := readWire(t, reader)
	if refocused.Type != "offer" || refocused.Version != 2 || refocused.Offer == 0 {
		t.Fatalf("refocused offer = %+v", refocused)
	}

	if _, err := fmt.Fprintf(client, `{"type":"select","version":2,"offer":%d,"index":0}`+"\n", refocused.Offer); err != nil {
		t.Fatal(err)
	}
	if message := readWire(t, reader); message.Type != "result" || !message.OK {
		t.Fatalf("selection result = %+v", message)
	}
	if !strings.HasPrefix(shell.String(), "\x05\x15git commit") {
		t.Fatalf("shell input = %q", shell.String())
	}

	writeNUL(t, inputWriter, "end", "3", "0", "1", "")
	if message := readWire(t, reader); message.Type != "clear" || message.Version != 3 {
		t.Fatalf("end message = %+v", message)
	}

	_ = inputWriter.Close()
	_ = service.Close()
	_ = client.Close()
	select {
	case <-consumeDone:
	case <-time.After(3 * time.Second):
		t.Fatal("consumer did not stop")
	}
	select {
	case <-serveDone:
	case <-time.After(3 * time.Second):
		t.Fatal("serve did not stop")
	}
}

func TestServiceRejectsMalformedCommands(t *testing.T) {
	for _, message := range []string{
		`{"type":"select","version":1,"offer":1}`,
		`{"type":"focus"}`,
		`{"type":"select","version":1,"offer":1,"index":0,"extra":true}`,
		`{"type":"unknown"}`,
	} {
		state := &editstate.State{}
		completion, err := agent.NewSession("session-1", state, testCompleter(), &bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		server, client := net.Pipe()
		service, err := agent.NewService(completion, state, server)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- service.Serve() }()
		if _, err := fmt.Fprintln(client, message); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err == nil {
				t.Fatalf("accepted malformed message %q", message)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("no error for %q", message)
		}
		_ = service.Close()
		_ = client.Close()
	}
}

func TestServiceDoesNotBlockOnStalledInterface(t *testing.T) {
	state := &editstate.State{}
	completion, err := agent.NewSession("session-1", state, testCompleter(), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	defer client.Close()
	service, err := agent.NewService(completion, state, server)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()

	inputReader, inputWriter := io.Pipe()
	go func() { _ = editstate.ConsumeNULNotify(inputReader, state, service.Refresh) }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 1; i <= 20; i++ {
			var data []byte
			for _, field := range []string{"update", fmt.Sprint(i), "6", "1", "git co", ""} {
				data = append(data, field...)
				data = append(data, 0)
			}
			if _, err := inputWriter.Write(data); err != nil {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("editing stream blocked on a stalled interface")
	}
	_ = inputWriter.Close()
}

func TestServiceKeyboardNavigationAcceptsSelection(t *testing.T) {
	state := &editstate.State{}
	if err := state.Update(1, "git c", 5); err != nil {
		t.Fatal(err)
	}
	var shell bytes.Buffer
	completion, err := agent.NewSession("session-1", state, twoCandidateCompleter(), &shell)
	if err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	defer client.Close()
	service, err := agent.NewService(completion, state, server)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	reader := bufio.NewReader(client)
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))

	service.Refresh()
	offer := readWire(t, reader)
	if offer.Type != "offer" || len(offer.Items) != 2 || offer.Selected != 0 {
		t.Fatalf("offer = %+v", offer)
	}
	if !service.Navigate(agent.NavigationNext) {
		t.Fatal("next was not consumed")
	}
	selection := readWire(t, reader)
	if selection.Type != "selection" || selection.Index != 1 {
		t.Fatalf("selection = %+v", selection)
	}
	if !service.Navigate(agent.NavigationAccept) {
		t.Fatal("accept was not consumed")
	}
	result := readWire(t, reader)
	if result.Type != "result" || !result.OK {
		t.Fatalf("result = %+v", result)
	}
	if !strings.HasPrefix(shell.String(), "\x05\x15git checkout") {
		t.Fatalf("shell = %q", shell.String())
	}
	if service.HasOffer() {
		t.Fatal("offer still active after accept")
	}
	if service.Navigate(agent.NavigationNext) {
		t.Fatal("navigation consumed without an offer")
	}

	if err := state.Update(2, "git checkout", 12); err != nil {
		t.Fatal(err)
	}
	service.Refresh()
	if message := readWire(t, reader); message.Type != "clear" {
		t.Fatalf("insertion redraw re-offered candidates: %+v", message)
	}
	time.Sleep(250 * time.Millisecond)
	if err := state.Update(3, "git c", 5); err != nil {
		t.Fatal(err)
	}
	service.Refresh()
	if message := readWire(t, reader); message.Type != "offer" {
		t.Fatalf("next edit did not offer candidates: %+v", message)
	}
}

func TestServiceExactSelectionExecutesTypedLine(t *testing.T) {
	state := &editstate.State{}
	if err := state.Update(1, "git commit", 10); err != nil {
		t.Fatal(err)
	}
	var shell bytes.Buffer
	completion, err := agent.NewSession("session-1", state, testCompleter(), &shell)
	if err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	defer client.Close()
	service, err := agent.NewService(completion, state, server)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	reader := bufio.NewReader(client)
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))

	service.Refresh()
	offer := readWire(t, reader)
	if offer.Type != "offer" || len(offer.Items) != 1 || offer.Items[0].Name != "commit" {
		t.Fatalf("offer = %+v", offer)
	}
	if service.Navigate(agent.NavigationAccept) {
		t.Fatal("exact selection consumed Enter instead of executing the line")
	}
	if shell.Len() != 0 {
		t.Fatalf("exact selection replaced the buffer: %q", shell.String())
	}
	if service.HasOffer() {
		t.Fatal("offer still active after exact execute")
	}
	if message := readWire(t, reader); message.Type != "clear" {
		t.Fatalf("message = %+v", message)
	}
}

func TestServiceExactExecuteCanBeDisabled(t *testing.T) {
	state := &editstate.State{}
	if err := state.Update(1, "git commit", 10); err != nil {
		t.Fatal(err)
	}
	var shell bytes.Buffer
	completion, err := agent.NewSession("session-1", state, testCompleter(), &shell)
	if err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	defer client.Close()
	service, err := agent.NewService(completion, state, server)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	service.SetExactMatchExecute(false)
	service.Refresh()
	if service.Navigate(agent.NavigationAccept) != true {
		t.Fatal("exact selection was not consumed")
	}
	if !strings.HasPrefix(shell.String(), "\x05\x15git commit") {
		t.Fatalf("shell = %q", shell.String())
	}
}

func TestServiceKeepsSelectionAcrossUnchangedRedraw(t *testing.T) {
	state := &editstate.State{}
	if err := state.Update(1, "git c", 5); err != nil {
		t.Fatal(err)
	}
	completion, err := agent.NewSession("session-1", state, twoCandidateCompleter(), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	defer client.Close()
	service, err := agent.NewService(completion, state, server)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	reader := bufio.NewReader(client)
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))

	service.Refresh()
	if message := readWire(t, reader); message.Type != "offer" {
		t.Fatalf("offer = %+v", message)
	}
	if !service.Navigate(agent.NavigationNext) {
		t.Fatal("next was not consumed")
	}
	if message := readWire(t, reader); message.Type != "selection" || message.Index != 1 {
		t.Fatalf("selection = %+v", message)
	}

	if err := state.Update(2, "git c", 5); err != nil {
		t.Fatal(err)
	}
	service.Refresh()
	_ = client.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("unchanged redraw sent a message")
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))

	if !service.Navigate(agent.NavigationAccept) {
		t.Fatal("accept after unchanged redraw was not consumed")
	}
	if message := readWire(t, reader); message.Type != "result" || !message.OK {
		t.Fatalf("result = %+v", message)
	}
}

func TestServiceDismissHidesTheSameVersion(t *testing.T) {
	state := &editstate.State{}
	if err := state.Update(1, "git c", 5); err != nil {
		t.Fatal(err)
	}
	completion, err := agent.NewSession("session-1", state, twoCandidateCompleter(), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	defer client.Close()
	service, err := agent.NewService(completion, state, server)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	reader := bufio.NewReader(client)
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))

	service.Refresh()
	if message := readWire(t, reader); message.Type != "offer" {
		t.Fatalf("offer = %+v", message)
	}
	if !service.Navigate(agent.NavigationDismiss) {
		t.Fatal("dismiss was not consumed")
	}
	if message := readWire(t, reader); message.Type != "clear" {
		t.Fatalf("clear = %+v", message)
	}
	service.Refresh()
	if service.HasOffer() {
		t.Fatal("dismissed offer became active again")
	}
}
