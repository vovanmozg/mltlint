# Rotation-aware duplicate detection — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Detect duplicates that have been rotated 90/180/270° by indexing all four cardinal orientations of each original, while keeping the unsorted side single-hash and the cache format unchanged.

**Architecture:** Approach B — originals carry four perceptual hashes (0/90/180/270°), computed from a single decode and cached as separate entries under NUL-suffixed derived keys (no cache format change). `BuildIndexes` places each orientation's hash in its aspect-ratio bucket (90/270° land in the inverted-ratio bucket). `Classify` compares an unsorted file's single hash against the precomputed candidates. Every run after the first reads four cached hashes and never decodes.

**Tech Stack:** Go, `github.com/corona10/goimagehash` (DCT pHash, already a dependency), `go.etcd.io/bbolt` (cache, unchanged), `testify/require`.

**Spec:** `docs/superpowers/specs/2026-06-01-rotation-aware-duplicates-design.md`

---

## File Structure

- `internal/scanner/scanner.go` — add `PHash90/180/270` fields to `FileInfo`; add `rotate90`, `computeFilePHashRotations`, `ComputePHashRotations`; add rotation-suffix constants.
- `internal/scanner/scanner_test.go` — add `TestScan_PHASH_ROT`, `TestScan_PHASH_ROT_CACHE`.
- `internal/comparator/comparator.go` — change `Indexes.ByRatio` element type to `rotCandidate`; add `RatioSwapped`; rewrite `BuildIndexes` and the pHash path of `Classify`.
- `internal/comparator/comparator_test.go` — add `TestCmp_ROT`, `TestCmp_ROT_RATIO`.
- `cmd/mltlint/main.go` — phase 1 calls `ComputePHashRotations` instead of `ComputePHash`.
- `cmd/mltlint/e2e_test.go` — add `TestE2E_RotatedDuplicate` + a `rotate90File` test helper.
- `docs/REQUIREMENTS.md` — add `SCAN.PHASH.ROT`, `CACHE.ROT`, `CMP.ROT`, `CMP.ROT.RATIO`, `E2E.ROT`; reword `CMP.CLOSEST`.

---

## Task 1: Scanner — compute four rotation hashes (no cache yet)

**Files:**
- Modify: `internal/scanner/scanner.go`
- Test: `internal/scanner/scanner_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/scanner/scanner_test.go`:

```go
// Cycle 10: SCAN.PHASH.ROT — four cardinal-rotation hashes from one decode.
func TestScan_PHASH_ROT(t *testing.T) {
	sc := scanner.New(nil, 1, false)
	files, err := sc.Scan(context.Background(), fixturesDir(t))
	require.NoError(t, err)
	f := findByName(files, "10.jpg") // 2480x476, non-square → rotations differ
	require.NotNil(t, f)

	err = sc.ComputePHashRotations(context.Background(), []*scanner.FileInfo{f})
	require.NoError(t, err)
	require.NotEqual(t, uint64(0), f.PHash)
	require.NotEqual(t, uint64(0), f.PHash90)
	require.NotEqual(t, uint64(0), f.PHash180)
	require.NotEqual(t, uint64(0), f.PHash270)
	require.NotEqual(t, f.PHash, f.PHash90) // non-square → 0° and 90° differ

	// 0° hash must equal what plain ComputePHash produces for the same file.
	files2, err := sc.Scan(context.Background(), fixturesDir(t))
	require.NoError(t, err)
	g := findByName(files2, "10.jpg")
	require.NotNil(t, g)
	require.NoError(t, sc.ComputePHash(context.Background(), []*scanner.FileInfo{g}))
	require.Equal(t, g.PHash, f.PHash)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/scanner/ -run TestScan_PHASH_ROT -v`
Expected: FAIL — compile error `f.PHash90 undefined` / `sc.ComputePHashRotations undefined`.

- [ ] **Step 3: Add the new fields to `FileInfo`**

In `internal/scanner/scanner.go`, change the `FileInfo` struct's hash fields from:

```go
	MD5          string // empty until ComputeMD5
	PHash        uint64 // zero until ComputePHash
	Err          error  // per-file soft error, or nil
```

to:

