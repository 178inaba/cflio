# Changelog

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
