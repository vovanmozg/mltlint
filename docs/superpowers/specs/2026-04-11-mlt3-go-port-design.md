# mlt3 — Go port of mlt2 (design)

**Date:** 2026-04-09
**Status:** draft
**Source project:** `~/pro/mlt2` (Ruby)
**Target project:** `~/pro/mlt3` (Go)

## Goal

Port the mlt2 duplicate-finder utility to Go. Keep the behavior and requirements coverage of mlt2 while:

1. Eliminating the build pain around `phashion` / libpHash / `ruby-filemagic` / `sqlite3`. The Ruby version requires a patched Dockerfile to build `phashion` against modern architectures; the Go port must produce a single statically-linked binary with no external C dependencies and no Docker wrapper.
2. Improving parallelism — concurrent scanning and hashing of both sides via goroutines + worker pools.

Non-goals:

- Video support. Videos are dropped entirely (mlt2 listed video extensions but phashion only worked on images).
- Docker image. The Go binary runs natively.
- Wire-level compatibility of the phash integer value with libpHash (goimagehash produces different values by construction).

## Scope

mlt3 is a CLI utility that:

1. Scans an `originals` directory and an `unsorted` directory.
2. For each file in `unsorted`, determines whether a duplicate or near-duplicate exists in `originals`.
3. Writes two files:
   - `duplicates.json` — rmlint-compatible JSON report, extended with a `similarity` field and a `move_to` field on duplicate records.
   - `duplicates.sh` — shell script with `move_cmd` calls that move duplicates into a `dups` directory, grouped by similarity level (`similar/` or `doubtful/`).
4. Prints a human-readable summary to stderr in the style of rmlint.

mlt3 does **not** delete or move files itself — it only generates the script.

## Key decisions

| Topic | Decision | Rationale |
|---|---|---|
| Perceptual hash library | `github.com/corona10/goimagehash` (pure Go, DCT pHash 64-bit) | Pure Go, no cgo, static binary. Accept that hash values are not binary-compatible with libpHash — cache from mlt2 is unreadable, and that's fine. |
| Media types | Images only (`jpg`, `jpeg`, `png`, `gif`, `bmp`, `tif`, `tiff`, `webp`) | Video support in mlt2 was nominal — phashion did not actually hash videos. Dropping them removes dead functionality. |
| CLI model | Native binary, no Docker path-mapping magic | mlt2 used `/vt/*` docker paths and a `path_map` to rewrite them. mlt3 runs natively, so `--originals=PATH` is both the scan root and the path written to the output. |
| Cache backend | `go.etcd.io/bbolt` (pure Go KV store) | Fits the cache use case (single file, `path+mtime+size → phash`), no cgo, smaller than SQLite for the purpose. |
| Output format | rmlint-compatible JSON + sh, extended with `similarity` / `move_to` on duplicate records | Preserves interoperability with rmlint tooling; extensions are additive. |
| Mode flag | Removed. Single unified run finds both `similar` (phash distance 0) and `doubtful` (1..2) matches; level is recorded per result. | User feedback: exact byte-level matching is rmlint's job; mlt3's value is fuzzy matching; having separate modes is redundant. |
| Pipeline shape | Two-phase: phase 1 fully indexes originals, phase 2 classifies unsorted against the immutable index | Streaming classification would require revisions of preliminary results (a later `orig` could be a closer match). Two-phase guarantees correctness, supports clean SIGINT, and keeps the code simple. Phase-1 and phase-2 each run scan+hash concurrently inside themselves. |
| Project layout | Standard Go layout with `cmd/mltlint`, `internal/{scanner,comparator,cache,report}` | Matches Go community best practice; `internal/` signals "not a library"; mlt2 modules map 1:1 to packages. |
| Testing | Full requirements coverage via Go unit tests + one e2e test; mlt2 fixtures copied into `testdata/media/`; `testify/require` as the only test dependency. | User requires full confidence behavior matches mlt2. Unit tests cover each requirement; e2e covers pipeline integration. |

## Architecture

### Package layout

