# Kiro autocomplete spec import

`base.json` is generated from Kiro CLI's public autocomplete spec CDN:

- Index: <https://specs.q.us-east-1.amazonaws.com/index.json>
- Spec files fetched: 1479 of 1484 (2026-11-12)
- Top-level shell commands imported: 707
- Static nodes in `base.json`: 112,814
- Imported fields: `name`, `description`, `subcommands`, `options`, `args`
- Imported 5,900 positional argument definitions and option arguments
- Raw spec modules are shipped encrypted alongside the static pack; the bundled
  Node 22 runtime executes their `script`, `custom`, `postProcess`, template,
  `cache`, `trigger` and fuzzy-filter behavior
- Cross-spec `loadSpec`/`generateSpec` uses an encrypted spec index and an
  on-demand Node-to-agent request; shell history is a local token source. The
  AI custom calls are rewritten at import from `fig` to `oc setting` / `oc ai request`; the optional OC AI generator supports only OpenAI Chat Completions and Anthropic Messages, and is disabled unless enabled in the app AI settings.

The spec files are the Fig/AWS Q autocomplete format. The upstream Fig
autocomplete repository is MIT licensed; the license text is in
`KIRO-SPECS-LICENSE.MIT`. Kiro CDN distribution terms should be reviewed before
shipping a public build.

The import intentionally omits namespaced package specs such as `@scope/cli`
and duplicate nested spec names that contain `/`; those are not top-level shell
commands. Kiro's runtime can discover their values with generators. The
installed app stores `rules.bin` and per-spec `.bin` files encrypted with a
key embedded in the binaries; this prevents casual replacement, not a
determined attacker who can inspect the app bundle.
