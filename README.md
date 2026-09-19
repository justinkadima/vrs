# vrs

Snapshots for your code. A version control system for solo developers:
numbered snapshots of your folder, diffs against the last snapshot, and
undo/redo — no staging area, no branches, no remotes, no ceremony.

**Status: M0 (pre-alpha).** `save` and `log` work. `diff`, `undo`, `redo`,
and `goto` land next — see [PLAN.md](PLAN.md) for the full roadmap.

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

$ vrs log
#2    2026-09-19 01:09  bug fixed
#1    2026-09-19 01:07  initial
```

- Snapshots are **full-tree** and **append-only** — history can never be
  rewritten or lost by any command.
- Unchanged files cost **zero bytes** per save: content is content-addressed,
  CDC-chunked (~64 KiB), zstd-compressed.
- The entire history lives in **one file**: `.vrs/vrs.db` (SQLite).
  Copy it and you've backed up everything.
- Ignore rules: built-in defaults (`node_modules/`, `.git/`, `dist/`, …)
  plus a `.vrsignore` in gitignore syntax.

## Design

See [PLAN.md](PLAN.md) for the implementation plan, data model, and the
semantics of every command (including what `undo`/`redo`/`goto` will mean
when they land).

## License

MIT (to be finalized at v0.1.0).