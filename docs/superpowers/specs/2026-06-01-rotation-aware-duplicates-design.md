# Rotation-aware duplicate detection (design)

**Date:** 2026-06-01
**Status:** draft
**Project:** `~/pro/mlt3` (Go)

## Goal

Make mltlint find duplicates that have been rotated. Today the perceptual hash (DCT pHash from `goimagehash`) is not rotation-invariant: a 90° copy produces a completely different hash and also swaps width/height, so it escapes both the pHash comparison and the aspect-ratio candidate index. The README already promises to catch rotated photos ("the same photo might have been rotated"), but the algorithm does not deliver it. This work closes that gap for the **four cardinal rotations** (0/90/180/270°).

Non-goals:

- Mirror/flip variants (the full dihedral group D4). A mirrored photo is a different image, not a duplicate.
- Arbitrary-angle rotation (5°, 45°, …). That requires feature-based matching (ORB/SIFT) or a rotation-invariant transform — a different class of tool.
- EXIF-orientation normalization as a separate path. Physical-rotation matching subsumes the common phone-photo case regardless of EXIF.
- Reporting the matched rotation angle. Output format (JSON/console/shell) is unchanged.

## Scope

For each original, mltlint computes the perceptual hash of all four cardinal orientations and indexes them. An unsorted file keeps a single hash. A match `U = rotate(O)` is found because one of `O`'s four orientation hashes coincides with `U`'s natural hash. Classification levels and scores are unchanged: distance 0 → `similar`, distance 1–2 → `doubtful`.

Rotation handling is **always on** — no flag.

## Key decisions

| Topic | Decision | Rationale |
|---|---|---|
| Which side carries rotations | Originals carry 4 orientation hashes; unsorted keeps 1 hash (Approach B) | User re-runs the same unsorted set repeatedly and has no cache-size limit. Originals are hashed once and fully cached, so every run after the first is a pure index lookup + hamming compare with zero rotation work at query time. |
| Rotation set | 4 cardinal rotations (0/90/180/270°) | Covers the real-world cases (phone/manual rotation). Axis-aligned rotations keep pHash within the existing strict distance threshold. |
| Cache format | Unchanged. Each rotation is a separate entry under a derived key: same `(mtime, size)`, path with a NUL-separated suffix `path + "\x00rNNN"`. 0° keeps the plain path. | Value stays an 8-byte uint64, bucket stays `phash-v1`, key layout unchanged → old caches stay valid, no migration. NUL is forbidden in filesystem paths, so the suffix cannot collide with a real file. `cache.go` needs zero edits. |
| Cache miss handling | `ComputePHashRotations` checks all 4 keys first; if all hit, the file is **not decoded at all** | The repeated-run fast path. First run decodes once and fills 4 entries; later runs read 4 cached hashes and skip decode entirely. |
| Aspect-ratio index | 90°/270° hashes are indexed under `RatioSwapped(O) = round(H/W*10)/10`, computed from dimensions (not as `1/Ratio`) | A 90° rotation swaps W/H, so the rotated orientation lives in the inverted ratio bucket. Computing from raw dimensions avoids compounding the rounding already applied by `Ratio`. |
| `Indexes.ByRatio` type | Changes from `[]*scanner.FileInfo` to `[]rotCandidate` where `rotCandidate{ hash uint64; original *scanner.FileInfo }` | Each candidate entry must carry a specific orientation's hash while still pointing back to the original file for reporting. A bare `*FileInfo` (single `PHash`) cannot represent that. |
| Memory | Rotations are computed one at a time: rotate → hash → free → next | Peak memory stays ~2× the decoded image per worker; worker count is already bounded. Avoids holding 4 full-resolution bitmaps at once. |
| Classification thresholds | Unchanged (0 → similar, 1–2 → doubtful) | An axis-aligned rotation of an exact copy yields distance ~0; rotation + resave stays small because pHash is robust to recompression. Validated by an e2e fixture rather than assumed. |

## Architecture

### Pipeline (changes to `cmd/mltlint/main.go`)

```
Phase 1 (originals):  Scan → ComputePHashRotations(origFiles) → BuildIndexes
Phase 2 (unsorted):   Scan → ComputePHash(unsortedFiles)          # unchanged, 1 hash
Classify:             Classify(U) against the immutable index
```

Only the phase-1 hashing call changes (`ComputePHash` → `ComputePHashRotations`). The two-phase shape, signal handling, and grouping are untouched.