```
mlt3/
├── go.mod
├── cmd/mltlint/
│   ├── main.go              # CLI entry, flag parsing, orchestration, signal handling
│   └── e2e_test.go          # end-to-end tests
├── internal/
│   ├── scanner/
│   │   ├── scanner.go       # Scan, ComputeMD5, ComputePHash
│   │   └── scanner_test.go  # SCAN.*
│   ├── comparator/
│   │   ├── comparator.go    # Indexes, Classify
│   │   └── comparator_test.go  # CMP.*
│   ├── cache/
│   │   ├── cache.go         # bbolt-backed phash cache
│   │   └── cache_test.go    # CACHE.*
│   └── report/
│       ├── json.go          # WriteJSON (rmlint format + extensions)
│       ├── json_test.go     # OUT.JSON.*
│       ├── shell.go         # WriteShell (rmlint-style sh with move_cmd)
│       └── shell_test.go    # OUT.SH.*
├── docs/
│   ├── REQUIREMENTS.md      # ported from mlt2, updated for the mode removal
│   └── superpowers/specs/2026-04-09-mlt3-go-port-design.md (this file)
├── testdata/
│   └── media/               # fixtures copied from mlt2/spec/fixtures/media/
└── Makefile
```

### Package contracts

**`internal/scanner`** — "what files are on disk and what are their hashes"

```go
type FileInfo struct {
    Path         string // absolute path
    RelativePath string // path relative to scan root
    Size         int64
    Mtime        int64
    Width        int
    Height       int
    MD5          string // empty until ComputeMD5
    PHash        uint64 // zero until ComputePHash
    Err          error  // per-file soft error, or nil
}

type Scanner struct { ... }

func New(cache *cache.Cache, workers int, debug bool) *Scanner

// Walks dir, detects media files, fills size/mtime/dimensions.
// Does NOT compute MD5 or phash.
func (s *Scanner) Scan(ctx context.Context, dir string) ([]*FileInfo, error)

// Computes MD5 for files that don't have it yet, in parallel.
func (s *Scanner) ComputeMD5(ctx context.Context, files []*FileInfo) error

// Computes phash for files that don't have it yet, consulting the cache.
func (s *Scanner) ComputePHash(ctx context.Context, files []*FileInfo) error
```

**`internal/cache`** — "persistent phash cache"

```go
type Cache struct { ... }

// Opens bbolt db at path, creates bucket named after the goimagehash version.
func Open(path string) (*Cache, error)
func (c *Cache) Close() error

// Returns (hash, true) if a cached entry exists for (path, mtime, size),
// otherwise (0, false).
func (c *Cache) Get(path string, mtime, size int64) (uint64, bool)

// Stores/updates the entry.
func (c *Cache) Set(path string, mtime, size int64, hash uint64) error
```

**`internal/comparator`** — "how to decide if an unsorted file is a duplicate"

```go
type Indexes struct {
    BySize  map[int64][]*scanner.FileInfo       // for MD5 fast path candidates
    ByRatio map[float64][]*scanner.FileInfo     // grouped by rounded aspect ratio
    Empty   bool
}

type Result struct {
    Duplicate *scanner.FileInfo
    Original  *scanner.FileInfo
    Level     string // "similar" | "doubtful"
    Distance  int
    Score     float64
}

// Builds read-only indexes from a fully scanned/hashed originals slice.
func BuildIndexes(originals []*scanner.FileInfo) *Indexes

// Classifies a single unsorted file against the indexes.
// Returns nil if the file is not a duplicate of any original.
// May call hashComputer to compute MD5/phash lazily.
func Classify(
    ctx context.Context,
    file *scanner.FileInfo,
    indexes *Indexes,
    hashComputer HashComputer,
) (*Result, error)

type HashComputer interface {
    ComputeMD5(ctx context.Context, files []*scanner.FileInfo) error
    ComputePHash(ctx context.Context, files []*scanner.FileInfo) error
}
```

Classification algorithm (per unsorted file):

