package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// MaxMessage is the largest UI message, in bytes, excluding the newline.
const MaxMessage = 64 * 1024

// Item is one candidate sent to the interface. It contains no editing buffer.
type Item struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// OfferMessage replaces the interface's current candidate list.
type OfferMessage struct {
	Type     string `json:"type"`
	Version  uint64 `json:"version"`
	Offer    uint64 `json:"offer"`
	Selected int    `json:"selected"`
	Items    []Item `json:"items"`
}

// SelectionMessage highlights a candidate chosen with the keyboard.
type SelectionMessage struct {
	Type    string `json:"type"`
	Version uint64 `json:"version"`
	Offer   uint64 `json:"offer"`
	Index   int    `json:"index"`
}

// ClearMessage tells the interface to hide candidates for this version.
type ClearMessage struct {
	Type    string `json:"type"`
	Version uint64 `json:"version"`
}

// ResultMessage acknowledges a selection.
type ResultMessage struct {
	Type    string `json:"type"`
	Version uint64 `json:"version,omitempty"`
	Offer   uint64 `json:"offer,omitempty"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

type commandHeader struct {
	Type string `json:"type"`
}

type focusCommand struct {
	Type    string `json:"type"`
	Focused *bool  `json:"focused"`
}

type selectCommand struct {
	Type    string `json:"type"`
	Version uint64 `json:"version"`
	Offer   uint64 `json:"offer"`
	Index   int    `json:"index"`
}

type dismissCommand struct {
	Type    string `json:"type"`
	Version uint64 `json:"version"`
	Offer   uint64 `json:"offer"`
}

type commandKind uint8

const (
	commandFocus commandKind = iota + 1
	commandSelect
	commandDismiss
)

type command struct {
	kind    commandKind
	focused bool
	version uint64
	offer   uint64
	index   int
}

func parseCommand(data []byte) (command, error) {
	if len(data) == 0 || len(data) > MaxMessage || !json.Valid(data) {
		return command{}, errors.New("invalid UI message")
	}
	var header commandHeader
	if err := json.Unmarshal(data, &header); err != nil || header.Type == "" {
		return command{}, errors.New("invalid UI message header")
	}
	switch header.Type {
	case "focus":
		var value focusCommand
		if err := strictDecode(data, &value); err != nil || value.Focused == nil {
			return command{}, errors.New("invalid focus message")
		}
		return command{kind: commandFocus, focused: *value.Focused}, nil
	case "select":
		var value selectCommand
		if err := strictDecode(data, &value); err != nil || value.Version == 0 || value.Offer == 0 || value.Index < 0 {
			return command{}, errors.New("invalid select message")
		}
		return command{kind: commandSelect, version: value.Version, offer: value.Offer, index: value.Index}, nil
	case "dismiss":
		var value dismissCommand
		if err := strictDecode(data, &value); err != nil || value.Version == 0 || value.Offer == 0 {
			return command{}, errors.New("invalid dismiss message")
		}
		return command{kind: commandDismiss, version: value.Version, offer: value.Offer}, nil
	default:
		return command{}, errors.New("unknown UI message type")
	}
}

func strictDecode(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("trailing UI message data")
	}
	return nil
}

func encodeMessage(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > MaxMessage {
		return nil, errors.New("UI message too large")
	}
	return data, nil
}
