package scanner_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
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
