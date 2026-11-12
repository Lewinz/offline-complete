# Third-party source and release status

This inventory describes the current Go source and vendored dependencies. It
is not a legal opinion or a release approval. Do not ship the input parser or
any derived build until the unresolved provenance below has been reviewed.
No completion rules have been cleared for redistribution.

| Component | Use and fixed source | Notice | Status and remaining work |
| --- | --- | --- | --- |
| creack/pty | `github.com/creack/pty` v1.1.24; `internal/session` and the terminal proxy | [`creack-pty/LICENSE.MIT`](creack-pty/LICENSE.MIT); retain copyright and permission notice | Vendored license matches this copy. Review dependency and notices for the actual release. |
| golang.org/x/sys | v0.38.0, `golang.org/x/sys/unix`; local directory operations and IPC | [`golang-x-sys/LICENSE`](golang-x-sys/LICENSE), [`PATENTS`](golang-x-sys/PATENTS) | Vendored copies match. Review BSD redistribution conditions and patent grant for the actual release. |
| Amazon Q input | `internal/input` is a partial Go port of `crates/figterm/src/input/{mod.rs,keymap.rs,readbuf.rs}` at `b654a1beb880951dd2343120bb867659622eecd9` | [`amazon-q/LICENSE.MIT`](amazon-q/LICENSE.MIT), [`LICENSE.APACHE`](amazon-q/LICENSE.APACHE), [`README`](amazon-q/README.md) | Upstream declares MIT OR Apache-2.0; no release license selection is asserted here. The available checkout is shallow. See the unresolved WezTerm provenance below. |
| WezTerm/termwiz input | Amazon Q identifies its input module as largely derived from termwiz; exact originating revision and intervening contributions are unknown | [`wezterm/LICENSE.MIT`](wezterm/LICENSE.MIT), [`README`](wezterm/README.md) | The separately inspected termwiz checkout declares MIT, but has not been established as the actual source revision. **Not cleared for distribution.** Verify original revision, authorship and applicable notices before release. |

On 2026-11-11, with `GOFLAGS=-mod=vendor GOPROXY=off`, `go list -deps`
for `./...` on Darwin arm64 and amd64 (`CGO_ENABLED=0`) listed only the two
external Go modules above. This is a static package dependency listing, not a
review of every source file, build tool, system component, platform, runtime
behavior or transitive legal obligation. The `third_party/` license copies for
both Go modules matched their vendor copies by SHA-256. Amazon Q's local
checkout was at the fixed commit and its MIT and Apache license files matched
these copies; the checkout remained shallow. Hash equality checks copies, not
authenticity of upstream content or permission to redistribute a derivative.

The rules inventory and absent release rules are described in
`docs/feasibility.md`. A future rule bundle, font, icon, tool or other new
resource requires its own source, version, license and redistribution review.
