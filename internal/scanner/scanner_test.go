package scanner_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vp/mlt3/internal/cache"
	"github.com/vp/mlt3/internal/scanner"
)

func fixturesDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("../../testdata/media")
	require.NoError(t, err)
	return abs
}

func filenames(files []*scanner.FileInfo) []string {
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = filepath.Base(f.Path)
	}
	return names
}

func findByName(files []*scanner.FileInfo, name string) *scanner.FileInfo {
	for _, f := range files {
		if filepath.Base(f.Path) == name {
			return f
		}
	}
	return nil
}

// Cycle 1: SCAN.SKIP.EMPTY
func TestScan_SKIP_EMPTY(t *testing.T) {
	sc := scanner.New(nil, 1, false)
	files, err := sc.Scan(context.Background(), fixturesDir(t))
	require.NoError(t, err)
	names := filenames(files)
	require.NotContains(t, names, "7 broken.jpg")
}

// Cycle 2: SCAN.SKIP
func TestScan_SKIP(t *testing.T) {
	sc := scanner.New(nil, 1, false)
	files, err := sc.Scan(context.Background(), fixturesDir(t))
	require.NoError(t, err)
	names := filenames(files)
	require.NotContains(t, names, "6 broken.jpg") // non-image content
}

// Cycle 3: SCAN.FIND
func TestScan_FIND(t *testing.T) {
	sc := scanner.New(nil, 1, false)
	files, err := sc.Scan(context.Background(), fixturesDir(t))
	require.NoError(t, err)
	names := filenames(files)
	require.Contains(t, names, "1.jpg")
	require.Contains(t, names, "dull.jpg")
	require.Contains(t, names, "9.png")
}

// Cycle 4: SCAN.META
func TestScan_META(t *testing.T) {
	sc := scanner.New(nil, 1, false)
	files, err := sc.Scan(context.Background(), fixturesDir(t))
	require.NoError(t, err)
	f := findByName(files, "1.jpg")
	require.NotNil(t, f)
	require.Greater(t, f.Size, int64(0))
	require.Greater(t, f.Mtime, int64(0))
	require.Equal(t, "1.jpg", f.RelativePath)
}

// Cycle 5: SCAN.DIM
func TestScan_DIM(t *testing.T) {
	sc := scanner.New(nil, 1, false)
	files, err := sc.Scan(context.Background(), fixturesDir(t))
	require.NoError(t, err)
	f := findByName(files, "1.jpg")
	require.NotNil(t, f)
	require.Equal(t, 250, f.Width)
	require.Equal(t, 250, f.Height)
}

// Cycle 6: SCAN.RELPATH
func TestScan_RELPATH(t *testing.T) {
	sc := scanner.New(nil, 1, false)
	files, err := sc.Scan(context.Background(), fixturesDir(t))
	require.NoError(t, err)
	f := findByName(files, "9.png")
	require.NotNil(t, f)
	require.Equal(t, "9.png", f.RelativePath)
}

// Cycle 7: SCAN.LAZY.MD5 + SCAN.LAZY.PHASH
func TestScan_LAZY_MD5(t *testing.T) {
	sc := scanner.New(nil, 1, false)
	files, err := sc.Scan(context.Background(), fixturesDir(t))
	require.NoError(t, err)
	f := findByName(files, "1.jpg")
	require.NotNil(t, f)
	require.Empty(t, f.MD5)
}

func TestScan_LAZY_PHASH(t *testing.T) {
	sc := scanner.New(nil, 1, false)
	files, err := sc.Scan(context.Background(), fixturesDir(t))
	require.NoError(t, err)
	f := findByName(files, "1.jpg")
	require.NotNil(t, f)
	require.Equal(t, uint64(0), f.PHash)
}

// Cycle 8: SCAN.MD5
func TestScan_MD5(t *testing.T) {
	sc := scanner.New(nil, 1, false)
	files, err := sc.Scan(context.Background(), fixturesDir(t))
	require.NoError(t, err)
	f1 := findByName(files, "1.jpg")
	fDull := findByName(files, "dull.jpg")
	require.NotNil(t, f1)
	require.NotNil(t, fDull)
	err = sc.ComputeMD5(context.Background(), []*scanner.FileInfo{f1, fDull})
	require.NoError(t, err)
	require.Regexp(t, `^[0-9a-f]{32}$`, f1.MD5)
	require.Equal(t, f1.MD5, fDull.MD5) // byte-identical files
}

// Cycle 9: SCAN.PHASH
func TestScan_PHASH(t *testing.T) {
	sc := scanner.New(nil, 1, false)
	files, err := sc.Scan(context.Background(), fixturesDir(t))
	require.NoError(t, err)
	f := findByName(files, "1.jpg")
	require.NotNil(t, f)
	err = sc.ComputePHash(context.Background(), []*scanner.FileInfo{f})
	require.NoError(t, err)
	require.NotEqual(t, uint64(0), f.PHash)
}

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
	require.NotEqual(t, f.PHash, f.PHash90)  // non-square → 0° and 90° differ
	require.NotEqual(t, f.PHash, f.PHash180) // guards against a [h,h,h,h] regression

	// 0° hash must equal what plain ComputePHash produces for the same file.
	files2, err := sc.Scan(context.Background(), fixturesDir(t))
	require.NoError(t, err)
	g := findByName(files2, "10.jpg")
	require.NotNil(t, g)
	require.NoError(t, sc.ComputePHash(context.Background(), []*scanner.FileInfo{g}))
	require.Equal(t, g.PHash, f.PHash)
}

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

// CACHE.ROT — a partial cache hit must fall through and recompute the full set
// rather than returning zeros for the missing rotations.
func TestScan_PHASH_ROT_CACHE_Partial(t *testing.T) {
	c, err := cache.Open(filepath.Join(t.TempDir(), "c.db"))
	require.NoError(t, err)
	defer c.Close()
	sc := scanner.New(c, 1, false)

	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join(fixturesDir(t), "10.jpg"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "10.jpg"), data, 0644))

	files, err := sc.Scan(context.Background(), dir)
	require.NoError(t, err)
	f := findByName(files, "10.jpg")
	require.NotNil(t, f)

	// Seed only two of the four rotation keys with bogus values.
	require.NoError(t, c.Set(f.Path, f.Mtime, f.Size, 1))
	require.NoError(t, c.Set(f.Path+"\x00r90", f.Mtime, f.Size, 2))

	require.NoError(t, sc.ComputePHashRotations(context.Background(), []*scanner.FileInfo{f}))
	require.NoError(t, f.Err)
	// Recomputed from the real image (not the bogus partial cache, not zeros).
	require.NotEqual(t, uint64(1), f.PHash)
	require.NotEqual(t, uint64(2), f.PHash90)
	require.NotEqual(t, uint64(0), f.PHash180)
	require.NotEqual(t, uint64(0), f.PHash270)
}
