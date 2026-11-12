package rules

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
)

// The installed application ships an encrypted rule pack. This key is embedded
// in the binary: it prevents accidental edits and casual replacement of the
// file, not a determined attacker with the application bundle. The source
// specifications remain the executable trust boundary for generators.
var ruleKey = [...]byte{
	0x06, 0x88, 0xa5, 0x0b, 0xf4, 0x8f, 0xee, 0xce,
	0x1d, 0x73, 0x1b, 0x0f, 0x13, 0x56, 0x30, 0xa5,
	0x1b, 0x93, 0x3d, 0x55, 0xec, 0x25, 0x6e, 0x81,
	0xc8, 0x06, 0x85, 0xe8, 0x3c, 0xc9, 0xca, 0xd5,
}

var ruleMagic = []byte("OCR1")

const maxEncryptedBytes = maxRuleBytes + 1024

// Encrypt seals data with AES-256-GCM. The output is magic || nonce || ciphertext.
func Encrypt(data []byte) ([]byte, error) {
	block, err := aes.NewCipher(ruleKey[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	result := make([]byte, 0, len(ruleMagic)+len(nonce)+len(data)+aead.Overhead())
	result = append(result, ruleMagic...)
	result = append(result, nonce...)
	return aead.Seal(result, nonce, data, nil), nil
}

// Decrypt opens a rule pack produced by Encrypt. Non-encrypted input is
// rejected so a plaintext file cannot silently replace the bundled pack.
func Decrypt(data []byte) ([]byte, error) {
	if len(data) > maxEncryptedBytes {
		return nil, errors.New("encrypted rule data exceeds size limit")
	}
	if !bytes.HasPrefix(data, ruleMagic) {
		return nil, errors.New("rule data is not encrypted")
	}
	block, err := aes.NewCipher(ruleKey[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := aead.NonceSize()
	start := len(ruleMagic)
	if len(data) < start+nonceSize {
		return nil, errors.New("truncated encrypted rule data")
	}
	plain, err := aead.Open(nil, data[start:start+nonceSize], data[start+nonceSize:], nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt rule data: %w", err)
	}
	return plain, nil
}

// LoadFile reads either an encrypted pack or a development JSON pack. The
// format is detected by the magic prefix, never by the filename extension.
func LoadFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("rule file is not regular")
	}
	if info.Size() > maxEncryptedBytes {
		return nil, errors.New("rule file exceeds size limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxEncryptedBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxEncryptedBytes {
		return nil, errors.New("rule file exceeds size limit")
	}
	if bytes.HasPrefix(data, ruleMagic) {
		return Decrypt(data)
	}
	// Development packs remain plain JSON and are bounded by FromJSON.
	return data, nil
}
