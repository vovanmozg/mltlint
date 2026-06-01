package comparator

import (
	"context"
	"math"
	"math/bits"
	"sort"

	"github.com/vp/mlt3/internal/scanner"
)

const doubtfulThreshold = 2

// Result holds the classification result for an unsorted file.
type Result struct {
	Duplicate *scanner.FileInfo
	Original  *scanner.FileInfo
	Level     string // "similar" | "doubtful"
	Distance  int
	Score     float64
}

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

// HashComputer is the interface used to compute hashes on demand.
type HashComputer interface {
	ComputeMD5(ctx context.Context, files []*scanner.FileInfo) error
	ComputePHash(ctx context.Context, files []*scanner.FileInfo) error
}

// Ratio returns width/height * 10, rounded to 1 decimal. Returns 0 if either dimension is 0.
func Ratio(f *scanner.FileInfo) float64 {
	if f.Width == 0 || f.Height == 0 {
		return 0
	}
	return math.Round(float64(f.Width)/float64(f.Height)*10) / 10
}

// RatioSwapped returns the aspect ratio of the file with width/height swapped
// (i.e. the ratio it has after a 90°/270° rotation), computed directly from
// dimensions to avoid compounding the rounding already applied by Ratio.
func RatioSwapped(f *scanner.FileInfo) float64 {
	if f.Width == 0 || f.Height == 0 {
		return 0
	}
	return math.Round(float64(f.Height)/float64(f.Width)*10) / 10
}

// BuildIndexes builds BySize and ByRatio indexes over originals.
// Originals are sorted by Path for stable tie-breaking. Files with Err != nil are skipped.
//
// The 0° hash (PHash) is always indexed. The 90/180/270° rotation hashes are
// indexed only when populated (non-zero) — they are zero for an original hashed
// with ComputePHash instead of ComputePHashRotations, and a real image never has
// a true-zero perceptual hash, so skipping zeros keeps rotation matching opt-in
// without ever fabricating a match against an absent orientation.
func BuildIndexes(originals []*scanner.FileInfo) *Indexes {
	sorted := make([]*scanner.FileInfo, 0, len(originals))
	for _, o := range originals {
		if o.Err == nil {
			sorted = append(sorted, o)
		}
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Path < sorted[j].Path
	})

	bySize := make(map[int64][]*scanner.FileInfo)
	byRatio := make(map[float64][]rotCandidate)

	for _, o := range sorted {
		bySize[o.Size] = append(bySize[o.Size], o)

		r := Ratio(o)         // 0° and 180° keep the original ratio
		rs := RatioSwapped(o) // 90° and 270° invert width/height

		// 0° is always present; rotations are indexed only when computed.
		byRatio[r] = append(byRatio[r], rotCandidate{hash: o.PHash, original: o})
		if o.PHash180 != 0 {
			byRatio[r] = append(byRatio[r], rotCandidate{hash: o.PHash180, original: o})
		}
		if o.PHash90 != 0 {
			byRatio[rs] = append(byRatio[rs], rotCandidate{hash: o.PHash90, original: o})
		}
		if o.PHash270 != 0 {
			byRatio[rs] = append(byRatio[rs], rotCandidate{hash: o.PHash270, original: o})
		}
	}

	return &Indexes{BySize: bySize, ByRatio: byRatio}
}

// hammingDistance returns the number of differing bits between two uint64 values.
func hammingDistance(a, b uint64) int {
	return bits.OnesCount64(a ^ b)
}

// roundScore rounds a float64 to 2 decimal places.
func roundScore(v float64) float64 {
	return math.Round(v*100) / 100
}

// Classify classifies a single unsorted file against the indexes.
func Classify(ctx context.Context, file *scanner.FileInfo, indexes *Indexes, hc HashComputer) (*Result, error) {
	// Step 1: MD5 fast path — check if any originals share the same size.
	if candidates, ok := indexes.BySize[file.Size]; ok && len(candidates) > 0 {
		// Compute MD5 for the file.
		if err := hc.ComputeMD5(ctx, []*scanner.FileInfo{file}); err != nil {
			return nil, err
		}
		// Compute MD5 for all candidates that don't have one yet.
		if err := hc.ComputeMD5(ctx, candidates); err != nil {
			return nil, err
		}
		for _, c := range candidates {
			if file.MD5 != "" && c.MD5 != "" && file.MD5 == c.MD5 {
				return &Result{
					Duplicate: file,
					Original:  c,
					Level:     "similar",
					Distance:  0,
					Score:     1.0,
				}, nil
			}
		}
	}

	// Step 2: PHash path — compute phash for the file.
	if err := hc.ComputePHash(ctx, []*scanner.FileInfo{file}); err != nil {
		return nil, err
	}

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

	if bestOriginal == nil {
		return nil, nil
	}

	// Step 4-6: classify by distance.
	switch {
	case bestDist == 0:
		return &Result{
			Duplicate: file,
			Original:  bestOriginal,
			Level:     "similar",
			Distance:  0,
			Score:     1.0,
		}, nil
	case bestDist <= doubtfulThreshold:
		score := roundScore(1.0 - float64(bestDist)/3.0)
		return &Result{
			Duplicate: file,
			Original:  bestOriginal,
			Level:     "doubtful",
			Distance:  bestDist,
			Score:     score,
		}, nil
	default:
		return nil, nil
	}
}