```go
	MD5          string // empty until ComputeMD5
	PHash        uint64 // 0° hash; zero until ComputePHash/ComputePHashRotations
	PHash90      uint64 // 90° hash; populated for originals by ComputePHashRotations
	PHash180     uint64 // 180° hash; populated for originals by ComputePHashRotations
	PHash270     uint64 // 270° hash; populated for originals by ComputePHashRotations
	Err          error  // per-file soft error, or nil
```

- [ ] **Step 4: Add the `rotate90` helper**

Append to `internal/scanner/scanner.go`:

```go
// rotate90 returns img rotated 90° clockwise as a new RGBA image.
func rotate90(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(h-1-y, x, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}
```

- [ ] **Step 5: Add `computeFilePHashRotations`**

Append to `internal/scanner/scanner.go`:

```go
// computeFilePHashRotations decodes the image once and returns the perceptual
// hashes of its four cardinal rotations: [0°, 90°, 180°, 270°].
// Rotations are hashed one at a time so peak memory stays ~2x the decoded image.
func computeFilePHashRotations(path string) ([4]uint64, error) {
	var out [4]uint64

	f, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return out, err
	}

	hashOf := func(im image.Image) (uint64, error) {
		h, err := goimagehash.PerceptionHash(im)
		if err != nil {
			return 0, err
		}
		return h.GetHash(), nil
	}

	if out[0], err = hashOf(img); err != nil {
		return out, err
	}
	r := rotate90(img)
	if out[1], err = hashOf(r); err != nil {
		return out, err
	}
	r = rotate90(r)
	if out[2], err = hashOf(r); err != nil {
		return out, err
	}
	r = rotate90(r)
	if out[3], err = hashOf(r); err != nil {
		return out, err
	}
	return out, nil
}
```

- [ ] **Step 6: Add `ComputePHashRotations` (no cache yet)**

Append to `internal/scanner/scanner.go`:

```go
// ComputePHashRotations fills PHash, PHash90, PHash180, PHash270 for each file
// using parallel workers. Each file is decoded once. (Cache integration is
// added in a later step.)
func (s *Scanner) ComputePHashRotations(ctx context.Context, files []*FileInfo) error {
	sem := make(chan struct{}, s.workers)
	var wg sync.WaitGroup

	for _, fi := range files {
		fi := fi
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			select {
			case <-ctx.Done():
				fi.Err = ctx.Err()
				return
			default:
			}

			hashes, err := computeFilePHashRotations(fi.Path)
			if err != nil {
				fi.Err = err
				return
			}
			fi.PHash, fi.PHash90, fi.PHash180, fi.PHash270 = hashes[0], hashes[1], hashes[2], hashes[3]
		}()
	}

	wg.Wait()
	return nil
}
```

- [ ] **Step 7: Run test to verify it passes**

Run: `go test ./internal/scanner/ -run TestScan_PHASH_ROT -v`
Expected: PASS.

- [ ] **Step 8: Run the full scanner suite (no regressions)**

Run: `go test ./internal/scanner/ -v`
Expected: PASS (all existing `TestScan_*` plus the new one).

- [ ] **Step 9: Commit**

```bash
git add internal/scanner/scanner.go internal/scanner/scanner_test.go
git commit -m "feat(scanner): compute four cardinal-rotation phashes for a file"
```

---

## Task 2: Scanner — cache rotation hashes under NUL-suffixed keys

**Files:**
- Modify: `internal/scanner/scanner.go`
- Test: `internal/scanner/scanner_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/scanner/scanner_test.go`. This also requires two new imports — add `"os"` and `"github.com/vp/mlt3/internal/cache"` to the import block:

