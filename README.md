<h1 align="center">cc-sesh</h1>

<p align="center">
  <em>A sesh fork for Claude Code users — see every Claude instance's live status right inside the picker.</em>
</p>

<p align="center">
  <a href="https://opensource.org/licenses/MIT">
    <img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="MIT License" />
  </a>
  <a href="https://github.com/joshmedeski/sesh">
    <img src="https://img.shields.io/badge/forked%20from-joshmedeski%2Fsesh-blue.svg" alt="forked from joshmedeski/sesh" />
  </a>
</p>

<div align="center">

[English](README.md) | [简体中文](README.zh-cn.md)

</div>

---

## Credit

cc-sesh is a downstream fork of [**joshmedeski/sesh**](https://github.com/joshmedeski/sesh).

Every piece of session management, every tmux/zoxide/tmuxinator integration, the configuration system, the naming strategies, the picker — all of it was designed and polished over years by Josh Medeski and the upstream sesh contributors. **Without sesh, there is no cc-sesh.**

- Upstream repo: <https://github.com/joshmedeski/sesh>
- Upstream author: [Josh Medeski](https://github.com/joshmedeski) ([sponsor sesh](https://github.com/sponsors/joshmedeski))
- For the full feature list and configuration reference, the [upstream README](https://github.com/joshmedeski/sesh#readme) is authoritative

The LICENSE remains MIT, copyright © 2023 Josh Medeski (see [LICENSE](LICENSE)). Modifications added by this fork are released under the same MIT license.

If you don't need the Claude Code integration described below, **please use upstream sesh instead** — it is more stable, has a much more active community, and benefits from a richer ecosystem (Raycast / Ulauncher / Walker integrations, packaging across more platforms, etc.).

---

## What this fork adds

cc-sesh adds **one thing** on top of upstream sesh: it makes the live status of every [Claude Code](https://docs.claude.com/en/docs/claude-code) process visible in the picker, and pins a sticky reminder when a session has finished a round of work — so you can decide which session to attend to next when several are running in parallel.

### 1. Per-row Claude status table in the picker

Upstream sesh shows `src icon + session name` per row. cc-sesh inserts a 4-column status table in between:

```
  ^a all  ^t tmux  ^g configs  ^x zoxide  ^f find  ^d kill
  ──────────────────────────────────────────────────────────
       ATTN IDLE RUN  WAIT
  >   tm    1                 my-feature-branch
      tm                      bay-translate-extension
      tm    ●    1            ai-dev-kit          done 15m ago
      tm              2       long-running-task
      tm                  1   oauth-flow
      tm    1    1   1        mixed
      ze                      ~/Code/backend/athena
      ze                      ~/AI-Workspace/bay-translate
```

- **ATTN** (orange ●): a sticky reminder. **Independent of Claude's current state** — it lights up the moment a session finishes a `busy / subagent → idle` transition, and stays on until you attach (or manually dismiss / kill the session). It answers the question *"did the thing I told it to do actually finish?"*
- **IDLE**: number of currently-idle Claude processes
- **RUN**: `busy + subagent` (actively working)
- **WAIT**: processes waiting for user authorization (OAuth, permission prompts, etc.)

Data is sourced by scanning `~/.claude/sessions/*.json`, then matched to tmux panes by `cwd`, then aggregated per session. **No internal Claude Code APIs are used; no Claude Code configuration is modified.** Pure read-only.

### 2. Picker hotkeys filled in

So you no longer need `fzf-tmux` to wrap the picker, the hotkeys from the upstream `fzf-tmux` recipe are baked in:

| Hotkey | Action |
|---|---|
| `Ctrl-A` | all (default list) |
| `Ctrl-T` | tmux sessions only |
| `Ctrl-G` | sesh.toml configs only |
| `Ctrl-X` | zoxide history only |
| `Ctrl-F` | walk `$HOME` to depth ≤ 2 (replaces `fd`) |
| `Ctrl-D` | kill the tmux session under the cursor |
| `Alt-D` | dismiss the ATTN flag on the current row (does not kill the session) |

### 3. Renamed for coexistence

So you can keep upstream sesh installed alongside this fork:

| | upstream sesh | cc-sesh |
|---|---|---|
| binary | `sesh` | `cc-sesh` |
| Go module | `github.com/joshmedeski/sesh/v2` | `github.com/Wingsdh/cc-sesh/v2` |
| config dir | `$XDG_CONFIG_HOME/sesh/` | `$XDG_CONFIG_HOME/cc-sesh/` |
| config file | `sesh.toml` | `sesh.toml` (same filename) |
| state dir | — | `$XDG_STATE_HOME/cc-sesh/` (only used by the fork's attention state) |

> Both tools can be installed on the same machine; their configs and state never collide.

---

## Install

### Homebrew

```sh
brew install Wingsdh/tap/cc-sesh
```

Installed via my self-maintained [Homebrew tap](https://github.com/Wingsdh/homebrew-tap) (not in homebrew-core). The formula is updated automatically by GoReleaser on every release tag.

### Go install

```sh
go install github.com/Wingsdh/cc-sesh/v2@latest
```

Requires Go 1.25+.

---

After installing, the binary is named `cc-sesh`. All subcommand names match upstream sesh (`list / connect / picker / window / ...`).

> No AUR / Conda / Nix packaging is provided — this is a fork I maintain for my own use.

---

## Usage

### Basic commands

Identical to upstream sesh — just replace every `sesh` with `cc-sesh`:

```sh
cc-sesh list             # list every session source (tmux + config + zoxide + tmuxinator)
cc-sesh connect <name>   # connect to a session (creates it if it doesn't exist)
cc-sesh picker           # open the built-in picker (recommended)
```

The semantics and flags of `list / connect / window / pane / clone / root / last` and friends all match upstream — **see the [upstream README](https://github.com/joshmedeski/sesh#readme) for full details**.

### Recommended tmux binding

The picker is the main reason to use cc-sesh, so the best way to invoke it is as a tmux popup:

```tmux
bind-key "K" display-popup -h 90% -w 60% -E "cc-sesh picker -i"
```

`-i` shows source icons (requires a Nerd Font).

### How the Claude Code integration works

During each picker fetch:

1. Call the upstream lister to enumerate sessions (tmux / zoxide / config / tmuxinator)
2. Scan `~/.claude/sessions/*.json` and filter to live processes
3. Use `tmux list-panes` to grab each pane's `cwd`, then map Claude processes to tmux sessions by `cwd`
4. Compare against the previous round's `busy` state, detect `busy/subagent → idle` transitions, and persist them to `~/.local/state/cc-sesh/attention.json`
5. Render the per-session LiveBadge + Attention onto each row

The whole pipeline is transparent on machines without Claude Code — any failure (no `~/.claude/sessions/`, tmux not running, malformed JSON, etc.) silently degrades back to upstream sesh behavior, and the columns simply stay blank.

### Configuration

The config file lives at `~/.config/cc-sesh/sesh.toml`. **The schema is identical to upstream sesh** — `[default_session]` / `[[session]]` / `[[wildcard]]` / `[tui]` / `blacklist` / `dir_length` / `sort_order` / `cache`, all of it works as-is.

For configuration syntax and examples, see the [upstream Configuration section](https://github.com/joshmedeski/sesh#configuration).

cc-sesh adds **no new configuration keys** — the Claude integration is zero-config and intentionally not configurable.

### Attention state file

To make the sticky reminder survive across processes, cc-sesh persists attention state to:

```
$XDG_STATE_HOME/cc-sesh/attention.json
# default: ~/.local/state/cc-sesh/attention.json
```

You can delete this file at any time to wipe all attention flags; the next picker open starts collecting fresh state.

---

## Sync strategy with upstream

- The `main` branch periodically rebases / merges in upstream release tags
- The fork-specific changes are isolated to:
  - `claude/` (live + attention) — net-new packages
  - `picker/` — UI and hotkey changes
  - `seshcli/claude_wire.go` — wires lister + claude/live + claude/attention into the picker
  - global rename of module path, binary name, and config paths

If you want this Claude integration upstreamed, please take it up with [Josh Medeski](https://github.com/joshmedeski) directly — I have no plans to send a PR, since this complexity isn't useful for the vast majority of sesh users.

---

## License

MIT, inheriting from upstream [sesh's LICENSE](LICENSE), copyright © 2023 Josh Medeski. Modifications by this fork are released under the same MIT license.
