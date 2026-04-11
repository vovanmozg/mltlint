# mltlint

Like [rmlint](https://github.com/sahib/rmlint), but for visually similar images. Uses perceptual hashing instead of byte-exact comparison, so it catches re-compressed and re-saved duplicates too.

Scans an **unsorted** directory, compares against **originals**, and generates a shell script to move duplicates out.

## Motivation

I keep all my photos organized by year on a personal NAS. When adding new photos — say, from my wife's phone — some of them are already in the archive. I don't want duplicates, but I can't just compare bytes: the same photo might have been rotated, re-compressed, or re-saved with different metadata.

rmlint is great for finding exact copies, but it misses these cases. mltlint solves this by comparing how images *look*, not how they're stored.

## Install

Download a binary from [Releases](../../releases) or build from source:

```bash
go build -o mltlint ./cmd/mltlint
```

## Usage

```bash
mltlint --originals=/path/to/originals --unsorted=/path/to/unsorted --dups=/path/to/dups
```

This produces two files:
- `mltlint.json` — machine-readable report
- `mltlint.sh` — review and run to move duplicates

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--originals` | required | Directory with existing originals |
| `--unsorted` | required | Directory with files to check |
| `--dups` | required | Destination directory for duplicates |
| `--output` | `.` | Where to write mltlint.json/sh |
| `--cache` | `~/.cache/mltlint/cache.db` | Path to phash cache |
| `--workers` | NumCPU | Number of parallel workers |
| `--debug` | false | Verbose logging to stderr |

## How it works

1. Scans originals and computes perceptual hashes (DCT-based pHash)
2. Scans unsorted files and compares against originals
3. Classifies matches: **similar** (distance=0), **doubtful** (distance 1-2), or unique
4. Byte-identical files are detected via MD5 fast path without computing pHash

Computing perceptual hashes is CPU-heavy, so mltlint caches them in a local database. The first run takes longer, but subsequent runs skip already-hashed files and finish much faster.

## Example output

```
# Duplicate(s):
    original '/photos/originals/IMG_1234.jpg'
    similar  '/photos/unsorted/copy.jpg' (dist=0)
    similar  '/photos/unsorted/resaved.jpg' (dist=0)

==> In total 150 files, whereof 12 are duplicates in 5 groups.
==> This equals 45.20 KB of duplicates which could be moved.
==> Scanning took in total 2.3s.
```

## Supported formats

jpg, jpeg, png, gif, bmp, tif, tiff, webp

## Credits

Inspired by [rmlint](https://github.com/sahib/rmlint).