```go
// Cycle 11: CACHE.ROT — rotations cached under derived keys; a full cache hit
// serves without decoding (proven by deleting the source before the 2nd call).
func TestScan_PHASH_ROT_CACHE(t *testing.T) {
	c, err := cache.Open(filepath.Join(t.TempDir(), "c.db"))
	require.NoError(t, err)
	defer c.Close()
	sc := scanner.New(c, 1, false)

	// Copy a fixture into a temp dir so we can delete it later.
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join(fixturesDir(t), "10.jpg"))
	require.NoError(t, err)
	dst := filepath.Join(dir, "10.jpg")
	require.NoError(t, os.WriteFile(dst, data, 0644))

	files, err := sc.Scan(context.Background(), dir)
	require.NoError(t, err)
	f := findByName(files, "10.jpg")
	require.NotNil(t, f)
	require.NoError(t, sc.ComputePHashRotations(context.Background(), []*scanner.FileInfo{f}))
	want := [4]uint64{f.PHash, f.PHash90, f.PHash180, f.PHash270}

	// All four entries are present under their derived keys.
	h0, ok := c.Get(f.Path, f.Mtime, f.Size)
	require.True(t, ok)
	require.Equal(t, want[0], h0)
	h90, ok := c.Get(f.Path+"\x00r90", f.Mtime, f.Size)
	require.True(t, ok)
	require.Equal(t, want[1], h90)
	h180, ok := c.Get(f.Path+"\x00r180", f.Mtime, f.Size)
	require.True(t, ok)
	require.Equal(t, want[2], h180)
	h270, ok := c.Get(f.Path+"\x00r270", f.Mtime, f.Size)
	require.True(t, ok)
	require.Equal(t, want[3], h270)

	// Delete the source. A second call on a fresh FileInfo with the same
	// identity must succeed from cache alone (no decode → no error).
	require.NoError(t, os.Remove(dst))
	g := &scanner.FileInfo{Path: f.Path, Mtime: f.Mtime, Size: f.Size}
	require.NoError(t, sc.ComputePHashRotations(context.Background(), []*scanner.FileInfo{g}))
	require.NoError(t, g.Err)
	require.Equal(t, want, [4]uint64{g.PHash, g.PHash90, g.PHash180, g.PHash270})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/scanner/ -run TestScan_PHASH_ROT_CACHE -v`
Expected: FAIL — second call decodes a deleted file, so `g.Err` is a "no such file" error (and the cache `Get` assertions fail because nothing was stored).

- [ ] **Step 3: Add rotation-suffix constants**

In `internal/scanner/scanner.go`, just above the `FileInfo` struct, add:

```go
// Rotation cache-key suffixes. NUL is forbidden in filesystem paths, so these
// derived keys cannot collide with a real file's path.
const (
	rotSuffix90  = "\x00r90"
	rotSuffix180 = "\x00r180"
	rotSuffix270 = "\x00r270"
)
```

- [ ] **Step 4: Add cache read/write to `ComputePHashRotations`**

In `internal/scanner/scanner.go`, replace the goroutine body of `ComputePHashRotations` (the part after the `ctx.Done()` select) with:

```go
			// Cache fast path: if all four rotations are cached, skip decode.
			if s.cache != nil {
				h0, ok0 := s.cache.Get(fi.Path, fi.Mtime, fi.Size)
				h90, ok90 := s.cache.Get(fi.Path+rotSuffix90, fi.Mtime, fi.Size)
				h180, ok180 := s.cache.Get(fi.Path+rotSuffix180, fi.Mtime, fi.Size)
				h270, ok270 := s.cache.Get(fi.Path+rotSuffix270, fi.Mtime, fi.Size)
				if ok0 && ok90 && ok180 && ok270 {
					fi.PHash, fi.PHash90, fi.PHash180, fi.PHash270 = h0, h90, h180, h270
					return
				}
			}

			hashes, err := computeFilePHashRotations(fi.Path)
			if err != nil {
				fi.Err = err
				return
			}
			fi.PHash, fi.PHash90, fi.PHash180, fi.PHash270 = hashes[0], hashes[1], hashes[2], hashes[3]

			if s.cache != nil {
				_ = s.cache.Set(fi.Path, fi.Mtime, fi.Size, hashes[0])
				_ = s.cache.Set(fi.Path+rotSuffix90, fi.Mtime, fi.Size, hashes[1])
				_ = s.cache.Set(fi.Path+rotSuffix180, fi.Mtime, fi.Size, hashes[2])
				_ = s.cache.Set(fi.Path+rotSuffix270, fi.Mtime, fi.Size, hashes[3])
			}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/scanner/ -run TestScan_PHASH_ROT_CACHE -v`
Expected: PASS.

