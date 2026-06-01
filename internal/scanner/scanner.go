package scanner

import (
	"context"
	"crypto/md5"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/corona10/goimagehash"
	"github.com/vp/mlt3/internal/cache"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// supportedExts is the set of file extensions we consider candidates.
var supportedExts = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".gif":  true,
	".bmp":  true,
	".tif":  true,
	".tiff": true,
	".webp": true,
}

// Rotation cache-key suffixes. NUL is forbidden in filesystem paths, so these
// derived keys cannot collide with a real file's path.
const (
	rotSuffix90  = "\x00r90"
	rotSuffix180 = "\x00r180"
	rotSuffix270 = "\x00r270"
)

// FileInfo holds metadata and lazily-computed hashes for a single media file.
type FileInfo struct {
	Path         string
	RelativePath string
	Size         int64
	Mtime        int64
	Width        int
	Height       int
	MD5          string // empty until ComputeMD5
	PHash        uint64 // 0° hash; zero until ComputePHash/ComputePHashRotations
	PHash90      uint64 // 90° hash; zero until ComputePHashRotations
	PHash180     uint64 // 180° hash; zero until ComputePHashRotations
	PHash270     uint64 // 270° hash; zero until ComputePHashRotations
	Err          error  // per-file soft error, or nil
}

// Scanner walks directories and computes hashes on demand.
type Scanner struct {
	cache   *cache.Cache
	workers int
	debug   bool
}

// New creates a new Scanner.
func New(c *cache.Cache, workers int, debug bool) *Scanner {
	if workers <= 0 {
		workers = 1
	}
	return &Scanner{
		cache:   c,
		workers: workers,
		debug:   debug,
	}
}

// Scan walks dir and returns FileInfo for each valid media file found.
// It skips empty files and files whose content type is not "image/".
func (s *Scanner) Scan(ctx context.Context, dir string) ([]*FileInfo, error) {
	var files []*FileInfo

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}
		if d.IsDir() {
			return nil
		}

		// Check extension first (cheap filter).
		ext := strings.ToLower(filepath.Ext(path))
		if !supportedExts[ext] {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		// Skip empty files.
		if info.Size() == 0 {
			return nil
		}

		// Detect content type using first 512 bytes.
		contentType, readErr := detectContentType(path)
		if readErr != nil || !strings.HasPrefix(contentType, "image/") {
			return nil
		}

		// Read image dimensions.
		width, height := readDimensions(path)

		// Compute relative path from the scan root.
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			rel = filepath.Base(path)
		}

		fi := &FileInfo{
			Path:         path,
			RelativePath: rel,
			Size:         info.Size(),
			Mtime:        info.ModTime().Unix(),
			Width:        width,
			Height:       height,
		}

		files = append(files, fi)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return files, nil
}

// detectContentType reads up to 512 bytes and returns the MIME content type.
func detectContentType(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return "", err
	}
	return http.DetectContentType(buf[:n]), nil
}

// readDimensions decodes image config to extract width and height.
// Returns 0, 0 on failure (soft — caller still includes the file).
func readDimensions(path string) (int, int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()

	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// ComputeMD5 fills in the MD5 field for each file using parallel workers.
func (s *Scanner) ComputeMD5(ctx context.Context, files []*FileInfo) error {
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

			hash, err := computeFileMD5(fi.Path)
			if err != nil {
				fi.Err = err
				return
			}
			fi.MD5 = hash
		}()
	}

	wg.Wait()
	return nil
}

// computeFileMD5 reads the file and returns its hex MD5 digest.
func computeFileMD5(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// ComputePHash fills in the PHash field for each file using parallel workers.
// It consults the cache (if available) before computing.
func (s *Scanner) ComputePHash(ctx context.Context, files []*FileInfo) error {
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

			// Check cache first.
			if s.cache != nil {
				if h, ok := s.cache.Get(fi.Path, fi.Mtime, fi.Size); ok {
					fi.PHash = h
					return
				}
			}

			hash, err := computeFilePHash(fi.Path)
			if err != nil {
				fi.Err = err
				return
			}
			fi.PHash = hash

			// Store in cache.
			if s.cache != nil {
				_ = s.cache.Set(fi.Path, fi.Mtime, fi.Size, hash)
			}
		}()
	}

	wg.Wait()
	return nil
}

// computeFilePHash decodes the image and returns its perceptual hash.
func computeFilePHash(path string) (uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return 0, err
	}

	h, err := goimagehash.PerceptionHash(img)
	if err != nil {
		return 0, err
	}
	return h.GetHash(), nil
}

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

// computeFilePHashRotations decodes the image once and returns the perceptual
// hashes of its four cardinal rotations: [0°, 90°, 180°, 270°].
// Rotations are hashed one at a time, so only a couple of rotated copies are
// live at once and peak memory stays a small multiple of the decoded image.
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

// ComputePHashRotations fills PHash, PHash90, PHash180, PHash270 for each file
// using parallel workers. Each file is decoded once. Unlike ComputePHash, which
// computes only the 0° hash, this computes all four cardinal orientations and
// caches each under its own key.
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

			// Cache fast path: skip decode only when all four rotations are
			// cached. A partial hit means the set is inconsistent (e.g. left by
			// an older binary), so we fall through and recompute the whole set.
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
		}()
	}

	wg.Wait()
	return nil
}