### `internal/cache` — no code changes

The rotation discriminator lives in the `path` string passed to the existing `Get`/`Set`:

- 0° → plain `path` (identical to today's key) → reuses existing cache entries.
- 90/180/270° → `path + "\x00r90"` / `"\x00r180"` / `"\x00r270"`.

Same bucket `phash-v1`, same 16-byte `(mtime,size)` prefix, same 8-byte value. The stale-key cleanup in `Set` stays correct: the 0° key and the rotation keys have different path-byte lengths, so a `Set` for one rotation never matches/deletes another's key.

Example — two entries for `/photos/originals/IMG_1234.jpg` (`mtime=1700000000`=0x6553F100, `size=84213`=0x0148F5):

```
0°:   key   = 00 00 00 00 65 53 F1 00 | 00 00 00 00 00 01 48 F5 | "/photos/originals/IMG_1234.jpg"
      value = 9F 3A 1C 00 55 AA BB CD                                  (pHash 0°, 8B BE)

90°:  key   = 00 00 00 00 65 53 F1 00 | 00 00 00 00 00 01 48 F5 | "/photos/originals/IMG_1234.jpg\x00r90"
      value = 11 22 33 44 55 66 77 88                                  (pHash 90°, 8B BE)
```

### `internal/scanner`

- `FileInfo` gains `PHash90, PHash180, PHash270 uint64`, populated only for originals. `PHash` remains the 0° hash and is the only hash used for unsorted files.
- New method `ComputePHashRotations(ctx, files)`:
  1. For each file, look up the 4 cache keys (plain + 3 NUL-suffixed). If all 4 hit, fill the fields and return — no decode.
  2. Otherwise decode once, compute the missing orientations (rotate one at a time), fill the fields, and `Set` each missing entry under its key.
- A small axis-aligned rotation helper (90/180/270°) on a decoded `image.Image`, no new dependency.

### `internal/comparator`

- `Indexes.ByRatio` becomes `map[float64][]rotCandidate`.
- `BuildIndexes(originals)`: for each original `O` (iterated in `Path`-sorted order, as today), append:
  - `{O.PHash, O}` and `{O.PHash180, O}` to bucket `Ratio(O)`,
  - `{O.PHash90, O}` and `{O.PHash270, O}` to bucket `RatioSwapped(O)`.
- `Classify(ctx, file, indexes, hc)`:
  - MD5 fast path via `BySize` — unchanged.
  - pHash path: still computes the unsorted file's own single pHash via `hc` (unchanged). It no longer calls `hc` on candidates — candidate hashes are precomputed and embedded in the index by `BuildIndexes`. Look up `indexes.ByRatio[Ratio(file)]`, compute `min hamming(file.PHash, cand.hash)`, track the best original with strict `<` (first-wins → tie broken by path order, preserved because each original's variants are appended contiguously in path order). Level/score by distance, unchanged.

### Edge cases

- **Square images** (`Ratio == RatioSwapped == 1.0`): all 4 variants land in bucket 1.0; min hamming resolves it.
- **Self-match**: impossible — only unsorted is classified against the originals index.
- **Tie-break**: `CMP.CLOSEST` preserved (see Classify above).

## Requirements (to add to `docs/REQUIREMENTS.md`, then `req-sync`)

- **SCAN.PHASH.ROT** — for an original, hashes of 0/90/180/270° are computed from a single decode.
- **CACHE.ROT** — rotations are cached under derived NUL-suffixed keys; value format and bucket are unchanged; old caches stay valid; when all 4 keys hit, no decode occurs.
- **CMP.ROT** — an original rotated 90/180/270° and placed in unsorted is matched (similar, distance ~0).
- **CMP.ROT.RATIO** — a rotation that inverts the aspect ratio is still found (inverted ratio bucket in the index).
- **CMP.CLOSEST** — reword: multiple candidate entries may point to one original; closest original wins, ties broken by stable path order.

## Testing

- `comparator_test.go` — unit: original with 4 orientation hashes indexed, unsorted hash equal to a rotated variant → match; ratio-inversion lookup; tie-break with multiple entries per original.
- `scanner_test.go` — `ComputePHashRotations` fills all 4 fields; a second call serves from cache with no decode.
- `e2e_test.go` — at test setup, physically rotate a real `testdata` image 90°, place it in unsorted, run the CLI, assert it lands in duplicates. This is the empirical check that an axis-aligned rotation stays within threshold.
