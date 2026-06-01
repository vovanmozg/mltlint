# mlt3 Requirements

mltlint — CLI utility that scans an unsorted directory, finds images that are duplicates or near-duplicates of images in an originals directory, and generates a JSON report and shell script for moving duplicates. Works natively (no Docker).

## SCAN

Scanning directories and collecting image metadata.

## SCAN.FIND

All image files (jpg, jpeg, png, gif, bmp, tif, tiff, webp) in a directory are found during scanning.
internal/scanner/scanner_test.go:TestScan_FIND

## SCAN.META

For each file, size, modification time, and relative path are collected.
internal/scanner/scanner_test.go:TestScan_META

## SCAN.DIM

For each image, width and height are determined.
internal/scanner/scanner_test.go:TestScan_DIM

## SCAN.RELPATH

The relative path is stored from the root of the scanned directory.
internal/scanner/scanner_test.go:TestScan_RELPATH

## SCAN.SKIP

Files that are not images (by content, not by extension) are skipped.
internal/scanner/scanner_test.go:TestScan_SKIP

## SCAN.SKIP.EMPTY

Empty files (0 bytes) are skipped during scanning.
internal/scanner/scanner_test.go:TestScan_SKIP_EMPTY

## SCAN.LAZY

MD5 and phash are not computed during scanning — only on demand.

## SCAN.LAZY.MD5

MD5 is not computed at scan time.
internal/scanner/scanner_test.go:TestScan_LAZY_MD5

## SCAN.LAZY.PHASH

Perceptual hash is not computed at scan time.
internal/scanner/scanner_test.go:TestScan_LAZY_PHASH

## SCAN.MD5

MD5 is computed on demand and is identical for byte-identical files.
internal/scanner/scanner_test.go:TestScan_MD5

## SCAN.PHASH

Perceptual hash is computed on demand for determining visual similarity.
internal/scanner/scanner_test.go:TestScan_PHASH

## SCAN.PHASH.ROT

Perceptual hashes for all four cardinal rotations (0/90/180/270°) are computed for an original from a single decode.
internal/scanner/scanner_test.go:TestScan_PHASH_ROT

## CACHE

Caching phash in bbolt for faster repeated runs.

## CACHE.MISS

For an unknown file, the cache returns no result.
internal/cache/cache_test.go:TestCache_MISS

## CACHE.HIT

A previously stored phash is returned from the cache without recomputation.
internal/cache/cache_test.go:TestCache_HIT

## CACHE.INV.MTIME

When a file's modification time changes, the cache entry is invalidated.
internal/cache/cache_test.go:TestCache_INV_MTIME

## CACHE.INV.SIZE

When a file's size changes, the cache entry is invalidated.
internal/cache/cache_test.go:TestCache_INV_SIZE

## CACHE.UPD

When phash is stored again for the same path, the entry is updated.
internal/cache/cache_test.go:TestCache_UPD

## CACHE.ROT

Rotation hashes are cached as separate entries under NUL-suffixed derived keys (path+"\x00r90"/"\x00r180"/"\x00r270"); the bucket and 8-byte value format are unchanged, so existing caches stay valid. When all four keys are cached, the file is not decoded.
internal/scanner/scanner_test.go:TestScan_PHASH_ROT_CACHE

## CMP

Classifying unsorted files against originals.

## CMP.SIM

Files with identical phash (distance=0) are marked as similar.
internal/comparator/comparator_test.go:TestCmp_SIM

## CMP.SIM.MD5

If MD5 of two files matches, they are marked as similar (distance=0, score=1.0) without computing phash.
internal/comparator/comparator_test.go:TestCmp_SIM_MD5

## CMP.SIM.NODUP

A file found as a similar match is not also reported as doubtful.
internal/comparator/comparator_test.go:TestCmp_SIM_NODUP

## CMP.DOUBT

Files with phash distance in [1, 2] are marked as doubtful.
internal/comparator/comparator_test.go:TestCmp_DOUBT

## CMP.DOUBT.RATIO

Files with different aspect ratios are not marked as doubtful.
internal/comparator/comparator_test.go:TestCmp_DOUBT_RATIO

## CMP.DOUBT.RATIO.OK

Files with the same aspect ratio and close phash are marked as doubtful.
internal/comparator/comparator_test.go:TestCmp_DOUBT_RATIO_OK

## CMP.CLOSEST

For each unsorted file, the closest original (minimum hamming distance over all indexed orientations) is selected. An original contributes multiple candidate entries (one per rotation); ties are broken by stable path order.
internal/comparator/comparator_test.go:TestCmp_CLOSEST

## CMP.ROT

An original rotated 90/180/270° and placed in unsorted is matched as a duplicate (distance ~0) against that original.
internal/comparator/comparator_test.go:TestCmp_ROT

## CMP.ROT.RATIO

A rotation that inverts the aspect ratio (90/270°) is still found via the inverted ratio bucket.
internal/comparator/comparator_test.go:TestCmp_ROT_RATIO

## CMP.SCORE

