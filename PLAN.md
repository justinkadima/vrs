# vrs — Implementation Plan

**vrs** is a version control system for solo developers: snapshots of your folder,
diffs against the last snapshot, and undo/redo — with no staging area, no branches,
no remotes, no ceremony.

- **Binary:** `vrs` (Go, static, zero CGo)
- **Storage:** one SQLite database in `.vrs/` + content-addressed chunks inside it
- **Mental model:** a timeline of numbered snapshots (`#1, #2, …`), not a DAG

---

## 1. Scope

### In (v1)

| Command | Behavior |
|---|---|
| `vrs save [msg]` / `vrs save -m msg` | Snapshot the **entire tree**. First run in a folder auto-inits `.vrs/`. Optional message; auto-generated if absent (changed-file names). Prints the new `#N`. |
| `vrs diff` | List of added / modified / deleted files in current scope vs latest snapshot (merges git's `status`). |
| `vrs diff <path>` | Unified diff of that file vs latest snapshot (or vs `@ref`). |
| `vrs undo [path] [@ref]` | Restore working copy from a snapshot (latest by default). Files created since the snapshot are moved to trash, not deleted. |
| `vrs redo [--force]` | Reapply the last undone change. Refuses to clobber edits made after the undo unless `--force`. |
| `vrs goto @ref` | Materialize any snapshot into the working copy. Current uncommitted state is captured first (nothing is ever lost). |
| `vrs log [--all] [-n N]` | Timeline: `#N  date  message`. `--all` shows hidden captures (undo/goto captures). |
| `vrs help`, `vrs version` | — |

**Scope rules:** commands locate the repo root by walking up to `.vrs/`. `save` and
`goto` always operate on the full tree (invariant). `diff`, `undo`, `redo` default to
the current directory's subtree; a path argument narrows further.

### Out (deliberately, post-v1)

Daemon/ambient auto-snapshots · machine sync · experiments/branches · `find` (generalized
bisect) · publish-to-git · VS Code extension · GC/retention.
(The schema is designed so none of these require breaking changes.)

### `@ref` syntax (v1)

- `@N` — snapshot N
- `@-N` — N snapshots back from the latest
- `@2h`, `@3d` — newest snapshot with timestamp ≤ now − duration

---

## 2. Core semantics and invariants

1. **Snapshots are full-tree.** Every snapshot stores the complete state of the repo
   (dedup makes it cheap). No partial snapshots, ever.
2. **History is append-only.** No command rewrites or deletes a snapshot. `undo` only
   touches the working copy.
3. **Capture-before-mutate.** Any command that overwrites the working copy (`undo`,
   `goto`, `redo`) first captures the current state as a hidden snapshot. Uncommitted
   work can never be lost — this replaces the daemon's safety net in v1.
4. **Non-destructive defaults.** Files that must disappear from the working copy are
   moved to `.vrs/trash/<op-timestamp>/`, never hard-deleted.
5. **Undo/redo stack (LIFO).** Each `undo` pushes a redo entry `{capture, target, scope}`;
   `redo` pops the most recent. `save` clears the whole stack. `redo` validates that
   the affected paths haven't changed since the undo completed.
6. **Saving from a past position** (after `goto`) appends a new snapshot whose parent
   is the base it was created from; v1 prints a warning ("this starts a new line").
   Old snapshots are preserved.

---

## 3. Architecture

```
cmd/vrs/            main: arg dispatch, exit codes
internal/cli/       one file per verb (save, diff, undo, redo, log, goto), @ref parsing
internal/snap/      capture engine: walk, ignore, chunk, hash → snapshot rows
                    materialize: snapshot rows → files on disk (idempotent)
internal/store/     SQLite open/migrate/pragmas, chunk put/get, refcounts, transactions
internal/diffx/    status walk (fast path), unified-diff rendering
internal/ignore/   .vrsignore + built-in defaults (gitignore-style matching)
internal/oplog/     ops journal + redo stack helpers
internal/paths/     root discovery, scope resolution, path normalization
```

Dependencies (all pure Go, no CGo):

| Dep | Use |
|---|---|
| `modernc.org/sqlite` | storage |
| `github.com/restic/chunker` | content-defined chunking (Rabin CDC, ~64KB avg) |
| `github.com/klauspost/compress/zstd` | chunk compression |
| `github.com/sergi/go-diff` | unified diff rendering |
| `github.com/sabhiram/go-gitignore` | ignore patterns |
| stdlib `flag` | CLI (7 verbs don't justify a framework) |

---

## 4. Data model (SQLite)

```sql
PRAGMA journal_mode = WAL;        -- readers + single writer
PRAGMA synchronous  = NORMAL;
PRAGMA busy_timeout  = 5000;
PRAGMA auto_vacuum   = INCREMENTAL;   -- chunk GC without VACUUM

CREATE TABLE meta (           -- schema_version, current_base (snapshot id the WC sits on)
  key TEXT PRIMARY KEY, value TEXT NOT NULL
);

CREATE TABLE snapshots (
  id     INTEGER PRIMARY KEY,          -- the human handle: #N
  ts     INTEGER NOT NULL,             -- unix nanos
  kind   TEXT NOT NULL,               -- 'save' | 'capture'   (captures hidden from log)
  message TEXT,
  parent INTEGER                       -- id of the base snapshot (line/fork detection later)
);

CREATE TABLE entries (                 -- manifest: one row per file at a snapshot
  snapshot  INTEGER NOT NULL REFERENCES snapshots(id),
  path      TEXT NOT NULL,             -- repo-relative, '/'-separated
  file_hash TEXT NOT NULL,             -- sha256 hex of full file content
  size      INTEGER NOT NULL,
  mode      INTEGER NOT NULL,
  PRIMARY KEY (snapshot, path)
) WITHOUT ROWID;

CREATE TABLE chunks (                  -- content-addressed, zstd-compressed
  hash     TEXT PRIMARY KEY,          -- sha256 hex of uncompressed chunk
  data     BLOB NOT NULL,
  raw_size INTEGER NOT NULL,
  refcount INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE file_chunks (             -- ordered chunk list per file version
  file_hash  TEXT NOT NULL,
  seq        INTEGER NOT NULL,
  chunk_hash TEXT NOT NULL,
  PRIMARY KEY (file_hash, seq)
) WITHOUT ROWID;

CREATE TABLE ops (                     -- journal of every mutating command
  id     INTEGER PRIMARY KEY,
  ts     INTEGER NOT NULL,
  kind   TEXT NOT NULL,                -- save | undo | redo | goto | init
  detail TEXT NOT NULL                 -- JSON (refs, scope, snapshot ids)
);

CREATE TABLE redo_stack (
  id      INTEGER PRIMARY KEY,         -- top = max(id)
  capture INTEGER NOT NULL,            -- pre-undo state snapshot id
  target  INTEGER NOT NULL,            -- the undo's @ref (what was materialized)
  scope   TEXT NOT NULL                -- JSON array of paths
);

CREATE TABLE wc_cache (                -- mtime/size fast path (updated on save AND materialize)
  path      TEXT PRIMARY KEY,
  mtime_ns  INTEGER NOT NULL,
  size      INTEGER NOT NULL,
  file_hash TEXT NOT NULL
);
```

**Hashing:** one streaming pass per file → chunk boundaries (CDC), per-chunk sha256,
whole-file sha256. Chunks dedup by hash; unchanged files between snapshots reuse
`file_hash` rows with zero I/O.

**Trash:** `.vrs/trash/<unix-nanos>/<path…>` — plain files, browsable by hand.

**Ignore:** `.vrsignore` (gitignore syntax) at root + built-in defaults:
`node_modules/, .git/, target/, dist/, build/, out/, __pycache__/, .venv/, venv/,
.DS_Store, *.pyc, .env*`.

---

## 5. Command flows

### `vrs save`
1. Find root (walk up) or auto-init (mkdir `.vrs/`, create DB, write default `.vrsignore`).
2. Walk tree applying ignores. For each file: compare stat vs `wc_cache`;
   changed candidates → hash + chunk; unchanged → reuse snapshot entry.
3. In one `BEGIN IMMEDIATE` transaction: upsert chunks (+refcount), insert snapshot
   (kind=save, parent=meta.base), entries, file_chunks, op row; **clear redo_stack**;
   update `wc_cache`; set meta.base = new id. Commit.
4. Print `#N saved — 12 files changed (3 added, 9 modified) — 240 KiB stored`.

### `vrs diff`
- **No path:** walk scope → per file: stat-cache hit = unchanged; else hash and compare
  to latest snapshot's entry. Report `added / modified / deleted`.
- **Path:** stream old version from chunks, read current file, render unified diff.
- `@ref` argument: compare against that snapshot instead of latest.

### `vrs undo`
1. Resolve target T (latest snapshot, or `@ref`). Scope S (cwd subtree, or path).
2. If working copy in S already equals T: `nothing to undo`, exit 0.
3. **Capture** current state of S (hidden snapshot, kind=capture) → push redo entry.
4. Materialize T onto S: overwrite modified files, restore deleted ones,
   move WC-only files in S to trash. Update `wc_cache` for S. Log op.

### `vrs redo`
1. Pop top redo entry {capture, target, scope}.
2. Hash scope paths; if any differ from T's state (user edited since undo):
   refuse with a message listing the files; `--force` overrides (but capture first — capture-before-mutate).
3. Materialize the `capture` snapshot onto scope. Log op.

### `vrs goto`
1. Resolve `@ref` → G. **Capture** entire current WC (kind=capture).
2. Materialize G onto the full tree (trash extras). Set meta.base = G. Log op.
3. Print `working copy now at #G — previous state captured (log --all)`.

### Materialize (shared)
Idempotent: per entry, if the on-disk file already has the right hash (stat fast
path), skip; else write temp + rename, chmod. Safe to re-run after interruption.

---

## 6. Milestones

### M0 — Storage core + save + log  (~2–3 days) ✅
- Store: open/migrate, pragmas, chunk put/get, refcounts, single-txn save.
- Capture engine: walk, ignore, chunk, hash, manifest.
- `vrs save`, `vrs log`.
- **Done when:** save on a real project works; DB inspectable; second save stores ~0 new bytes for unchanged files.

### M1 — The loop: diff, undo, redo  (~3–4 days) ✅
- Status walk with `wc_cache` fast path; per-file unified diff.
- Capture-before-mutate engine; trash; redo stack; invalidation rules.
- **Done when:** the full narrative works: save → edit → diff → undo → redo → save (redo cleared).

### M2 — @refs + goto  (~2 days) ✅
- @ref parser (N, -N, durations), `goto`, `log --all`, parent tracking, past-save warning.
- **Done when:** goto to older snapshot and back loses nothing; uncommitted work survives every path.

### M3 — Hardening + release  (~3 days) ✅
- `.vrsignore` handling complete; perf pass (targets: 10k-file repo — clean save <300ms, diff <300ms, 100-file change save <2s).
- E2E test suite (scripted scenarios against the built binary), fuzz chunker wrapper.
- goreleaser config (darwin arm64/amd64, linux amd64/arm64), README, `go install` works.
- **Tag `v0.1.0`.**

---

## 7. Testing strategy

- **Unit:** chunk roundtrip + dedup (same bytes → one chunk, refcount 2); ignore matcher;
  @ref parser; materialize/capture roundtrip property (materialize(capture(wc)) == wc, incl. modes).
- **Concurrency:** two simultaneous `save`s — second waits on busy_timeout, both commit serially.
- **Crash safety:** transaction boundaries mean a killed save leaves the DB at the previous snapshot; materialize re-run resumes.
- **E2E:** golden script scenarios (init-by-save, undo/redo interplay, goto round-trip, save-clears-redo, trash recovery) executed against the real binary.

---

## 8. Risks and open questions

| Risk | Mitigation |
|---|---|
| DB growth (every save stores changed bytes forever) | Dedup keeps text repos tiny (~changed bytes/save). Refcounts already in schema → GC/retention is a later feature, not a migration. |
| mtime/size fast path can miss same-size-same-mtime edits (git's tradeoff) | Document; `vrs fsck --verify` (full rehash) is a trivial post-v1 addition. |
| modernc/sqlite slower than CGo bindings | Batch all writes in one transaction per command; write volume is small. Measured in M3; acceptable worst case. |
| Windows path handling | Normalize to `/` internally from day one; v1 targets macOS/Linux, Windows is best-effort. |
| Symlinks / empty dirs | v1: symlinks ignored (documented), empty dirs untracked (git-like). |
| Module path | `github.com/<you>/vrs` — owner needed before first release; affects `go install` only. |

---

## 9. Post-v1 roadmap (order of attack)

**Shipped post-v1: agent checkpointing (v0.2.0).** `vrs capture` (hidden,
idempotent checkpoints) + `vrs mcp` (MCP stdio server: snapshot/restore/
diff/log for AI agents). Landed with zero schema changes — kind='capture'
and the @ref machinery carried the whole feature.

1. **Daemon + ambient snapshots** — the killer feature; `save`/`undo`/`diff` semantics unchanged (they target latest *named* snapshot; `@refs` reach auto-saves).
2. **`vrs find --run "cmd"`** — generalized bisect over the fine-grained timeline.
3. **Machine sync** — chunk/op-level replication, SQLite file never synced directly.
4. **Experiments** (`try` / `keep` / `toss`) — the `parent` column already supports lines.
5. **Git interop** — squash/export the main line to a git remote (renamed from `publish`; the word now carries no vrs meaning).

---

## 10. Import / Export (v0.3.0 — M4)

vrs moves recorded state between machines over SSH — the solo workflow:
snapshot → export to the server → hot-fix on the server → import back → saved.
**Never a sync engine**: both commands are single, directional, whole-state
operations. No conflict detection, no ancestry, no bidirectional anything.
If you want sync, use rsync — vrs is your undo button, not your sync engine.

### Targets

- `[user@]host:/abs/path` — SSH/SFTP. `~/.ssh/config` aliases (Hostname,
  User, Port, IdentityFile) are honored; auth is agent + keys only
  (passwords unsupported in v1); host keys are checked against
  `~/.ssh/known_hosts` and never auto-accepted.
- Local directory paths (`~` expanded, relative to cwd).

### `vrs export <target> [@ref] [--prune]`

Materialize a *recorded* state (the position, or explicit `@ref`) onto the
target — never the untracked working tree.

- Incremental via the target manifest (`.vrs-manifest.json` at the target
  root): only files whose hash or mode differ are written; full upload on
  first export. Manifest updated only after a fully successful run — an
  interrupted export leaves the target at the old consistent state.
- Target files absent from the snapshot are left alone; `--prune` moves them
  to `.vrs-trash/<unix-nanos>/` at the target — never hard-deleted.
- Refused if the snapshot itself contains the reserved names
  (`.vrs-manifest.json`, `.vrs-trash/`), or if the target is inside the
  repository (the manifest must not become tracked content).

### `vrs import <target> [--prune]`

Overlay the target directory onto the working tree, then snapshot it.

- Download set = remote files passing the repo's ignore rules (local
  `.vrsignore` + built-ins). `.vrs/` is never touched on either side;
  reserved names are never imported.
- Quick-check (rsync semantics): download only files whose size or mtime
  differ from the local copy; remote mtimes are preserved so repeats are
  incremental. Same size+mtime-but-different-content blind spot as rsync/git.
- Nothing changed → no capture, no snapshot ("nothing to import").
- Anything changed → capture-before-mutate (pre-import state is a hidden
  snapshot), overlay, then an automatic save advancing the position
  (`import from <target> — N files`). Auto-initializes on first use.
- Local files absent on the remote survive by default; `--prune` moves them
  to the repo's `.vrs/trash/`.
- Import takes no `@ref`.

### Out of scope (the fence)

Hooks, restarts, release directories, symlink flips, multi-host
orchestration, config templating, pull/conflict semantics, password auth,
rsync's delta protocol (whole changed files are shipped; history dedup keeps
the *repository* small, not the wire).