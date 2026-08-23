# CLAUDE.md

## Agent contract

cflio's primary consumer is an AI coding agent calling it from a shell; the conventions below exist to keep that contract stable.

- **Three-way documentation sync** — user-facing CLI behaviour is stated in `README.md`, `skills/cflio/SKILL.md`, and the cobra help strings under `internal/cmd/`. Changing the CLI means updating all three in the same PR. Overlaps today, each stated in more than one of the three: the 90 s default `--timeout`; the exit codes; the default `md` output format; the read → edit → update cycle and the sidecar's version lock; `update --title`'s renaming the page in the same request and the sidecar's refresh from the update response; the default filenames the two read modes write, and `--markdown`'s output being read-only; the `Degraded` and `Unchecked` counts `read --markdown` reports; `read --attachments`'s requiring `--markdown`, and its keeping a file already at the destination rather than replacing or re-fetching it; `attachments download`'s required `--pattern` and its refusal to overwrite an existing file; `plantuml`'s read → `get` → edit → `set` → `update` flow, `set`'s bumping `revision` and why the viewer needs it, and `set`'s refusing a macro with no `ac:local-id` on a live doc; and profile resolution.
- **The plugin description is a synced surface** — the one-line description in `.claude-plugin/plugin.json` and `.claude-plugin/marketplace.json` (top level and the `plugins[]` entry) must be byte-identical, and must say the same thing as the repository's GitHub "About" text and the README's opening line.
- **Output format and exit codes are a contract** — `--format md|json` and the exit codes produced by `Execute` and `describeFailure` in `internal/cmd/root.go` (`0`, `124` when the `--timeout` deadline expires, following the GNU `timeout` convention, `1` for everything else) are branched on mechanically by agent consumers. An interrupt produces no code of cflio's own on Unix: cflio prints nothing and terminates by the signal, so a parent sees `WIFSIGNALED` rather than a normal exit. Changing any of them is a breaking change, held to a higher bar than for a human-facing CLI.
- **`skills/cflio/SKILL.md` is router-style** — activation conditions and workflow only; per-command syntax stays delegated to `cflio --help`.

## Code conventions

Shared across the sibling CLIs (cflio, rdsh, slio):

- cobra commands are wired constructor-style (`newXCmd()` returning `*cobra.Command`); no package-level command or flag variables.
- Output formatting lives in `internal/format`. An API-client package is named after the service, and its main file after the package.