1. MD5 fast path. If `indexes.BySize[file.Size]` is non-empty:
    a. Ensure the file has MD5 computed (call `hashComputer.ComputeMD5` on `[]*FileInfo{file}` if missing).
    b. Ensure each size-matched original has MD5 computed (call `hashComputer.ComputeMD5` on the subset that's missing it). Populate into the `FileInfo.MD5` field directly — no shared MD5 index is maintained.
    c. If any candidate matches `file.MD5` → `Result{Original: <first match in stable path order>, Level: "similar", Distance: 0, Score: 1.0}` and return. (Byte-identical ⇒ visually identical, skip phash.)
2. Ensure file's phash is computed.
3. Look up `indexes.ByRatio[file.Ratio()]`. If the ratio bucket is empty, return `nil`.
4. For each candidate in the ratio bucket, compute `hamming(file.PHash, cand.PHash)`. Track the minimum-distance match.
5. If min distance == 0 → `Level: "similar"`, `Score: 1.0`.
6. If min distance ∈ {1, 2} → `Level: "doubtful"`, `Score: 1.0 - distance/3.0` (0.67 for dist=1, 0.33 for dist=2, rounded to 2 decimals).
7. Otherwise return `nil`.

Step 1b may cause multiple classifier invocations to ask for MD5 on the same original files; `FileInfo.MD5` is set once and reused, so repeated requests are near-free. Since phase 2 runs classifier in a single goroutine, no locking is needed around `FileInfo.MD5` assignment.

Note on similar vs doubtful ratio filter: mlt2 applies the aspect ratio filter only in the doubtful stage (`find_closest_match`). For mlt3, since we're doing a single pass that finds both, we apply the ratio filter uniformly for any phash-based match. Rationale: if two images have genuinely different aspect ratios, they are not the same image, regardless of the distance. This is a small semantic tightening vs mlt2's `find_similar` (which didn't check ratio), justified by correctness — a phash collision across different aspect ratios is a false positive by nature. MD5 fast path is not affected — byte-identical files always match, ratios are equal by definition.

**`internal/report`** — "how to format the output"

```go
type Group struct {
    Original   *scanner.FileInfo
    Duplicates []*Result            // all results pointing at Original
}

type Stats struct {
    TotalFiles     int
    IgnoredFiles   int
    IgnoredFolders int
    Aborted        bool
    Progress       int
}

// Pure functions — no filesystem access.
func WriteJSON(w io.Writer, cfg Config, groups []*Group, stats Stats) error
func WriteShell(w io.Writer, cfg Config, groups []*Group) error
func WriteConsoleSummary(w io.Writer, cfg Config, groups []*Group, stats Stats, elapsed time.Duration) error

type Config struct {
    Args           string
    Cwd            string
    Originals      string
    Unsorted       string
    Dups           string
}
```

**`cmd/mltlint`** — orchestration

```go
func main() {
    cfg := parseFlags()
    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer cancel()

    c, err := cache.Open(cfg.CachePath)
    must(err); defer c.Close()

    sc := scanner.New(c, cfg.Workers, cfg.Debug)

    // Phase 1: originals — scan + compute phash only (MD5 computed lazily in phase 2)
    origFiles, err := sc.Scan(ctx, cfg.Originals)
    must(err)
    must(sc.ComputePHash(ctx, origFiles))
    indexes := comparator.BuildIndexes(origFiles)

    // Phase 2: unsorted — scan + compute phash; MD5 is computed on demand inside Classify
    unsortedFiles, err := sc.Scan(ctx, cfg.Unsorted)
    must(err)
    must(sc.ComputePHash(ctx, unsortedFiles))

    groups := classifyAndGroup(ctx, unsortedFiles, indexes, sc)

    // Output
    stats := computeStats(origFiles, unsortedFiles, groups, ctx.Err() != nil)
    writeOutputs(cfg, groups, stats)
    writeSummary(os.Stderr, cfg, groups, stats)
}
```

Why phase 1 skips MD5: MD5 is only useful for detecting byte-identical matches, and we don't know which originals will have size-collisions until we start processing unsorted. Phase 2's classifier computes MD5 on demand for each unsorted file and its size-matched originals. Originals that never collide with any unsorted file never get their MD5 computed.

### Data flow

```
┌──────────────── Phase 1: originals ────────────────┐
│                                                    │
│  scan(originals) ──► hashWorkers (phash only)     │
│                      (N workers, cache-aware)      │
│                                │                   │
│                                ▼                   │
│                        BuildIndexes()              │
│                        ├─ BySize                   │
│                        └─ ByRatio                  │
└────────────────────────────────────────────────────┘
                     │
                     ▼ (indexes immutable)
┌──────────────── Phase 2: unsorted ─────────────────┐
│                                                    │
│  scan(unsorted) ──► hashWorkers (phash) ──┐       │
│                                           ▼        │
│                                      classifier    │
│                                        (md5 on     │
│                                        demand)     │
│                                           │        │
│                                           ▼        │
│                                       collector    │
│                                    (groups by orig)│
└────────────────────────────────────────────────────┘
                     │
                     ▼
┌──────────── Finalization ──────────────┐
│ WriteJSON(duplicates.json)              │
│ WriteShell(duplicates.sh)               │
│ WriteConsoleSummary(stderr)             │
└─────────────────────────────────────────┘
```

Within each phase, the scanner walks the filesystem in a goroutine, pushing `*FileInfo` into a channel. N hash workers read from the channel, compute hashes (consulting the cache), and push results into an output channel. In phase 2 a single classifier goroutine reads hashed files from that channel and performs the lookup against the immutable indexes.

Number of workers defaults to `runtime.NumCPU()`, configurable via `--workers=N`.

### Progress reporting

Phase 1 (rewritten line in stderr):
```
[phase 1/2] originals: scanned 1245, hashed 1200 (cache: 1150, computed: 50)
```

Phase 2:
```
[phase 2/2] unsorted: scanned 892, classified 880, found: 12 similar, 3 doubtful
```

Console summary after finalization (written to stderr, then the output paths):
```
# Duplicate(s):
    original '/home/user/media/originals/1.png'
    similar  '/home/user/media/unsorted/unso1.png' (dist=0)
    doubtful '/home/user/media/unsorted/unso2.png' (dist=2)

    original '/home/user/media/originals/beach.jpg'
    similar  '/home/user/media/unsorted/beach_copy.jpg' (dist=0)

==> Note: Please use the saved script below for moving, not the above output.
==> In total 5 files, whereof 3 are duplicates in 2 groups.
==> This equals 221.45 KB of duplicates which could be moved.
==> Scanning took in total 12.3s.

Wrote a json file to: /home/user/media/duplicates.json
Wrote a sh file to: /home/user/media/duplicates.sh
```

## Output format

### `duplicates.json`

rmlint-compatible flat JSON array: header, then `duplicate_file` records, then footer.

**Header:**
```json
{
  "description": "mlt3 duplicate report",
  "cwd": "/home/user/media",
  "args": "mltlint --originals=... --unsorted=... --dups=...",
  "version": "1.0.0",
  "rev": "",
  "progress": 0,
  "checksum_type": "phash-dct64"
}
```

**Duplicate_file record** (for each file in each group — both originals and duplicates):
```json
{
  "id": 266095130,
  "type": "duplicate_file",
  "progress": 100,
  "checksum": "d41d8cd98f00b204e9800998ecf8427e",
  "path": "/home/user/media/unsorted/2023/photo.jpg",
  "size": 73884,
  "twins": 3,
  "depth": 2,
  "inode": 16527604,
  "disk_id": 2049,
  "is_original": false,
  "mtime": 1484308666,
  "similarity": { "level": "similar", "distance": 0, "score": 1.0 },
  "move_to": "/home/user/media/dups/similar/2023/photo.jpg"
}
```

Rules:

- **`id`**: `crc32.ChecksumIEEE([]byte(Path))` — deterministic, stable across runs.
- **`checksum`**: MD5 hex string if MD5 was computed for this file; empty string otherwise (e.g., a `doubtful` match that went the phash-only path).
- **`twins`**: total number of records in the group (originals + duplicates).
- **`is_original`**: `true` for the original, `false` for the duplicate.
- **`similarity`**: only present on records with `is_original: false`.
- **`move_to`**: only present on records with `is_original: false`. Value: `{dups}/{level}/{relativePath}`.
- **`inode`/`disk_id`**: from `syscall.Stat_t` on unix. 0 if unavailable.
- **`depth`**: number of path segments in RelativePath (`strings.Count(rel, "/") + 1`).
- **`progress`**: `100` under normal completion. Under `aborted`, the same value everywhere reflecting the progress at finalization time.
- Records are ordered: all records of group 1 consecutively (original first, then its duplicates in scan order), then group 2, etc.

**Footer:**
```json
{
  "aborted": false,
  "progress": 100,
  "total_files": 4,
  "ignored_files": 0,
  "ignored_folders": 0,
  "duplicates": 2,
  "duplicate_sets": 1,
  "total_lint_size": 147768
}
```

- `total_files`: all scanned files across both directories
- `ignored_files`: soft-skip count (empty files, non-media, decode errors, permission errors on files)
- `ignored_folders`: permission errors on directories
- `duplicates`: count of records with `is_original: false`
- `duplicate_sets`: count of unique originals that have at least one duplicate
- `total_lint_size`: sum of `size` of duplicate records

### `duplicates.sh`

```bash
#!/usr/bin/env bash
# This file was autogenerated by mlt3 mltlint
# Your command line was: mltlint --originals=... --unsorted=... --dups=...

DUPS_DIR="/home/user/media/dups"

handle_emptydir() { :; }
handle_emptyfile() { :; }
original_cmd() { :; }

move_cmd() {
  local duplicate="$1"
  local original="$2"
  local dest="$3"
  if [ ! -e "$original" ]; then
    echo "SKIP (original missing): $original"
    return
  fi
  local dest_dir
  dest_dir="$(dirname "$dest")"
  mkdir -p "$dest_dir"
  mv "$duplicate" "$dest"
}

######### START OF AUTOGENERATED OUTPUT #########

original_cmd '/home/user/media/originals/1.png' # original
move_cmd '/home/user/media/unsorted/unso1.png' '/home/user/media/originals/1.png' '/home/user/media/dups/similar/unso1.png' # similar (dist=0)
move_cmd '/home/user/media/unsorted/unso2.png' '/home/user/media/originals/1.png' '/home/user/media/dups/doubtful/unso2.png' # doubtful (dist=2)

original_cmd '/home/user/media/originals/beach.jpg' # original
move_cmd '/home/user/media/unsorted/beach_copy.jpg' '/home/user/media/originals/beach.jpg' '/home/user/media/dups/similar/beach_copy.jpg' # similar (dist=0)

######### END OF AUTOGENERATED OUTPUT #########
```

Rules:

- `#!/usr/bin/env bash` shebang.
- `set -e` is **not** used — we want move errors to be visible but not to abort the whole run.
- `original_cmd` is a no-op marker for rmlint compatibility.
- `move_cmd` is the only function that actually does work; checks for original's presence and moves the duplicate into `dest`, creating parent directories as needed.
- Records are grouped: `original_cmd` for the original, followed by `move_cmd` calls for all its duplicates, then a blank line before the next group.
- Level is encoded both in the destination path (`.../dups/similar/...` vs `.../dups/doubtful/...`) and in the trailing comment (`# similar (dist=0)` or `# doubtful (dist=2)`).

## CLI

```
mltlint --originals=PATH --unsorted=PATH --dups=PATH [options]

Required:
  --originals=PATH     Directory with existing originals
  --unsorted=PATH      Directory with files to check for duplicates
  --dups=PATH          Directory into which duplicates should be moved
                       (used in move_cmd destinations; not created by mltlint)

Options:
  --output=DIR         Where to write duplicates.json and duplicates.sh
                       (default: current working directory)
  --cache=PATH         Path to the phash cache file
                       (default: $XDG_CACHE_HOME/mltlint/cache.db, or ~/.cache/mltlint/cache.db)
  --workers=N          Number of hash workers (default: runtime.NumCPU())
  --debug              Verbose logging to stderr
  --help               Print usage
```

No `--mode` flag. No `--originals`/`--unsorted` dual (docker-path + host-path) — CLI paths are both scanned and written into outputs directly.

## Error handling

### Fatal errors (exit 1)

- Missing or invalid required flags.
- `--originals`, `--unsorted`, or `--dups` does not exist / is not a directory / is not readable.
- Cannot open the bbolt cache (permission denied, corrupted, held by another process).
- Cannot write output files.

Each fatal error is printed to stderr with the cause, then the process exits with code 1.

### Per-file soft errors (log, skip, continue)

- File removed between scan and hash (`os.IsNotExist`).
- Permission denied on a specific file.
- Image decode failure (truncated, corrupted).
- phash computation failure.
- MD5 failure (I/O error mid-read).

Each soft error produces a stderr line: `[skip] <path>: <cause>`. The file is excluded from further processing and from the report. A running counter is included in the footer's `ignored_files`.

### Warnings (log, skip, no error)

- Empty file (`SCAN.SKIP.EMPTY`).
- Content type not recognized as media (`SCAN.SKIP`).
- Image with zero dimensions (phash impossible).
- Stale cache entry (silent refresh).

### Graceful shutdown (SIGINT / SIGTERM)

- `signal.NotifyContext` produces a cancellable context.
- All scanners, workers, and the classifier check `ctx.Done()` in their loops.
- First signal → cancel context → goroutines finish their current file and exit → finalize:
    - If phase 1 was interrupted: no results to write, exit code 130.
    - If phase 2 was interrupted: write `duplicates.json` (with `aborted: true, progress: classified/scanned`) and `duplicates.sh` with the groups collected so far. Exit code 130.
- **Second signal** → `os.Exit(130)` immediately, no finalization.

### Atomic output writes

`duplicates.json` and `duplicates.sh` are first written to sibling temp files (`duplicates.json.tmp`), then renamed into place. If write fails mid-way, the temp file is deleted and the original (if any) is preserved.

### Edge cases

| ID | Situation | Behavior |
|---|---|---|
| EC1 | Empty originals | Phase 1 produces empty indexes; phase 2 runs and finds nothing; empty-but-valid report is written. |
| EC2 | Empty unsorted | Similar — report is empty. |
| EC3 | Duplicate MD5s inside originals | When the unsorted file hits the MD5 fast path, multiple originals may match. Classification picks the first in stable path order. The other original is invisible in the report (we do not detect originals-vs-originals duplicates; that's rmlint's job). |
| EC4 | Multiple originals close by phash to one unsorted | `Classify` picks the minimum-distance match; ties broken by stable path order. |
| EC5 | Unsorted file has phash but dimensions=0 | Ratio is undefined; file can only match via MD5 fast path. Phash comparison is skipped for this file. |
| EC6 | Cache bucket from a prior goimagehash version | Bucket name encodes the goimagehash version; old bucket is simply ignored, not deleted, on a version bump. |
| EC7 | Symlinks | `filepath.Walk` does not follow directory symlinks; regular file symlinks are followed via `os.Stat`. |
| EC8 | File appears during scan | Not observed in this run; will be picked up next run. |
| EC9 | File removed between scan and hash | Soft error, skipped, logged. |
| EC10 | Two identical files inside unsorted | Both classified independently, both appear in the report under the same original. |

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Success, no soft errors |
| 1 | Fatal error |
| 2 | Completed, but one or more soft errors occurred (`ignored_files > 0`) |
| 130 | Interrupted by SIGINT or SIGTERM |

## Testing strategy

### Methodology: strict TDD (Red-Green-Refactor)

All implementation follows strict TDD: **no production code without a failing test first**.

For each requirement ID in `docs/REQUIREMENTS.md`:
1. **RED** — write a test that expresses the requirement, run it, watch it fail for the right reason (feature missing, not typo/compile error).
2. **GREEN** — write the minimal production code to make the test pass. No extras, no "while I'm here" improvements.
3. **REFACTOR** — clean up duplication, improve names, extract helpers. All tests stay green.
4. **Next requirement** — repeat.

Packages are implemented bottom-up by dependency order (cache → scanner → comparator → report → cmd). Within each package, requirements are tackled one-by-one in TDD cycles. E2e tests are written last, after all internal packages are green.

Parallelism internals (worker pools, channels) are not directly unit-tested for race conditions — `go test -race` catches those. Unit tests verify the contract ("after `ComputePHash`, all files have non-zero PHash"), not the concurrency mechanics.

### Goals

- Every requirement in `docs/REQUIREMENTS.md` is covered by at least one test.
- Test names include the requirement ID so the mapping is explicit.
- Every test was observed failing (RED) before the production code was written.
- Core logic (comparator, cache, formatters) is covered by fast unit tests with hardcoded inputs.
- Scanner is covered by tests against real fixture files.
- One end-to-end test runs the built binary against real fixtures.

### Files

```
internal/scanner/scanner_test.go     # SCAN.*
internal/cache/cache_test.go         # CACHE.*
internal/comparator/comparator_test.go  # CMP.*
internal/report/json_test.go         # OUT.JSON.*
internal/report/shell_test.go        # OUT.SH.*
cmd/mltlint/e2e_test.go              # end-to-end
testdata/media/                      # fixtures copied from ~/pro/mlt2/spec/fixtures/media/
```

### Naming convention

`TestXxx_YYY` where `Xxx_YYY` is the requirement ID with dots replaced by underscores, e.g.:

- `TestScan_FIND` → `SCAN.FIND`
- `TestScan_SKIP_EMPTY` → `SCAN.SKIP.EMPTY`
- `TestCmp_DOUBT_RATIO` → `CMP.DOUBT.RATIO`
- `TestOutJSON_HDR` → `OUT.JSON.HDR`

Sub-cases use `t.Run("subcase name", ...)`.

### Hardcoded vs fixture-based tests

**Hardcoded** (unit tests for logic):

- `comparator_test.go` — constructs `FileInfo{MD5: "aaa", PHash: 100, Width: 250, Height: 250}` directly; tests the classification logic without touching the filesystem or the real phash library.
- `cache_test.go` — uses `t.TempDir()` and bbolt directly; tests get/set/invalidation with hardcoded (path, mtime, size, hash) tuples.
- `report/json_test.go`, `report/shell_test.go` — pass prebuilt `Group`/`Result` objects to the formatters; assert on the output bytes (parsed JSON for robustness).

**Fixture-based**:

- `scanner_test.go` only. Uses `testdata/media/` with the same images copied from mlt2. Assertions avoid depending on specific phash bit patterns (only check that the phash is non-zero after compute).

### `testdata/media/`

Copied verbatim from `~/pro/mlt2/spec/fixtures/media/`:

| File | Purpose |
|---|---|
| `1.jpg` (250×250) | Base case — valid image |
| `2 smaller.jpg` | Same image, smaller — tests similar match |
| `6 broken.jpg` | Non-image content with .jpg extension → SCAN.SKIP |
| `7 broken.jpg` | Empty file → SCAN.SKIP.EMPTY |
| `8 broken phash.gif` | Valid gif but phashion used to choke on it — used for error-handling coverage |
| `9.png` | Unique image |
| `10.jpg` | Unique image |
| `dull.jpg` | Byte-identical to `1.jpg` → MD5 fast path |

### End-to-end tests

`cmd/mltlint/e2e_test.go`:

- `TestE2E_BasicFlow` — builds the binary, runs it against a temp copy of `testdata/media/` in both `originals/` and `unsorted/`, parses the resulting `duplicates.json` and `duplicates.sh`, asserts:
    - JSON has valid header/footer.
    - At least one `is_original: true` record.
    - At least one `is_original: false` record with `similarity.level ∈ {similar, doubtful}`.
    - `duplicates.sh` starts with shebang, defines `move_cmd`, contains at least one `original_cmd` and one `move_cmd`.
- `TestE2E_AbortedRun` — launches the binary via `exec.CommandContext` with a context that is cancelled after N milliseconds, asserts that if any output files were produced, they have `aborted: true`.
- `TestE2E_EmptyOriginals` — empty `originals/`, normal `unsorted/`, asserts empty-but-valid report and exit code 0.

### Tooling

- `testing` — standard library.
- `github.com/stretchr/testify/require` — readable assertions. Single test dependency.
- `go test -race -cover ./...` in CI.
- Coverage target: ≥ 80% on `internal/` packages.

### Non-goals for tests

- Specific phash bit patterns (depends on goimagehash version).
- Performance benchmarks (out of scope for this port).
- Concurrency race-condition tests (relied on `go test -race`).

## Requirements delta vs mlt2

Going from mlt2's `docs/REQUIREMENTS.md` to mlt3's:

### Removed

- `CMP.EXACT` — no exact mode.
- `CMP.EXACT.ONLY` — no exact mode.
- `CMP.ALL` — no all mode.
- `CMP.ALL.NODUP` — no staged pipeline.

### Added

- `CMP.SIM.MD5` — If MD5 of two files matches, classifier marks them as similar (distance=0, score=1.0) without computing phash. This is both an optimization and a correctness guarantee — byte-identical files are always similar.
- `CMP.CLOSEST` — For each unsorted file, the closest original (minimum hamming distance) is selected; ties are broken by stable path order.
- `OUT.JSON.TWINS` — Each duplicate_file record includes a `twins` field equal to the group size (original + all its duplicates).
- `OUT.JSON.ORIG` — For each duplicate group, the original is emitted as its own `is_original: true` record before its duplicates.
- `OUT.SH.ORIGCMD` — For each duplicate group, the shell script emits an `original_cmd '...'` marker line before the group's `move_cmd` lines.
- `OUT.SH.GROUP` — Move commands are grouped by original (one blank line between groups), not by level.

### Unchanged

All SCAN.*, CACHE.*, OUT.JSON.* (except the added above), OUT.SH.* (except the added above), CMP.SIM, CMP.SIM.NODUP, CMP.DOUBT, CMP.DOUBT.RATIO, CMP.DOUBT.RATIO.OK, CMP.SCORE, CMP.EMPTY remain semantically equivalent; their tests are ported to Go with the same logical cases.

## Dependencies

Only four external Go modules:

| Module | Purpose |
|---|---|
| `github.com/corona10/goimagehash` | Perceptual hash (DCT pHash 64-bit) |
| `go.etcd.io/bbolt` | Embedded KV store for the phash cache |
| `github.com/stretchr/testify` | Test assertions (test-only) |
| `golang.org/x/sys` | `syscall.Stat_t` for inode/disk_id (transitive, if needed) |

Image decoding uses the standard library's `image/jpeg`, `image/png`, `image/gif`. Additional decoders (webp, bmp, tiff) come from `golang.org/x/image` (official Go sub-repo, not an external dep in spirit). Content type detection: `net/http.DetectContentType` on the first 512 bytes.

`CGO_ENABLED=0` build produces a fully static binary.

## Build and run

```makefile
# Makefile

.PHONY: test lint build clean

test:
	go test -race -cover ./...

lint:
	golangci-lint run

build:
	CGO_ENABLED=0 go build -o bin/mltlint ./cmd/mltlint

clean:
	rm -rf bin/
```

## Open questions

None at the time of writing. All decisions have been made through the brainstorming conversation.

## Implementation order (TDD, bottom-up)

### Step 0 — Scaffold

Scaffold project structure (no production logic): `go.mod`, `cmd/mltlint/main.go` stub, `internal/` package dirs, `Makefile`, `.gitignore`, `testdata/media/` (copied from mlt2). Port `docs/REQUIREMENTS.md` from mlt2 with the delta above.

### Step 1 — `internal/cache` (TDD cycles)

No internal dependencies. Each cycle = one requirement → RED → GREEN → REFACTOR.

```
CACHE.MISS       → TestCache_MISS       → ...
CACHE.HIT        → TestCache_HIT        → ...
CACHE.INV.MTIME  → TestCache_INV_MTIME  → ...
CACHE.INV.SIZE   → TestCache_INV_SIZE   → ...
CACHE.UPD        → TestCache_UPD        → ...
```

### Step 2 — `internal/scanner` (TDD cycles)

Depends on: `internal/cache`. Uses `testdata/media/` fixtures.

```
SCAN.SKIP.EMPTY  → TestScan_SKIP_EMPTY  → ...
SCAN.SKIP        → TestScan_SKIP        → ...
SCAN.FIND        → TestScan_FIND        → ...
SCAN.META        → TestScan_META        → ...
SCAN.DIM         → TestScan_DIM         → ...
SCAN.RELPATH     → TestScan_RELPATH     → ...
SCAN.LAZY.MD5    → TestScan_LAZY_MD5    → ...
SCAN.LAZY.PHASH  → TestScan_LAZY_PHASH  → ...
SCAN.MD5         → TestScan_MD5         → ...
SCAN.PHASH       → TestScan_PHASH       → ...
```

### Step 3 — `internal/comparator` (TDD cycles)

Depends on: `internal/scanner` (types only). All tests use hardcoded `FileInfo` — no FS access. `HashComputer` interface mocked with a trivial in-memory implementation.

```
CMP.EMPTY        → TestCmp_EMPTY        → ...
CMP.SIM          → TestCmp_SIM          → ...
CMP.SIM.MD5      → TestCmp_SIM_MD5      → ...
CMP.SIM.NODUP    → TestCmp_SIM_NODUP    → ...
CMP.DOUBT        → TestCmp_DOUBT        → ...
CMP.DOUBT.RATIO  → TestCmp_DOUBT_RATIO  → ...
CMP.DOUBT.RATIO.OK → TestCmp_DOUBT_RATIO_OK → ...
CMP.CLOSEST      → TestCmp_CLOSEST      → ...
CMP.SCORE        → TestCmp_SCORE        → ...
```

### Step 4 — `internal/report` (TDD cycles)

No internal dependencies (takes data types from comparator). Tests pass prebuilt `Group`/`Result` objects.

```
OUT.JSON.HDR     → TestOutJSON_HDR      → ...
OUT.JSON.FTR     → TestOutJSON_FTR      → ...
OUT.JSON.REC     → TestOutJSON_REC      → ...
OUT.JSON.SIM     → TestOutJSON_SIM      → ...
OUT.JSON.ORIG    → TestOutJSON_ORIG     → ...
OUT.JSON.TWINS   → TestOutJSON_TWINS    → ...
OUT.JSON.HOST    → TestOutJSON_HOST     → ...
OUT.SH           → TestOutSH            → ...
OUT.SH.FUNC      → TestOutSH_FUNC      → ...
OUT.SH.ORIGCMD   → TestOutSH_ORIGCMD   → ...
OUT.SH.GROUP     → TestOutSH_GROUP     → ...
OUT.SH.CHK       → TestOutSH_CHK       → ...
OUT.SH.CMD       → TestOutSH_CMD       → ...
OUT.SH.CMT       → TestOutSH_CMT       → ...
OUT.SH.HOST      → TestOutSH_HOST      → ...
```

### Step 5 — `cmd/mltlint` (orchestration + e2e)

Wire flag parsing, two-phase pipeline, signal handling, progress reporting, console summary. Minimal unit tests for flag parsing; main validation via e2e tests.

```
TestE2E_BasicFlow       → full successful run against testdata
TestE2E_EmptyOriginals  → degenerate case, empty report, exit 0
TestE2E_AbortedRun      → context cancel, aborted: true in output
```

### Step 6 — Final verification

- `go test -race -cover ./...` passes with ≥ 80% coverage on `internal/` packages.
- `golangci-lint run` passes.
- `CGO_ENABLED=0 go build -o bin/mltlint ./cmd/mltlint` produces a static binary.
- Smoke-test the binary against real media directories.
