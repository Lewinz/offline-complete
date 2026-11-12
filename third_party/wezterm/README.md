# WezTerm / termwiz attribution

The Amazon Q `crates/figterm/src/input/README.md` says its input module is
largely derived from WezTerm, particularly `termwiz`. Amazon Q copied that
module; this project ported a subset of it to Go in `internal/input`, not
directly from the WezTerm checkout.

The locally available WezTerm checkout at commit
`80174f8009f41565f0fa8c66dab90d4f9211ae16` contains corresponding `termwiz`
input, keymap, and read buffer modules. Its `termwiz/Cargo.toml` declares MIT,
and `termwiz/LICENSE.md` is reproduced as `LICENSE.MIT`. The license requires
inclusion of its copyright and permission notices in copies or substantial
portions.

On 2026-11-10 the local checkout's `termwiz/Cargo.toml` identifies version
`0.20.0` and MIT, and its `termwiz/LICENSE.md` matched the copy here by
SHA-256 (`191c46fcf52061382b1c51a70311eb9081381cc158e5899f3739473a9432185b`).
This checks the notice copy, not the provenance of the Amazon Q source.

This comparison does **not** establish that the Amazon Q module was copied
from this exact WezTerm revision, nor does it establish all upstream authorship
or third-party provenance. The Amazon Q checkout available for inspection is
shallow. Verify the original revision, intervening history, and any additional
notices before distribution. Retain the Amazon Q notices separately as well.
