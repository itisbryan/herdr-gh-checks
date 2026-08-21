# GH Checks

[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
![herdr 0.7+](https://img.shields.io/badge/herdr-0.7%2B-8a2be2)
![platforms: linux • macOS](https://img.shields.io/badge/platforms-linux%20%E2%80%A2%20macOS-informational)
![Go 1.25+](https://img.shields.io/badge/go-1.25%2B-00add8.svg)

**Watch and review the current PR's CI in a [herdr](https://herdr.dev) pane.** The pane shows the PR headline, review decision, per-check progress, description, and the changed-file list, refreshed every 5s — and puts a compact CI/merge status on your sidebar space rows. Built with Go + [Bubble Tea](https://github.com/charmbracelet/bubbletea). It only reads GitHub; the one thing it writes is your explicit merge / approve / review, run through `gh`.

## Features

- **Live CI watch** — PR pill, review decision, animated per-check progress, and the changed-file list, polled every 5s until the PR settles.
- **Review inline** — open any file side-by-side against base in `nvim`; `ga` records a `path:line` note, then `s` sends the review to an agent pane.
- **Any PR** — press `p` to browse open PRs and review / approve / comment / request-changes without leaving the pane.
- **Workflows** — trigger a GitHub Actions workflow on a branch and watch a run to completion, right in the pane.
- **Merge & update branch** — merge the PR or bring it up to date with base, worktree-aware.
- **Sidebar status** — a colored Nerd Font glyph per space (pass / fail / running / merged / open), animated while CI runs.

## Requirements

- **herdr ≥ 0.7.0** (the plugin system)
- **`gh`** (authenticated: `gh auth status`), **`git`**, and **`nvim`** on your `PATH`
- A **Nerd Font** for the sidebar glyphs
- **macOS or Linux** (amd64 / arm64). **Windows via WSL** (installs the Linux build normally); native Windows binaries are attached to each [release](https://github.com/itisbryan/herdr-gh-checks/releases) for git-bash / manual use.
- **Go 1.25+** only if building from source — `plugin install` downloads a prebuilt binary when a matching release exists

## Install

From GitHub (downloads a prebuilt binary for your platform; builds from source only if there's no matching release):

```bash
herdr plugin install itisbryan/herdr-gh-checks
```

Or link a local checkout for development:

```bash
cd herdr-gh-checks && go build -o herdr-gh-checks . && herdr plugin link .
```

Discoverable through the [Herdr plugin marketplace](https://herdr.dev/plugins/) via the `herdr-plugin` GitHub topic. Marketplace listings are automatic and are not endorsements or security reviews.

## Quick start

Open the pane on a workspace whose cwd is a GitHub repo (or worktree):

```bash
herdr plugin pane open \
  --plugin herdr-gh-checks \
  --entrypoint panel \
  --placement split \
  --direction right
```

### Keybinding

herdr keybindings live in the user's config, not the plugin manifest. Add one to `~/.config/herdr/config.toml` and run `herdr server reload-config`:

```toml
[[keys.command]]
key = "prefix+i"
type = "plugin_action"
command = "herdr-gh-checks.show"   # <plugin_id>.<action_id> — a dot, not a slash
description = "open GH Checks"
```

Then press your prefix (default `ctrl+b`) followed by `i`. Avoid `alt+` chords (they emit characters in the terminal) and keys taken by built-ins.

## Keys

| Key | Action |
| --- | --- |
| `↑↓` · `j` `k` | Move the file cursor |
| `⏎` | Review the selected file (`ga` in nvim annotates a line) |
| `d` | Review all files side-by-side |
| `/` | Filter files |
| `a` · `s` | Notes manager · send review to an agent |
| `u` · `m` · `o` | Update branch with base · merge · open on web |
| `p` | Browse & review other PRs (`a` approve · `r` review · `c` comment) |
| `tab` `w` | Focus Workflows — `⏎` run · `v` watch a run |
| `1`–`4` | Fold sections |

## Sidebar setup

For each space, the plugin writes a `ci_<state>` token (`workspace report-metadata --token ci_pass=…`). herdr's packed sidebar only renders tokens you place in your row config, so add the state tokens to `~/.config/herdr/config.toml`, then `herdr server reload-config`:

```toml
[[ui.sidebar.spaces.rows]]
rows = [
  ["state_icon", "workspace",
    {token = "$ci_pass",   fg = "#a6e3a1", bold = true},
    {token = "$ci_fail",   fg = "#f38ba8", bold = true},
    {token = "$ci_run",    fg = "#f9e2af"},
    {token = "$ci_merged", fg = "#cba6f7"},
    {token = "$ci_open",   fg = "#89b4fa"}],
  ["branch", "git_status"],
]
```

Run the status daemon in the background so the tokens stay fresh:

```bash
nohup ./herdr-gh-checks/herdr-gh-checks --sidebar &
```

## License

[MIT](LICENSE) © itisbryan
