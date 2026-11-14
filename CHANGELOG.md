# Changelog

## [v1.1.1](https://github.com/178inaba/atlio/compare/v1.1.0...v1.1.1) - 2026-11-14

### Changes
- Tell an agent to check before re-running a create whenever the request may have been carried out by @178inaba in https://github.com/178inaba/atlio/pull/125
- Refuse matched attachments that would be written to one file by @178inaba in https://github.com/178inaba/atlio/pull/126

## [v1.1.0](https://github.com/178inaba/atlio/compare/v1.0.6...v1.1.0) - 2026-11-12

### Changes
- Rename the CLI, module, config and plugin from cflio to atlio by @178inaba in https://github.com/178inaba/atlio/pull/107
- Move the Confluence commands under atlio confluence by @178inaba in https://github.com/178inaba/atlio/pull/109
- Store the bare site URL in profiles and lift the shared Atlassian transport out of the Confluence client by @178inaba in https://github.com/178inaba/atlio/pull/110
- Add atlio confluence create to make a page under a parent and keep editing the same file by @178inaba in https://github.com/178inaba/atlio/pull/114
- Add atlio jira issue read, writing an issue to a file as ADF or Markdown by @178inaba in https://github.com/178inaba/atlio/pull/115
- Add confluence attachments upload so an agent can put a screenshot on a page by @178inaba in https://github.com/178inaba/atlio/pull/113
- Add atlio jira search for JQL queries by @178inaba in https://github.com/178inaba/atlio/pull/118
- Add atlio jira comments list by @178inaba in https://github.com/178inaba/atlio/pull/121
- Accept a space's overview and space URLs wherever atlio takes a page by @178inaba in https://github.com/178inaba/atlio/pull/119
- Add atlio jira attachments list and download by @178inaba in https://github.com/178inaba/atlio/pull/123
- Split the atlio skill into SKILL.md and per-product reference files by @178inaba in https://github.com/178inaba/atlio/pull/124

## [v1.0.6](https://github.com/178inaba/cflio/compare/v1.0.5...v1.0.6) - 2026-11-11

### Changes
- Generate the Homebrew cask's quarantine hook as postflight_steps by @178inaba in https://github.com/178inaba/cflio/pull/98
### Dependency updates
- Bump golang.org/x/term from 0.45.0 to 0.46.0 by @dependabot[bot] in https://github.com/178inaba/cflio/pull/92
- Bump golangci/golangci-lint from v2.13.2 to v2.14.0 by @dependabot[bot] in https://github.com/178inaba/cflio/pull/94
- Bump golangci/golangci-lint from v2.14.0 to v2.15.0 by @dependabot[bot] in https://github.com/178inaba/cflio/pull/96
- Bump golang.org/x/term from 0.46.0 to 0.47.0 by @dependabot[bot] in https://github.com/178inaba/cflio/pull/95

## [v1.0.5](https://github.com/178inaba/cflio/compare/v1.0.4...v1.0.5) - 2026-09-03

### Changes
- Approve and auto-merge Dependabot pull requests by @178inaba in https://github.com/178inaba/cflio/pull/89
### Dependency updates
- Bump golangci/golangci-lint from v2.13.1 to v2.13.2 by @dependabot[bot] in https://github.com/178inaba/cflio/pull/91

## [v1.0.4](https://github.com/178inaba/cflio/compare/v1.0.3...v1.0.4) - 2026-09-02

### Changes
- Pin the npx skills install command to the stable tag by @178inaba in https://github.com/178inaba/cflio/pull/79
- Correct what the 178inaba/cflio@stable shorthand actually does by @178inaba in https://github.com/178inaba/cflio/pull/83
- Port the JSON handling to encoding/json/v2 by @178inaba in https://github.com/178inaba/cflio/pull/82
- Generate release notes from GitHub's What's Changed by @178inaba in https://github.com/178inaba/cflio/pull/87
- Derive the README install URL from the marketplace entry by @178inaba in https://github.com/178inaba/cflio/pull/86

## [v1.0.3](https://github.com/178inaba/cflio/compare/v1.0.2...v1.0.3) - 2026-08-31

- Enable revive and adopt the newer standard-library forms Go 1.27 offers by @178inaba in https://github.com/178inaba/cflio/pull/74
- Resolve Confluence short links by decoding the tiny-link token locally by @178inaba in https://github.com/178inaba/cflio/pull/78

## [v1.0.2](https://github.com/178inaba/cflio/compare/v1.0.1...v1.0.2) - 2026-08-31

