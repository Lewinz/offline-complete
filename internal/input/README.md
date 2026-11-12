# Input module

Go port of the terminal input parser used by the reference implementation. It
keeps the keymap trie, xterm/SS3 key sequences, Alt/escape handling, bracketed
paste and UTF-8 decoding.

The Go package is a derivative of the WezTerm `termwiz` input parser, which the
Amazon Q project copied into `crates/figterm/src/input` at commit
`b654a1beb880951dd2343120bb867659622eecd9`. See `third_party/amazon-q/` and
`third_party/wezterm/` for the upstream notices; both declare MIT.

Deliberately not ported for the first macOS version:

- mouse reports and pixel mouse events;
- Kitty keyboard protocol and key encoding;
- Windows console input records and Windows-only key handling.

These paths are not used by the first version and would need their own tests
before being added. Do not assume the port is byte-for-byte identical to the
upstream parser outside the covered sequences.

Bracketed paste events are limited to 1 MiB. An oversized paste is discarded
until its closing marker, then produces `EventPasteTooLarge`; its bytes are
never emitted as key events. An unterminated paste remains in discard mode and
produces no event. This parser is not used for raw PTY forwarding.
