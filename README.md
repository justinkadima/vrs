# vrs — snapshots for solo developers

**vrs** is version control with no ceremony: numbered snapshots of your
whole folder, `undo` when you break something, `goto` to travel in time.
No staging area, no branches, no remotes, no staging area, no mental
model to maintain.

If you work alone and git's branching/merging/staging machinery has ever
felt like a tax you never asked to pay, vrs is for you. If you
collaborate with other people, keep using git — vrs deliberately does
not do collaboration.

**Status: v0.3.6.** Core loop (`save`, `diff`, `undo`, `redo`, `goto`,
`log`), agent support (`capture`, `mcp`), and `export`/`import` over SSH
and local directories.

## Install

```sh
go install github.com/justinkadima/vrs/cmd/vrs@latest
```

or build from source (single static binary, no dependencies):

```sh
git clone git@github.com:justinkadima/vrs.git
cd vrs && go build ./cmd/vrs
```

Works on macOS, Linux and Windows (amd64, arm64).

## 60 seconds

```sh
$ cd my-project
$ vrs save "initial"          # first save auto-initializes .vrs/
initialized vrs repository in /home/you/my-project
#1 saved — 42 files: 42 added, 0 modified, 0 deleted — 1.2 MiB new

$ ...hack on things...

$ vrs diff                    # what changed since the snapshot I'm on
added     notes.md
modified   main.go
deleted    old.txt

$ vrs save                    # message optional; positional or -m
#2 saved — 42 files: 0 added, 2 modified, 0 deleted — 3.4 KiB new

$ ...something breaks...

$ vrs undo                    # working copy back to the last snapshot
undid 2 change(s) — working tree restored to #2
  2 file(s) written
  1 file(s) moved to .vrs/trash/1780000000000000000
previous state captured as #3 — `vrs redo` to reapply, `vrs log --all` to see

$ vrs redo                    # changed your mind? reapply
redone — 2 file(s) written, 1 trashed (state from capture #3)

$ vrs goto @1                  # travel to any snapshot; no arg = newest
$ vrs log                      # the timeline
#2    2026-09-19 01:07  wip auth
#1    2026-09-19 01:05  initial snapshot (42 files)
```

That's the whole mental model: **save** when it works, **diff** to see
what you did, **undo/goto** when it doesn't.

## Solo developer workflows

### The daily loop

Work → `vrs save` → repeat. There is no staging area and nothing to
commit: every save is a full snapshot of the tree, and unchanged files
cost zero bytes (content is chunked, compressed and content-addressed —
saving twice in a row refuses politely: `nothing to save — working tree
matches #2`).

Messages are optional. Use them when a save *means* something:

```sh
vrs save "auth works"
vrs save before-refactor
```

### "Get me out of here"

Every mutating command is safe by construction:

```sh
vrs undo              # back to the snapshot you're on
vrs undo @5           # back to any snapshot
vrs goto @2h          # to the newest snapshot at least 2h old
vrs goto               # back to the tip after exploring the past
vrs redo              # reapply what undo removed
vrs redo --force      # ...even if you edited since (captures your edits first)
```

`undo`, `redo` and `goto` **always capture your current state as a hidden
snapshot first** — you can't lose work by trying to recover work. Files
they remove from the working copy go to `.vrs/trash/`, never
hard-deleted. History is append-only: nothing ever rewrites an old
snapshot.

If you save while visiting an old snapshot (`goto @3`, edit, `save`),
that **forks a new line** — vrs says so, and the old snapshots stay
reachable.

### Deploying, hot-fixing, and adopting old folders

`export` ships a *recorded snapshot* — never uncommitted work — to a
server or a local directory. `import` overlays a source back onto your
tree and snapshots the result:

```sh
$ vrs export deploy:/var/www/site       # position → server, incremental
exported #4 to deploy:/var/www/site — 12 file(s) written, 30 unchanged

$ vrs export deploy:/var/www/site @2    # rollback = redeploy any snapshot

# ...2am hot-fix directly on the server...

$ vrs import deploy:/var/www/site       # bring the hot-fix back
imported 1 file(s) from deploy:/var/www/site — snapshot #7
previous state captured as #6 — `vrs goto @6` to recover
```

The same pair doubles as a one-command on-ramp for unversioned folders:

```sh
vrs import ../old-copy          # auto-initializes and snapshots everything
vrs import ssh://user@host:2222/var/www/site    # URI form for non-22 ports
```

Local directory targets work exactly like remote ones. Re-imports are
incremental and a no-op when nothing changed. `--prune` opts into exact
mirroring — extras are moved to trash on the receiving side, never
deleted.

**Not a sync engine.** Single, directional, whole-state operations — no
hooks, no restarts, no conflict detection. If you need sync, use rsync.

## AI agents

Two integration levels, both built on the same history you use
interactively.

### Scripts and one-off agents: `capture`

`vrs capture` writes a hidden, deduplicated checkpoint — perfect for
"checkpoint before a risky operation":

```sh
$ vrs capture -t "before npm install"
checkpoint #4 — 12 file(s) changed (tag: before npm install) — restore with `vrs goto @4`
unchanged — state already recorded as #2      # capturing twice stores nothing
```