- Drop the wrapper rationale from the attest step's comment by @178inaba in https://github.com/178inaba/cflio/pull/71
- Tie the plugin to releases with a stable tag and a tagpr-bumped version by @178inaba in https://github.com/178inaba/cflio/pull/72

## [v1.0.1](https://github.com/178inaba/cflio/compare/v1.0.0...v1.0.1) - 2026-08-31

- Attest the release artifacts with build provenance and move off app-id by @178inaba in https://github.com/178inaba/cflio/pull/66

## [v0.0.1](https://github.com/178inaba/cflio/commits/v0.0.1) - 2026-08-31

- Implement the initial Confluence read/write CLI (auth / profile / read / update / search / children / comments) by @178inaba in https://github.com/178inaba/cflio/pull/2
- Build child page URLs from the parent's web link by @178inaba in https://github.com/178inaba/cflio/pull/5
- Add a --markdown read mode that converts the storage body locally by @178inaba in https://github.com/178inaba/cflio/pull/9
- Add a CI workflow and Dependabot, pinning golangci-lint via compose.yaml by @178inaba in https://github.com/178inaba/cflio/pull/10
- Build the cobra command tree with constructors by @178inaba in https://github.com/178inaba/cflio/pull/15
- Pin the golangci-lint linter set and relax the go directive by @178inaba in https://github.com/178inaba/cflio/pull/17
- Route comment bodies through the Markdown converter and retire StripStorage by @178inaba in https://github.com/178inaba/cflio/pull/18
- Resolve mentions and page links when reading a page as Markdown by @178inaba in https://github.com/178inaba/cflio/pull/19
- Ignore .claude/worktrees so linked worktrees do not dirty git status by @178inaba in https://github.com/178inaba/cflio/pull/24
- Make --format a pflag.Value type so cobra validates it during flag parsing by @178inaba in https://github.com/178inaba/cflio/pull/25
- Tell a failed reference lookup apart from one that resolved to nothing by @178inaba in https://github.com/178inaba/cflio/pull/26
- Report a mistyped subcommand under a group command instead of exiting 0 by @178inaba in https://github.com/178inaba/cflio/pull/29
- Render the plantumlcloud macro's source as a fenced block instead of dropping it by @178inaba in https://github.com/178inaba/cflio/pull/35
- Decode a plantumlcloud data payload with no base64 padding by @178inaba in https://github.com/178inaba/cflio/pull/38
- Catch the signal only while the terminal is modified, and die of it by @178inaba in https://github.com/178inaba/cflio/pull/41
- Return the exit code from Execute, with 124 when --timeout expires by @178inaba in https://github.com/178inaba/cflio/pull/42
- Record the agent contract and shared conventions in CLAUDE.md by @178inaba in https://github.com/178inaba/cflio/pull/43
- Ship the Agent Skill as a Claude Code plugin with a self-hosted marketplace by @178inaba in https://github.com/178inaba/cflio/pull/44
- Add cflio attachments list and download so a page's images can be read by @178inaba in https://github.com/178inaba/cflio/pull/45
- Resolve a body's images to downloaded files in read --markdown by @178inaba in https://github.com/178inaba/cflio/pull/46
- Report an unresolvable help topic instead of exiting 0 by @178inaba in https://github.com/178inaba/cflio/pull/47
- Rename a page with cflio update --title by @178inaba in https://github.com/178inaba/cflio/pull/48
- Add cflio plantuml list, get and set by @178inaba in https://github.com/178inaba/cflio/pull/49
- Add cflio plantuml add so a new diagram can be inserted into a downloaded page body by @178inaba in https://github.com/178inaba/cflio/pull/50
- Post footer comments with cflio comments create by @178inaba in https://github.com/178inaba/cflio/pull/51
- Record a mistyped subcommand as an error instead of reporting it from the help function by @178inaba in https://github.com/178inaba/cflio/pull/53
- Render the expand macro as <details> and drop the toc macro instead of degrading them by @178inaba in https://github.com/178inaba/cflio/pull/55
- Move the module to go 1.27 and bump golangci-lint to v2.13.1 by @178inaba in https://github.com/178inaba/cflio/pull/58
- Enable the gofmt formatter so CI fails on unformatted code by @178inaba in https://github.com/178inaba/cflio/pull/59
- Release cflio with tagpr and GoReleaser by @178inaba in https://github.com/178inaba/cflio/pull/63
