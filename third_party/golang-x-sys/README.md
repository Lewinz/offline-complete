# golang.org/x/sys

`golang.org/x/sys` v0.38.0 is vendored for `unix.Openat` in the local
rule inventory tool. It provides a supported Go wrapper for descriptor-relative
`O_NOFOLLOW` opening on Darwin; `os.Root.OpenFile` resolves symlinks itself.
No runtime download or executable tool is involved.

The dependency was taken from the existing local module cache with `GOPROXY=off`
and `GOSUMDB=off`; no independent verification of its origin or online checksum
was performed in this session. `go.sum` records the module's Go checksum. The
cached module zip SHA-256 was
`dacd7c9aa2b298f966822da214c6d601da08f14d41b29032bcac4bc503887a49`.

The module carries a BSD 3-Clause license and a separate patent grant; both
are reproduced here and in `vendor/golang.org/x/sys/`. Source redistribution
must retain the license; binary redistribution must reproduce the copyright,
conditions, and disclaimer in accompanying materials. Review the license and
patent grant for the intended release. SHA-256: LICENSE
`911f8f5782931320f5b8d1160a76365b83aea6447ee6c04fa6d5591467db9dad`,
PATENTS `96f408bfae65bf137fc2525d3ecb030271c50c1e90799f87abf8846d8dd505cc`.
On the local Darwin arm64 host, the imported `unix` package directly imports
only Go standard-library packages. Darwin amd64 and arm64 offline builds were
also checked; these are not execution tests on Intel, a full source audit, or
release approval.
