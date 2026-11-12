// Package ipc implements the bounded handshake frame for the local session
// socket. Transport authentication is separate from framing.
package ipc

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"offline-complete/internal/strictjson"
)

// MaxFrame is the largest accepted JSON payload, excluding the length prefix.
const MaxFrame = 64 * 1024

// Handshake is the first message on a session connection.
type Handshake struct {
	Version   int    `json:"version"`
	SessionID string `json:"session_id"`
}

// ReadHandshake reads one framed handshake and validates its version and
// session ID. All frames are length-checked before allocation.
func ReadHandshake(reader io.Reader, expectedSession string) (Handshake, error) {
	if reader == nil {
		return Handshake{}, errors.New("nil handshake reader")
	}
	if err := validateSessionID(expectedSession); err != nil {
		return Handshake{}, fmt.Errorf("invalid expected session: %w", err)
	}
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return Handshake{}, err
	}
	length := int(binary.BigEndian.Uint32(header[:]))
	if length == 0 || length > MaxFrame {
		return Handshake{}, errors.New("invalid frame length")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return Handshake{}, err
	}

	if !utf8.Valid(payload) {
		return Handshake{}, errors.New("invalid handshake: invalid UTF-8")
	}
	if !json.Valid(payload) {
		return Handshake{}, errors.New("invalid handshake: invalid JSON")
	}
	if err := strictjson.ValidateSurrogates(payload); err != nil {
		return Handshake{}, fmt.Errorf("invalid handshake: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return Handshake{}, errors.New("invalid handshake: expected object")
	}
	var message Handshake
	seen := make(map[string]bool, 2)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return Handshake{}, fmt.Errorf("invalid handshake: %w", err)
		}
		key, ok := token.(string)
		if !ok || (key != "version" && key != "session_id") || seen[key] {
			return Handshake{}, errors.New("invalid handshake: unknown or duplicate field")
		}
		seen[key] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return Handshake{}, fmt.Errorf("invalid handshake: %w", err)
		}
		if bytes.Equal(value, []byte("null")) {
			return Handshake{}, errors.New("invalid handshake: null field")
		}
		if key == "version" {
			err = json.Unmarshal(value, &message.Version)
		} else {
			err = json.Unmarshal(value, &message.SessionID)
		}
		if err != nil {
			return Handshake{}, fmt.Errorf("invalid handshake: %w", err)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return Handshake{}, fmt.Errorf("invalid handshake: %w", err)
	}
	if !seen["version"] || !seen["session_id"] {
		return Handshake{}, errors.New("invalid handshake: missing field")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Handshake{}, errors.New("invalid handshake: trailing data")
		}
		return Handshake{}, fmt.Errorf("invalid handshake: %w", err)
	}
	if message.Version != 1 {
		return Handshake{}, errors.New("unsupported handshake version")
	}
	if expectedSession == "" || message.SessionID != expectedSession {
		return Handshake{}, errors.New("handshake mismatch")
	}
	return message, nil
}

func validateSessionID(session string) error {
	return WriteHandshake(io.Discard, Handshake{Version: 1, SessionID: session})
}

// WriteHandshake writes one framed handshake.
func WriteHandshake(writer io.Writer, message Handshake) error {
	if writer == nil {
		return errors.New("nil handshake writer")
	}
	if message.Version != 1 || message.SessionID == "" {
		return errors.New("invalid handshake version or session ID")
	}
	if len(message.SessionID) > MaxFrame {
		return errors.New("invalid frame length")
	}
	if !utf8.ValidString(message.SessionID) {
		return errors.New("invalid handshake: invalid UTF-8 session ID")
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if len(payload) == 0 || len(payload) > MaxFrame {
		return errors.New("invalid frame length")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeAll(writer, header[:]); err != nil {
		return err
	}
	return writeAll(writer, payload)
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
