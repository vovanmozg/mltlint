package comparator_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vp/mlt3/internal/comparator"
	"github.com/vp/mlt3/internal/scanner"
)

type noopHashComputer struct{}

func (n *noopHashComputer) ComputeMD5(_ context.Context, _ []*scanner.FileInfo) error {
	return nil
}
func (n *noopHashComputer) ComputePHash(_ context.Context, _ []*scanner.FileInfo) error {
	return nil
}

var noop = &noopHashComputer{}

func fi(path, md5 string, phash uint64, w, h int, size int64) *scanner.FileInfo {
	return &scanner.FileInfo{Path: path, RelativePath: path, MD5: md5, PHash: phash, Width: w, Height: h, Size: size}
}

// Cycle 1: CMP.EMPTY — no match found → nil result.
func TestCmp_EMPTY(t *testing.T) {
	o := fi("/originals/photo.jpg", "aaa", 100, 250, 250, 8000)
	u := fi("/unsorted/unique.png", "ddd", 999, 545, 545, 370000)
	indexes := comparator.BuildIndexes([]*scanner.FileInfo{o})
	r, err := comparator.Classify(context.Background(), u, indexes, noop)
	require.NoError(t, err)
	require.Nil(t, r)
}

// Cycle 2: CMP.SIM — same phash (distance=0), different MD5 and size → similar via phash path.
func TestCmp_SIM(t *testing.T) {
	o := fi("/originals/photo.jpg", "bbb", 100, 250, 250, 18000)
	u := fi("/unsorted/smaller.jpg", "bbb2", 100, 240, 240, 18001) // different size to skip MD5 path
	indexes := comparator.BuildIndexes([]*scanner.FileInfo{o})
	r, err := comparator.Classify(context.Background(), u, indexes, noop)
	require.NoError(t, err)
	require.NotNil(t, r)
	require.Equal(t, "similar", r.Level)
	require.Equal(t, 0, r.Distance)
}

// Cycle 3: CMP.SIM.MD5 — same MD5 and same size → similar via MD5 fast path, even with different phash.
func TestCmp_SIM_MD5(t *testing.T) {
	o := fi("/originals/photo.jpg", "aaa", 100, 250, 250, 8000)
	u := fi("/unsorted/photo.jpg", "aaa", 200, 250, 250, 8000) // same MD5+size, different phash
	indexes := comparator.BuildIndexes([]*scanner.FileInfo{o})
	r, err := comparator.Classify(context.Background(), u, indexes, noop)
	require.NoError(t, err)
	require.NotNil(t, r)
	require.Equal(t, "similar", r.Level)
	require.Equal(t, 0, r.Distance)
	require.Equal(t, 1.0, r.Score)
}

// Cycle 4: CMP.SIM.NODUP — file that matches as similar (via MD5) should not also show as doubtful.
func TestCmp_SIM_NODUP(t *testing.T) {
	o := fi("/originals/photo.jpg", "aaa", 100, 250, 250, 8000)
	u := fi("/unsorted/photo.jpg", "aaa", 100, 250, 250, 8000)
	indexes := comparator.BuildIndexes([]*scanner.FileInfo{o})
	r, err := comparator.Classify(context.Background(), u, indexes, noop)
	require.NoError(t, err)
	require.NotNil(t, r)
	require.Equal(t, "similar", r.Level) // not doubtful
}

// Cycle 5: CMP.DOUBT — phash distance in [1,2] → doubtful. hamming(100, 101) = 1 (XOR=1, popcount=1).
func TestCmp_DOUBT(t *testing.T) {
	o := fi("/originals/photo.jpg", "aaa", 100, 250, 250, 8000)
	u := fi("/unsorted/edited.jpg", "ccc", 101, 250, 250, 9000) // distance 1
	indexes := comparator.BuildIndexes([]*scanner.FileInfo{o})
	r, err := comparator.Classify(context.Background(), u, indexes, noop)
	require.NoError(t, err)
	require.NotNil(t, r)
	require.Equal(t, "doubtful", r.Level)
	require.LessOrEqual(t, r.Distance, 2)
}