- [ ] **Step 6: Run the full scanner suite**

Run: `go test ./internal/scanner/ -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/scanner/scanner.go internal/scanner/scanner_test.go
git commit -m "feat(scanner): cache rotation phashes under NUL-suffixed keys"
```

---

## Task 3: Comparator — index four orientations and match against them

**Files:**
- Modify: `internal/comparator/comparator.go`
- Test: `internal/comparator/comparator_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/comparator/comparator_test.go`:

```go
// Cycle 10: CMP.ROT — unsorted hash equals one of the original's rotation
// hashes → matched as similar to that original.
func TestCmp_ROT(t *testing.T) {
	o := &scanner.FileInfo{
		Path: "/originals/o.jpg", RelativePath: "o.jpg",
		PHash: 100, PHash90: 200, PHash180: 300, PHash270: 400,
		Width: 250, Height: 250, Size: 8000,
	}
	// Unsorted file looks like o rotated 90° (its natural hash == o.PHash90).
	u := &scanner.FileInfo{
		Path: "/unsorted/u.jpg", RelativePath: "u.jpg",
		MD5: "zzz", PHash: 200, Width: 250, Height: 250, Size: 8123,
	}
	indexes := comparator.BuildIndexes([]*scanner.FileInfo{o})
	r, err := comparator.Classify(context.Background(), u, indexes, noop)
	require.NoError(t, err)
	require.NotNil(t, r)
	require.Equal(t, "/originals/o.jpg", r.Original.Path)
	require.Equal(t, "similar", r.Level)
	require.Equal(t, 0, r.Distance)
}

// Cycle 11: CMP.ROT.RATIO — a 90° rotation that inverts the aspect ratio is
// still found via the inverted ratio bucket.
func TestCmp_ROT_RATIO(t *testing.T) {
	// Original is landscape 400x200 (ratio 2.0); its 90° hash is 500.
	o := &scanner.FileInfo{
		Path: "/originals/wide.jpg", RelativePath: "wide.jpg",
		PHash: 100, PHash90: 500, PHash180: 110, PHash270: 510,
		Width: 400, Height: 200, Size: 8000,
	}
	// Unsorted is the rotated copy: portrait 200x400 (ratio 0.5), hash 500.
	u := &scanner.FileInfo{
		Path: "/unsorted/tall.jpg", RelativePath: "tall.jpg",
		MD5: "yyy", PHash: 500, Width: 200, Height: 400, Size: 8200,
	}
	indexes := comparator.BuildIndexes([]*scanner.FileInfo{o})
	r, err := comparator.Classify(context.Background(), u, indexes, noop)
	require.NoError(t, err)
	require.NotNil(t, r)
	require.Equal(t, "/originals/wide.jpg", r.Original.Path)
	require.Equal(t, "similar", r.Level)
	require.Equal(t, 0, r.Distance)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/comparator/ -run 'TestCmp_ROT' -v`
Expected: FAIL — `u` (ratio 2.0/0.5) finds no candidates because `ByRatio` only holds the original's 0° orientation, so `r` is nil.

- [ ] **Step 3: Add `rotCandidate` and change the `Indexes.ByRatio` type**

In `internal/comparator/comparator.go`, replace the `Indexes` struct:

```go
// Indexes holds pre-built lookup indexes over originals.
type Indexes struct {
	BySize  map[int64][]*scanner.FileInfo
	ByRatio map[float64][]rotCandidate
}

// rotCandidate is one orientation of an original: a precomputed rotation hash
// plus a back-pointer to the original file for reporting.
type rotCandidate struct {
	hash     uint64
	original *scanner.FileInfo
}
```

- [ ] **Step 4: Add `RatioSwapped`**

In `internal/comparator/comparator.go`, just below `Ratio`, add:

```go
// RatioSwapped returns the aspect ratio of the file with width/height swapped
// (i.e. the ratio it has after a 90°/270° rotation), computed directly from
// dimensions to avoid compounding the rounding already applied by Ratio.
func RatioSwapped(f *scanner.FileInfo) float64 {
	if f.Width == 0 || f.Height == 0 {
		return 0
	}
	return math.Round(float64(f.Height)/float64(f.Width)*10) / 10
}
```