Each result includes a similarity score: 1.0 for similar, 0.67 for dist=1, 0.33 for dist=2.
internal/comparator/comparator_test.go:TestCmp_SCORE

## CMP.EMPTY

If no duplicates are found, an empty result is returned.
internal/comparator/comparator_test.go:TestCmp_EMPTY

## OUT

Generating output files with classification results.

## OUT.JSON

JSON report is a valid JSON array.
internal/report/json_test.go:TestOutJSON

## OUT.JSON.HDR

JSON starts with a header (type: header) containing description and checksum type.
internal/report/json_test.go:TestOutJSON_HDR

## OUT.JSON.FTR

JSON ends with a footer containing total statistics: file count, duplicates, sets, lint size.
internal/report/json_test.go:TestOutJSON_FTR

## OUT.JSON.REC

Each duplicate record contains path, size, checksum, is_original, and move_to.
internal/report/json_test.go:TestOutJSON_REC

## OUT.JSON.SIM

Each duplicate record contains a similarity field with level, distance, and score.
internal/report/json_test.go:TestOutJSON_SIM

## OUT.JSON.ORIG

For each duplicate group, the original is emitted as its own is_original: true record.
internal/report/json_test.go:TestOutJSON_ORIG

## OUT.JSON.TWINS

Each duplicate_file record includes a twins field equal to the group size.
internal/report/json_test.go:TestOutJSON_TWINS

## OUT.JSON.HOST

All paths in JSON are real filesystem paths, not docker paths.
internal/report/json_test.go:TestOutJSON_HOST

## OUT.SH

Shell script starts with a shebang.
internal/report/shell_test.go:TestOutSH

## OUT.SH.FUNC

Shell script contains a move_cmd function for moving files.
internal/report/shell_test.go:TestOutSH_FUNC

## OUT.SH.ORIGCMD

For each duplicate group, an original_cmd marker line is emitted.
internal/report/shell_test.go:TestOutSH_ORIGCMD

## OUT.SH.GROUP

Move commands are grouped by original, separated by blank lines.
internal/report/shell_test.go:TestOutSH_GROUP

## OUT.SH.CHK

Before moving, the original's existence is checked — if missing, the duplicate is not moved.
internal/report/shell_test.go:TestOutSH_CHK

## OUT.SH.CMD

Move commands use real paths: duplicate, original, destination.
internal/report/shell_test.go:TestOutSH_CMD

## OUT.SH.CMT

Each move command is accompanied by a comment with original info.
internal/report/shell_test.go:TestOutSH_CMT

## OUT.SH.HOST

Shell script does not contain docker paths — only real filesystem paths.
internal/report/shell_test.go:TestOutSH_HOST

## OUT.CON

Console summary is printed to stdout with colored output.

## OUT.CON.HDR

Console output starts with "# Duplicate(s):" header.

## OUT.CON.GRP

Each original is printed in green with its path.

## OUT.CON.DUP

Each duplicate is printed in yellow with level, path, and distance.

## OUT.CON.STATS

Summary lines prefixed with blue "==>" showing file count, duplicates, lint size, and elapsed time.

## OUT.CON.PATHS

After the summary, paths of written json and sh files are printed.

## OUT.ATOMIC

JSON and shell files are written to a temp file first, then atomically renamed.

## CLI

Command-line interface and orchestration.

## CLI.FLAGS

mltlint accepts flags: --originals, --unsorted, --dups (required), --output, --cache, --workers, --debug (optional).

## CLI.FLAGS.REQ

If --originals, --unsorted, or --dups is missing, exit with error and usage.

## CLI.PIPELINE

Execution runs in two phases: phase 1 scans originals and computes phash, phase 2 scans unsorted and computes phash.

## CLI.SIGNAL

SIGINT and SIGTERM are trapped for graceful abort.

## CLI.ABORT

When interrupted, partial results are written with aborted=true in JSON footer.

## CLI.EXIT.0

Exit code 0 on success.

## CLI.EXIT.1

Exit code 1 on fatal error.

## CLI.EXIT.2

Exit code 2 when ignored files are present.

## CLI.EXIT.130

Exit code 130 on interrupt (SIGINT/SIGTERM).

## CLI.CACHE.XDG

Default cache path uses XDG_CACHE_HOME, fallback to ~/.cache/mltlint/cache.db.

## E2E

End-to-end tests.

## E2E.BASIC

Basic flow: byte-identical match produces JSON with header/footer/records and shell with move_cmd.
cmd/mltlint/e2e_test.go:TestE2E_BasicFlow

## E2E.EMPTY

Empty originals: no duplicates found, footer has duplicates=0.
cmd/mltlint/e2e_test.go:TestE2E_EmptyOriginals

## E2E.ABORT

Interrupted run: if JSON is written, footer has aborted=true.
cmd/mltlint/e2e_test.go:TestE2E_AbortedRun

## E2E.ROT

A physically rotated copy of an original is detected as a duplicate end-to-end.
cmd/mltlint/e2e_test.go:TestE2E_RotatedDuplicate