// Cycle 6: CMP.DOUBT.RATIO — different aspect ratio → no match even with close phash.
func TestCmp_DOUBT_RATIO(t *testing.T) {
	o := fi("/originals/photo.jpg", "aaa", 100, 250, 250, 8000) // ratio 1.0
	u := fi("/unsorted/wide.jpg", "eee", 101, 400, 200, 9000)   // ratio 2.0
	indexes := comparator.BuildIndexes([]*scanner.FileInfo{o})
	r, err := comparator.Classify(context.Background(), u, indexes, noop)
	require.NoError(t, err)
	require.Nil(t, r) // different ratio → no match
}

// Cycle 7: CMP.DOUBT.RATIO.OK — same ratio, close phash → doubtful.
func TestCmp_DOUBT_RATIO_OK(t *testing.T) {
	o := fi("/originals/photo.jpg", "aaa", 100, 250, 250, 8000)  // ratio 1.0
	u := fi("/unsorted/bigger.jpg", "fff", 101, 500, 500, 12000) // ratio 1.0, distance 1
	indexes := comparator.BuildIndexes([]*scanner.FileInfo{o})
	r, err := comparator.Classify(context.Background(), u, indexes, noop)
	require.NoError(t, err)
	require.NotNil(t, r)
	require.Equal(t, "doubtful", r.Level)
}

// Cycle 8: CMP.CLOSEST — multiple originals, pick closest (min distance). Ties broken by path order.
func TestCmp_CLOSEST(t *testing.T) {
	o1 := fi("/originals/a.jpg", "x", 100, 250, 250, 8000)
	o2 := fi("/originals/b.jpg", "y", 103, 250, 250, 9000) // hamming(100,103)=2
	u := fi("/unsorted/u.jpg", "z", 101, 250, 250, 10000)  // hamming(100,101)=1, hamming(103,101)=2
	indexes := comparator.BuildIndexes([]*scanner.FileInfo{o1, o2})
	r, err := comparator.Classify(context.Background(), u, indexes, noop)
	require.NoError(t, err)
	require.NotNil(t, r)
	require.Equal(t, "/originals/a.jpg", r.Original.Path) // closer
	require.Equal(t, 1, r.Distance)
}

// Cycle 9: CMP.SCORE — test exact score values.
func TestCmp_SCORE(t *testing.T) {
	o := fi("/originals/photo.jpg", "aaa", 100, 250, 250, 8000)

	t.Run("similar score", func(t *testing.T) {
		u := fi("/unsorted/same.jpg", "aaa", 100, 250, 250, 8000)
		indexes := comparator.BuildIndexes([]*scanner.FileInfo{o})
		r, err := comparator.Classify(context.Background(), u, indexes, noop)
		require.NoError(t, err)
		require.NotNil(t, r)
		require.Equal(t, 1.0, r.Score)
		require.Equal(t, 0, r.Distance)
	})

	t.Run("doubtful dist=1 score", func(t *testing.T) {
		u := fi("/unsorted/close.jpg", "bbb", 101, 250, 250, 9000) // hamming(100,101)=1
		indexes := comparator.BuildIndexes([]*scanner.FileInfo{o})
		r, err := comparator.Classify(context.Background(), u, indexes, noop)
		require.NoError(t, err)
		require.NotNil(t, r)
		require.Equal(t, 0.67, r.Score)
		require.Equal(t, 1, r.Distance)
	})

	t.Run("doubtful dist=2 score", func(t *testing.T) {
		// Need phash where hamming(100, x) = 2. 100=0b1100100, 97=0b1100001, XOR=0b0000101 → popcount=2
		u := fi("/unsorted/far.jpg", "ccc", 97, 250, 250, 10000)
		indexes := comparator.BuildIndexes([]*scanner.FileInfo{o})
		r, err := comparator.Classify(context.Background(), u, indexes, noop)
		require.NoError(t, err)
		require.NotNil(t, r)
		require.Equal(t, 0.33, r.Score)
		require.Equal(t, 2, r.Distance)
	})
}
