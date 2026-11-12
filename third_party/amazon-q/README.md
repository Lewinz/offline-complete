# Amazon Q input parser

The Rust input parser at `crates/figterm/src/input` of Amazon Q commit
`b654a1beb880951dd2343120bb867659622eecd9` was the reference for the Go package
`internal/input`. The Rust source is not part of the build and is not vendored
in this repository.

The upstream project provides MIT and Apache 2.0 licenses in this directory.
Its input module README says the code is largely derived from WezTerm's
termwiz. Verify the complete third-party attribution and license chain before
distributing this code.

The Go port covers only the subset described in `internal/input/README.md`.

On 2026-11-10 the local checkout's HEAD was the commit above and the checkout
was shallow. Its workspace declares `MIT OR Apache-2.0`; SHA-256 of its MIT
and Apache license files matched the copies here, respectively
`2b96c4c28ee2d49ba1b87f0bf8b2a92b098ef2b1ff8fe69e5f5426a9c14daaaa` and
`a6cba85bc92e0cff7a450b1d873c0eaa2e9fc96bf472df0247a26bec77bf3ff9`.
A shallow history cannot establish the original termwiz revision or all
intervening contributions; these checks do not clear the code for release.
