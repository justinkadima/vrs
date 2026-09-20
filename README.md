# vrs

Snapshots for your code. A version control system for solo developers:
numbered snapshots of your folder, diffs against the last snapshot, and
undo/redo — no staging area, no branches, no remotes, no ceremony.

**Status: v0.3.0.** Core commands (`save`, `diff`, `undo`, `redo`, `goto`,
`capture`, `log`, `mcp`) plus **import/export** over SSH and local
directories — snapshot, ship to your server, hot-fix there, import back.
See [PLAN.md](PLAN.md) for the full design and roadmap.

## Install

```sh
go install github.com/justinkadima/vrs/cmd/vrs@latest
```

or build from source:

```sh
git clone git@github.com:justinkadima/vrs.git
cd vrs && go build ./cmd/vrs
```

## Usage

```sh
$ cd my-project
$ vrs save "initial"          # first save auto-initializes .vrs/
initialized vrs repository in /home/you/my-project
#1 saved — 42 files: 42 added, 0 modified, 0 deleted — 1.2 MiB new

$ vrs save                    # nothing changed → refused, timeline stays clean
nothing to save — working tree matches #1

$ vrs save -m "bug fixed"     # message via flag or positional argument
#2 saved — 42 files: 0 added, 2 modified, 0 deleted — 3.4 KiB new

$ vrs diff                    # changes vs the snapshot you are on
added     notes.md
modified  main.go
deleted   old.txt

$ vrs diff main.go             # unified content diff
$ vrs diff main.go @2           # ... against any snapshot

$ vrs undo                     # working copy back to the snapshot you are on
undid 2 change(s) — working tree restored to #3
  2 file(s) written
  1 file(s) moved to .vrs/trash/1780000000000000000
previous state captured as #7 — `vrs redo` to reapply

$ vrs redo                     # changed your mind? reapply
redone — 2 file(s) written, 1 trashed (state from capture #7)

$ vrs goto @1                  # time travel: working copy back to #1
working copy now at #1 — previous state captured as #8 (`vrs log --all`)
  1 file(s) written
  1 file(s) moved to .vrs/trash/1780000001000000000
you are behind the tip (#3) — save here to start a new line from #1, or `vrs goto` (no argument) to return

$ vrs goto                     # no argument = return to the tip

$ vrs log
#3    2026-09-19 01:09  bug fixed
#2    2026-09-19 01:07  wip auth
#1    2026-09-19 01:05  initial snapshot (42 files)

$ vrs log --all                # include the hidden captures
```

- Snapshots are **full-tree** and **append-only** — history can never be
  rewritten or lost by any command.
- **Nothing is ever lost**: `undo`/`redo`/`goto` capture your current state
  as a hidden snapshot before touching anything (`vrs log --all` to see them).
- The working copy always sits **on** a snapshot (the tip after `save`, the
  target after `goto`); `diff` and `undo` compare against that position.
- `redo` refuses to clobber edits you made after an undo (editor convention);
  `vrs redo --force` captures your state first, then overwrites.
- Files removed by `undo`/`redo`/`goto` go to `.vrs/trash/`, never hard-deleted.
- `@refs` everywhere: `@3` (snapshot #3), `@-2` (two saves back), `@2h`
  (newest snapshot at least 2h old) — `vrs log` for the numbers.
- Saving from a past position (after `goto`) **forks a new line** — vrs says
  so, and every old snapshot stays intact.
- Unchanged files cost **zero bytes** per save: content is content-addressed,
  CDC-chunked (~64 KiB), zstd-compressed.
- The entire history lives in **one file**: `.vrs/vrs.db` (SQLite).
  Copy it and you've backed up everything.
- Ignore rules: built-in defaults (`node_modules/`, `.git/`, `dist/`, …)
  plus a `.vrsignore` in gitignore syntax (root file; `**`, `!negations`,
  anchored `/patterns` all work). Symlinks and empty dirs are untracked.

## Performance

Measured with the e2e harness (`go test ./e2e/ -run TestPerf10k -v`) on a
10k-file, ~165 MiB source-shaped fixture (Apple M-series, warm cache):

| Scenario | p50 | Target |
|---|---|---|
| Clean save (refused, no writes) | 82 ms | < 300 ms |
| Clean `diff` | 82 ms | < 300 ms |
| Save, 100 files changed | 141 ms | < 2 s |
| First-ever save (hash + chunk + compress everything) | 1.2 s | — |

## Agent checkpointing

AI coding agents can checkpoint and rewind your repository through the same
append-only history — no WIP commits, no vendor lock-in:

- **From any agent or script:** `vrs capture -t "before npm install"` writes a
  hidden, deduplicated checkpoint and prints its id; restore with
  `vrs goto @N`. Idempotent: capturing an unchanged tree stores nothing.
- **MCP server:** `vrs mcp` speaks Model Context Protocol over stdio — the
  agent client spawns it per session, so there is no daemon to install.
  Register it once:

  ```sh
  claude mcp add vrs -- vrs mcp
  ```

  and the agent gets four tools: `snapshot`, `restore`, `diff`, `log` —
  every restore captures first, so even a runaway agent can't lose work.

## Ship it: export / import

The solo loop — snapshot → deploy → hot-fix on the server → import back —
without becoming a deploy tool or a sync engine:

```sh
$ vrs export deploy:/var/www/site          # ship the recorded state (#N), not your mess
exported #4 to deploy:/var/www/site — 12 file(s) written, 30 unchanged

$ vrs export deploy:/var/www/site @2        # ship any snapshot — rollback by redeploying

# …you hot-fix a file directly on the server at 2am…

$ vrs import deploy:/var/www/site           # bring it back
imported 1 file(s) from deploy:/var/www/site — snapshot #7
previous state captured as #6 — `vrs goto @6` to recover
```

- Targets are `[user@]host:/path` or `ssh://user@host:2222/path` (URI form
  is the only way to inline a port; otherwise `~/.ssh/config` Port applies;
  aliases, agent and key auth, `known_hosts` enforced) or plain local
  directories.
- vrs prefers the host key *types* already recorded in your `known_hosts` —
  like OpenSSH — so a server offering several key types verifies against the
  key you've already trusted. (First contact still happens through `ssh`
  itself, which writes the entry.)
- Auth mirrors `ssh`: the agent plus every `IdentityFile` from your config
  (multiple lines and `IdentitiesOnly` honored), and encrypted keys prompt
  for a passphrase on the terminal. Under `vrs mcp` or scripts there is no
  prompt — load the key with `ssh-add` instead. Password auth is not
  supported.
- Exports are **incremental** via a manifest at the target and always ship
  *recorded* state — never uncommitted work.
- Transfers report progress on stderr: a scan heartbeat, then
  `[12/142] path · 1.4 MiB` per file, so a stuck connection is visible
  instead of silent. Summaries stay on stdout. `--prune` mirrors exactly
  (extras trashed, never deleted).
- Imports overlay the source onto your tree (ignore-filtered, `.vrs/` never
  touched), then **snapshot automatically** — `import from …` lands in the
  timeline. Re-imports are incremental (rsync-style quick-check).
- Local-directory targets work identically — `vrs import ../old-copy` is a
  one-command way to put an unversioned folder under version control.
- **Not a sync engine.** Single, directional, whole-state operations. No
  hooks, no restarts, no conflict detection — if you need sync, use rsync.

## Design

See [PLAN.md](PLAN.md) for the implementation plan, data model, and the
semantics of every command (including what `undo`/`redo`/`goto` will mean
when they land).

## License

[MIT](LICENSE)