- [ ] **Step 5: Rewrite `BuildIndexes` to index all four orientations**

In `internal/comparator/comparator.go`, replace the index-building loop inside `BuildIndexes`:

```go
	bySize := make(map[int64][]*scanner.FileInfo)
	byRatio := make(map[float64][]rotCandidate)

	for _, o := range sorted {
		bySize[o.Size] = append(bySize[o.Size], o)

		r := Ratio(o)         // 0° and 180° keep the original ratio
		rs := RatioSwapped(o) // 90° and 270° invert width/height
		byRatio[r] = append(byRatio[r], rotCandidate{hash: o.PHash, original: o}, rotCandidate{hash: o.PHash180, original: o})
		byRatio[rs] = append(byRatio[rs], rotCandidate{hash: o.PHash90, original: o}, rotCandidate{hash: o.PHash270, original: o})
	}

	return &Indexes{BySize: bySize, ByRatio: byRatio}
```

(The `sorted` slice and its `sort.Slice(... Path ...)` above stay exactly as they are, so each original's four candidates are appended contiguously in path order — preserving the tie-break.)

- [ ] **Step 6: Update the pHash path of `Classify`**

In `internal/comparator/comparator.go`, replace everything from the `// Step 3: Look up by ratio` comment through the candidate loop (i.e. the block that ends right before the `if bestOriginal == nil` check) with:

```go
	// Step 3: Look up by ratio among the originals' precomputed rotation
	// candidates, and find the minimum hamming distance.
	fileRatio := Ratio(file)
	candidates, ok := indexes.ByRatio[fileRatio]
	if !ok || len(candidates) == 0 {
		return nil, nil
	}

	bestDist := math.MaxInt32
	var bestOriginal *scanner.FileInfo

	for _, c := range candidates {
		d := hammingDistance(file.PHash, c.hash)
		if d < bestDist {
			bestDist = d
			bestOriginal = c.original
		}
	}
```

Note: the old `if err := hc.ComputePHash(ctx, candidates); err != nil { ... }` call is removed — candidate hashes are precomputed in `BuildIndexes`. The earlier `// Step 2: PHash path — compute phash for the file.` block (`hc.ComputePHash(ctx, []*scanner.FileInfo{file})`) stays unchanged.

- [ ] **Step 7: Run the new tests to verify they pass**

Run: `go test ./internal/comparator/ -run 'TestCmp_ROT' -v`
Expected: PASS.

- [ ] **Step 8: Run the full comparator suite (existing tests must stay green)**

Run: `go test ./internal/comparator/ -v`
Expected: PASS. (Existing originals leave `PHash90/180/270` zero; those zero-valued candidates only ever yield large hamming distances against the test fixtures, so they never displace the real match.)

- [ ] **Step 9: Commit**

```bash
git add internal/comparator/comparator.go internal/comparator/comparator_test.go
git commit -m "feat(comparator): index four orientations, match rotations"
```

---

## Task 4: Wire rotation hashing into the pipeline

**Files:**
- Modify: `cmd/mltlint/main.go:103-107`

- [ ] **Step 1: Switch phase 1 to `ComputePHashRotations`**

In `cmd/mltlint/main.go`, replace:

```go
	fmt.Fprintf(os.Stderr, "[phase 1/2] originals: scanned %d, computing phash...\n", len(origFiles))
	if err := sc.ComputePHash(ctx, origFiles); err != nil {
		fmt.Fprintf(os.Stderr, "mltlint: compute phash for originals: %v\n", err)
		return 1
	}
```

with:

```go
	fmt.Fprintf(os.Stderr, "[phase 1/2] originals: scanned %d, computing phash (4 rotations)...\n", len(origFiles))
	if err := sc.ComputePHashRotations(ctx, origFiles); err != nil {
		fmt.Fprintf(os.Stderr, "mltlint: compute phash for originals: %v\n", err)
		return 1
	}
```

(Phase 2 keeps `sc.ComputePHash(ctx, unsortedFiles)` — unsorted stays single-hash.)

- [ ] **Step 2: Build to verify it compiles**

Run: `go build ./...`
Expected: no output, exit 0.

- [ ] **Step 3: Run the existing e2e suite (no regressions)**

Run: `go test ./cmd/mltlint/ -v`
Expected: PASS (`TestE2E_BasicFlow`, `TestE2E_EmptyOriginals`, `TestE2E_AbortedRun`).

- [ ] **Step 4: Commit**

```bash
git add cmd/mltlint/main.go
git commit -m "feat(cli): hash all four orientations of originals in phase 1"
```

---

## Task 5: End-to-end — a physically rotated copy is detected

**Files:**
- Modify: `cmd/mltlint/e2e_test.go`

- [ ] **Step 1: Write the failing test + helper**

Add `"image"` and `"image/jpeg"` to the import block of `cmd/mltlint/e2e_test.go`, then append:

```go
// rotate90File decodes src, rotates it 90° (independently of production code),
// and writes the result as a JPEG to dst. Any cardinal rotation of the source
// matches one of the original's four indexed orientations, so the exact
// direction does not matter.
func rotate90File(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	require.NoError(t, err)
	defer in.Close()
	img, _, err := image.Decode(in)
	require.NoError(t, err)

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	rot := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			rot.Set(h-1-y, x, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}

	out, err := os.Create(dst)
	require.NoError(t, err)
	defer out.Close()
	require.NoError(t, jpeg.Encode(out, rot, &jpeg.Options{Quality: 92}))
}

// E2E.ROT — a 90°-rotated copy of an original is reported as a duplicate.
func TestE2E_RotatedDuplicate(t *testing.T) {
	bin := buildBinary(t)
	base := t.TempDir()
	originals := filepath.Join(base, "originals")
	unsorted := filepath.Join(base, "unsorted")
	dups := filepath.Join(base, "dups")
	output := filepath.Join(base, "output")
	for _, d := range []string{originals, unsorted, dups, output} {
		require.NoError(t, os.MkdirAll(d, 0755))
	}

	fixtures := filepath.Join(repoRoot(t), "testdata", "media")
	copyFile(t, filepath.Join(fixtures, "10.jpg"), filepath.Join(originals, "10.jpg"))
	rotate90File(t, filepath.Join(fixtures, "10.jpg"), filepath.Join(unsorted, "rotated.jpg"))

	cachePath := filepath.Join(t.TempDir(), "cache.db")
	cmd := exec.Command(bin,
		"--originals="+originals, "--unsorted="+unsorted,
		"--dups="+dups, "--output="+output, "--cache="+cachePath)
	out, err := cmd.CombinedOutput()
	t.Logf("output:\n%s", out)
	require.NoError(t, err)

	jsonData, err := os.ReadFile(filepath.Join(output, "mltlint.json"))
	require.NoError(t, err)
	var records []map[string]interface{}
	require.NoError(t, json.Unmarshal(jsonData, &records))

	footer := records[len(records)-1]
	require.Equal(t, "footer", footer["type"])
	require.GreaterOrEqual(t, footer["duplicates"], float64(1))

	foundDup := false
	for _, r := range records[1 : len(records)-1] {
		if r["is_original"] == false {
			if p, _ := r["path"].(string); strings.Contains(p, "rotated.jpg") {
				foundDup = true
			}
		}
	}
	require.True(t, foundDup, "rotated copy was not detected as a duplicate")
}
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test ./cmd/mltlint/ -run TestE2E_RotatedDuplicate -v`
Expected: PASS. (This is the empirical confirmation that an axis-aligned rotation lands within the distance threshold. If it FAILS with the rotated file absent from duplicates, stop and report the observed distance — it means the threshold or rotation pipeline needs revisiting, not a test tweak.)

- [ ] **Step 3: Run the full e2e suite**

Run: `go test ./cmd/mltlint/ -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/mltlint/e2e_test.go
git commit -m "test(e2e): detect a physically rotated duplicate"
```

---

## Task 6: Update requirements documentation

**Files:**
- Modify: `docs/REQUIREMENTS.md`

- [ ] **Step 1: Reword `CMP.CLOSEST`**

In `docs/REQUIREMENTS.md`, replace the body of the `## CMP.CLOSEST` section:

```
For each unsorted file, the closest original (minimum hamming distance) is selected; ties broken by stable path order.
internal/comparator/comparator_test.go:TestCmp_CLOSEST
```

with:

```
For each unsorted file, the closest original (minimum hamming distance over all indexed orientations) is selected. An original contributes multiple candidate entries (one per rotation); ties are broken by stable path order.
internal/comparator/comparator_test.go:TestCmp_CLOSEST
```

- [ ] **Step 2: Add `SCAN.PHASH.ROT`**

In `docs/REQUIREMENTS.md`, after the `## SCAN.PHASH` section, add:

```
## SCAN.PHASH.ROT

Perceptual hashes for all four cardinal rotations (0/90/180/270°) are computed for an original from a single decode.
internal/scanner/scanner_test.go:TestScan_PHASH_ROT
```

- [ ] **Step 3: Add `CACHE.ROT`**

In `docs/REQUIREMENTS.md`, after the `## CACHE.UPD` section, add:

```
## CACHE.ROT

Rotation hashes are cached as separate entries under NUL-suffixed derived keys (path+"\x00r90"/"\x00r180"/"\x00r270"); the bucket and 8-byte value format are unchanged, so existing caches stay valid. When all four keys are cached, the file is not decoded.
internal/scanner/scanner_test.go:TestScan_PHASH_ROT_CACHE
```

- [ ] **Step 4: Add `CMP.ROT` and `CMP.ROT.RATIO`**

In `docs/REQUIREMENTS.md`, after the `## CMP.CLOSEST` section, add:

```
## CMP.ROT

An original rotated 90/180/270° and placed in unsorted is matched as a duplicate (distance ~0) against that original.
internal/comparator/comparator_test.go:TestCmp_ROT

## CMP.ROT.RATIO

A rotation that inverts the aspect ratio (90/270°) is still found via the inverted ratio bucket.
internal/comparator/comparator_test.go:TestCmp_ROT_RATIO
```

- [ ] **Step 5: Add `E2E.ROT`**

In `docs/REQUIREMENTS.md`, after the `## E2E.ABORT` section, add:

```
## E2E.ROT

A physically rotated copy of an original is detected as a duplicate end-to-end.
cmd/mltlint/e2e_test.go:TestE2E_RotatedDuplicate
```

- [ ] **Step 6: Verify referenced tests exist and pass**

Run: `go test ./... -run 'TestScan_PHASH_ROT|TestScan_PHASH_ROT_CACHE|TestCmp_ROT|TestCmp_ROT_RATIO|TestE2E_RotatedDuplicate' -v`
Expected: PASS (every requirement reference resolves to a passing test).

> Note: the global `req-sync` skill targets Ruby/RSpec projects (`req:`/`req_desc:` metadata). This Go project's `docs/REQUIREMENTS.md` is maintained by hand in the same style as its existing entries, so the edits above are done manually rather than via `req-sync`.

- [ ] **Step 7: Commit**

```bash
git add docs/REQUIREMENTS.md
git commit -m "docs: add rotation-aware requirements"
```

---

## Final verification

- [ ] **Run the whole suite**

Run: `go test ./...`
Expected: all packages PASS.

- [ ] **Build the binary**

Run: `go build -o /tmp/mltlint ./cmd/mltlint`
Expected: exit 0.

---

## Self-review notes (author)

- **Spec coverage:** Approach B (originals carry 4 hashes) → Tasks 1–4; cache format unchanged via NUL suffix → Task 2 + `CACHE.ROT`; ratio inversion → `RatioSwapped` in Task 3 + `CMP.ROT.RATIO`; tie-break preserved → Task 3 Step 5 note + reworded `CMP.CLOSEST`; always-on (no flag) → Task 4 (unconditional); output unchanged → no report/JSON tasks; empirical threshold check → Task 5.
- **Type consistency:** `rotCandidate{hash, original}`, `ByRatio map[float64][]rotCandidate`, `ComputePHashRotations`, `RatioSwapped`, fields `PHash90/180/270`, suffix constants `rotSuffix90/180/270` (`"\x00r90"` etc.) are used identically across scanner, comparator, and tests.
- **No placeholders:** every code/edit step contains the full code and exact run commands with expected results.
