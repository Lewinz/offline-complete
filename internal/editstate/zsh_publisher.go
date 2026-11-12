package editstate

import (
	"errors"

	"offline-complete/internal/shellparse"
)

// UpdateZsh publishes a zsh character-indexed snapshot. The caller must
// establish that zsh is using multibyte indexing for this particular update.
// An unverified mode or invalid cursor closes the stream like any failed update.
func (p *Publisher) UpdateZsh(line string, cursor int, multibyte bool) error {
	return p.send(func(seq uint64, session string) error {
		if !multibyte || len(line) > 64*1024 {
			return errors.New("invalid zsh editing snapshot")
		}
		offset, err := shellparse.ZshCursorToByte(line, cursor)
		if err != nil {
			return err
		}
		return WriteUpdateConn(p.conn, session, seq, line, offset, p.timeout)
	})
}
