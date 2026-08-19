# CLAUDE.md

## Agent contract

cflio's primary consumer is an AI coding agent calling it from a shell; the conventions below exist to keep that contract stable.

- **Three-way documentation sync** — user-facing CLI behaviour is stated in `README.md`, `skills/cflio/SKILL.md`, and the cobra help strings under `internal/cmd/`. Changing the CLI means updating all three in the same PR. Overlaps today, each stated in more than one of the three: the 90 s default `--timeout`, the exit codes, the default `md` output format, the read → edit → update cycle and the sidecar's version lock, `--markdown`'s read-only output, and profile resolution.
- **Output format and exit codes are a contract** — `--format md|json` and the exit codes produced by `Execute` and `describeFailure` in `internal/cmd/root.go` (`0`, `124` when the `--timeout` deadline expires, following the GNU `timeout` convention, `1` for everything else) are branched on mechanically by agent consumers. An interrupt produces no code of cflio's own on Unix: cflio prints nothing and terminates by the signal, so a parent sees `WIFSIGNALED` rather than a normal exit. Changing any of them is a breaking change, held to a higher bar than for a human-facing CLI.
- **`skills/cflio/SKILL.md` is router-style** — activation conditions and workflow only; per-command syntax stays delegated to `cflio --help`.

## Code conventions

Shared across the sibling CLIs (cflio, rdsh, slio):

- cobra commands are wired constructor-style (`newXCmd()` returning `*cobra.Command`); no package-level command or flag variables.
- Output formatting lives in `internal/format`. An API-client package is named after the service, and its main file after the package.