Checkpoints never pollute the timeline (`vrs log --all` to see them),
never advance your position, and never invalidate redos.

### MCP clients (Claude Code and friends): `vrs mcp`

`vrs mcp` speaks Model Context Protocol over stdio — the client spawns
it per session, so there is no daemon to install:

```sh
claude mcp add vrs -- vrs mcp
```

The agent gets four tools:

| Tool | What it does |
|---|---|
| `snapshot` | take a checkpoint of the current tree |
| `restore` | goto-semantics rewind — captures first, so even a runaway agent can't lose work |
| `diff` | what changed vs the position |
| `log` | the timeline (all commands accept an optional `path` argument for per-project use) |

A typical agent session: the agent calls `snapshot` before each risky
step and `restore` when a change made things worse — the same
append-only history, the same "nothing is ever lost" guarantee, visible
in `vrs log --all` alongside your own snapshots.

## Command reference

### Core

| Command | Purpose |
|---|---|
| `vrs save [msg] [-m msg]` | snapshot the working tree (auto-initializes) |
| `vrs diff [path] [@ref]` | changes vs the snapshot you're on; with a path, a unified content diff |
| `vrs undo [path] [@ref]` | restore the working copy from a snapshot (captures first) |
| `vrs redo [--force]` | reapply the last undone change |
| `vrs goto [@ref]` | jump the working copy to any snapshot; no argument = newest |
| `vrs log [-n N] [--all]` | the timeline (`--all` includes hidden captures) |

### Agents and scripts

| Command | Purpose |
|---|---|
| `vrs capture [-t tag]` | hidden checkpoint; idempotent; prints its `@N` |
| `vrs mcp` | MCP server over stdio (spawned by the client per session) |

### Ship it

| Command | Purpose |
|---|---|
| `vrs export <target> [@ref] [--prune]` | materialize a recorded snapshot onto a target |
| `vrs import <target> [--prune]` | overlay a target onto the working tree and snapshot it (auto-initializes) |

Targets: `user@host:/abs/path`, `ssh://user@host:2222/abs/path` (URI
form is the only way to inline a port; otherwise `~/.ssh/config` Port
applies), or a local directory. Auth mirrors `ssh` — `~/.ssh/config`
aliases, agent and key auth (`IdentitiesOnly` and multiple `IdentityFile`
lines honored), passphrase prompt on the terminal, `known_hosts`
strictly enforced (never auto-accepts). Password auth is not supported.

### Refs

`@`-refs work in every command:

| Ref | Meaning |
|---|---|
| `@5` | snapshot #5 (visible saves *and* hidden captures) |
| `@-2` | two saves back from the tip |
| `@2h` | newest snapshot at least 2h old (`@30m`, `@3d`, `@1w`) |

## The rules

The guarantees every command upholds:

- **Snapshots are full-tree and history is append-only.** No command can
  rewrite or lose a recorded state.
- **Nothing is ever lost.** `undo`/`redo`/`goto` capture your current
  state as a hidden snapshot before touching anything; import captures
  before overlaying. Files leaving the working copy go to
  `.vrs/trash/`, never hard-deleted.
- **The working copy always sits on a snapshot** (the position); `diff`
  and `undo` compare against it, `goto` moves it.
- **Exports ship recorded state, never uncommitted work.** vrs owns
  three names at a target (`.vrs-manifest.json`, `.vrs-trash/` — plus
  the repository's root `.vrsignore`, which never ships and is never
  touched) and imports skip them all. Everything else is your content
  and ships exactly as recorded.
- **The whole history is one file:** `.vrs/vrs.db` (SQLite). Copy it and
  you've backed up everything; delete it and the project is a plain
  folder again.
- **Ignore rules:** built-in defaults (`node_modules/`, `.git/`, `dist/`,
  `.env*`, `.DS_Store`, …) plus a root `.vrsignore` in gitignore syntax
  (`**`, `!negations`, anchored `/patterns` all work). Nested
  same-name files have no meaning to vrs. Symlinks and empty dirs are
  untracked.
- **Transfers report progress** on stderr (`[12/142] path · 1.4 MiB` per
  file); summaries stay on stdout.

## Performance

Measured with the e2e harness (`go test ./e2e/ -run TestPerf10k -v`) on a
10k-file, ~165 MiB source-shaped fixture (Apple M-series, warm cache):

| Scenario | p50 | Target |
|---|---|---|
| Clean save (refused, no writes) | 82 ms | < 300 ms |
| Clean `diff` | 82 ms | < 300 ms |
| Save, 100 files changed | 141 ms | < 2 s |
| First-ever save (hash + chunk + compress everything) | 1.2 s | — |

## What vrs is not

- **Not a collaboration tool.** No remotes-to-share, no push/pull, no
  merge. Solo machines only — export/import moves trees between yours.
- **Not a sync engine.** Import/export are single, directional,
  whole-state operations with no conflict detection.
- **Not a daemon.** `vrs mcp` is spawned per agent session and exits with
  it.
- **Not a backup system for your disk.** It versions folders; pair it
  with real backups (copying `.vrs/vrs.db` *is* a full backup).

## License

[MIT](LICENSE)