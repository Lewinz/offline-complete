# Bundled static rules

`base.json` is generated from Kiro CLI's public autocomplete spec CDN.
It contains declarative commands, subcommands, options, descriptions, argument
generators and filesystem templates. The app does not contact the CDN at
runtime. Raw spec modules are imported locally and executed by the bundled Node
generator runtime, with AI calls rewritten to OC commands.

- Source: <https://specs.q.us-east-1.amazonaws.com/index.json>
- Imported: 707 top-level shell commands, about 112,814 static nodes
- Imported: 5,900 positional argument definitions (including nested commands), 224 allowlisted script
  generators, and 57 filesystem templates
- Runtime: encrypted per-spec index, cross-spec loadSpec/generateSpec, shell-history tokens, option values, insertValue, multi-argument options and optional AI endpoint
- License: Fig/AWS Q autocomplete format; upstream Fig repository is MIT.
  See `KIRO-SPECS-LICENSE.MIT` and `KIRO-SPECS.md`.

The importer is a build-time step; the app does not contact this CDN at runtime.
Runtime generator code is local and executes through the bundled Node runtime.

```bash
shasum -a 256 rules/base.json
```

Kiro CDN distribution terms should be reviewed before publishing a public build.
