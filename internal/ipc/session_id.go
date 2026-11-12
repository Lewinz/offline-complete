package ipc

import (
	"crypto/rand"
	"encoding/hex"
)

// NewSessionID returns a random identifier for distinguishing sessions.
// It is not an authentication credential.
func NewSessionID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}
