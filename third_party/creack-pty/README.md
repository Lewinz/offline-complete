# creack/pty

`github.com/creack/pty` v1.1.24 is vendored under `vendor/` for the PTY session
layer. It is used only for opening a pseudoterminal and resizing it; no network
or account code is involved.

The module declares MIT. Its license text is reproduced as `LICENSE.MIT` in
this directory and is also included in `vendor/github.com/creack/pty/LICENSE`.
The full transitive dependency set is empty: the module uses only the Go
standard library on the targeted platforms.

Re-check the license and the vendored contents before release. On 2026-11-10,
the vendored license and the copy here matched by SHA-256
(`a2a1cea59d9c2d71908082f516f86bb560abf857646ecf69d70c121ead14aef4`),
and `GOFLAGS=-mod=vendor GOPROXY=off go list -deps github.com/creack/pty`
showed only standard-library dependencies on the local Darwin arm64 host.
This is not an independent verification of the downloaded module's origin or
of every platform's dependency graph.
