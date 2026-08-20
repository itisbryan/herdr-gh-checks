# GH Checks

A [Herdr](https://herdr.dev) plugin that watches the current PR's CI in a pane and shows CI/merge status on your sidebar space rows. Built with Go + [Charm/Bubble Tea](https://github.com/charmbracelet/bubbletea).

- **Live CI watch** — PR headline, review decision, per-check progress, description, and the changed-file list, refreshed every 5s.
- **Review inline** — open any file side-by-side against base in nvim; `ga` records `path:line` notes, then send the review to an agent.
- **Any PR** — press `p` to browse open PRs and review / approve / comment / request-changes without leaving the pane.
- **Workflows** — trigger GitHub Actions on a branch and watch a run to completion.
- **Merge / Update branch** — from the pane, worktree-aware.
- **Sidebar status** — colored Nerd Font glyphs per space (pass / fail / running / merged / open).

## Requirements

- [`herdr`](https://herdr.dev), [`gh`](https://cli.github.com) (authenticated), `git`, `nvim`, Go 1.25+
- A Nerd Font for the sidebar glyphs

## Install

```sh
git clone https://github.com/<you>/herder-gh-checks
herdr plugin install ./herder-gh-checks     # builds the binary
# or, for development:
cd herder-gh-checks && go build -o herder-gh-checks . && herdr plugin link .
```

Open the pane on a workspace whose cwd is a GitHub repo (or worktree):

```sh
herdr plugin pane open --plugin herder-gh-checks --entrypoint panel --placement split --direction right
```

## Keys

| Key | Action |
|-----|--------|
| `↑↓` / `j k` | move file cursor |
| `⏎` | review the selected file (`ga` to annotate a line) |
| `d` | review all files side-by-side |
| `/` | filter files |
| `a` | notes manager · `s` send review to an agent |
| `u` | update branch with base · `m` merge · `o` open on web |
| `p` | browse & review other PRs (`a` approve · `r` review · `c` comment) |
| `tab` / `w` | focus Workflows · `⏎` run · `v` watch a run |
| `1`–`4` | fold sections |

## Sidebar config

Add per-state tokens to `~/.config/herdr/config.toml`:

```toml
[[ui.sidebar.spaces.rows]]
rows = [
  ["state_icon", "workspace", {token = "$ci_pass", fg = "#a6e3a1", bold = true}],
  ["branch", "git_status"],
]
```

## License

MIT
