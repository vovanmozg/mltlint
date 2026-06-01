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

// CMP.CLOSEST — when a rotated match (via one original's 90° hash) ties with a
// same-orientation match (another original's 0° hash) at distance 0, the lower
// path wins, regardless of which orientation produced the hit.
func TestCmp_ROT_TIE(t *testing.T) {
	a := &scanner.FileInfo{
		Path: "/originals/a.jpg", RelativePath: "a.jpg",
		PHash: 700, PHash90: 600, PHash180: 701, PHash270: 601,
		Width: 250, Height: 250, Size: 8000,
	}
	b := &scanner.FileInfo{
		Path: "/originals/b.jpg", RelativePath: "b.jpg",
		PHash: 600, PHash90: 800, PHash180: 610, PHash270: 810,
		Width: 250, Height: 250, Size: 9000,
	}
	// u matches a via a.PHash90 (dist 0) and b via b.PHash (dist 0) — a wins by path.
	u := &scanner.FileInfo{
		Path: "/unsorted/u.jpg", RelativePath: "u.jpg",
		MD5: "ttt", PHash: 600, Width: 250, Height: 250, Size: 10000,
	}
	indexes := comparator.BuildIndexes([]*scanner.FileInfo{b, a}) // input order shuffled
	r, err := comparator.Classify(context.Background(), u, indexes, noop)
	require.NoError(t, err)
	require.NotNil(t, r)
	require.Equal(t, "/originals/a.jpg", r.Original.Path) // lower path wins the tie
	require.Equal(t, 0, r.Distance)
}

// CMP.ROT.RATIO — a non-square original is NOT matched by an unsorted file at a
// different aspect ratio whose hash is far from the orientations indexed there.
func TestCmp_ROT_NORATIO(t *testing.T) {
	o := &scanner.FileInfo{
		Path: "/originals/wide.jpg", RelativePath: "wide.jpg",
		PHash: 100, PHash90: 500, PHash180: 110, PHash270: 510,
		Width: 400, Height: 200, Size: 8000,
	}
	// Portrait file (ratio 0.5) lands in the swapped bucket {500, 510}; its hash
	// is far from both, and the landscape bucket {100, 110} is never consulted.
	u := &scanner.FileInfo{
		Path: "/unsorted/tall.jpg", RelativePath: "tall.jpg",
		MD5: "uuu", PHash: 999, Width: 200, Height: 400, Size: 8200,
	}
	indexes := comparator.BuildIndexes([]*scanner.FileInfo{o})
	r, err := comparator.Classify(context.Background(), u, indexes, noop)
	require.NoError(t, err)
	require.Nil(t, r)
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
