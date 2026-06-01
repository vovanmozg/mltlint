package main_test

import (
	"encoding/json"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "mltlint")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir, _ = os.Getwd() // cmd/mltlint dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "build failed: %s", out)
	return bin
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../..")
	require.NoError(t, err)
	return dir
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dst, data, 0644))
}

func setupTestDirs(t *testing.T) (originals, unsorted, dups, output string) {
	t.Helper()
	base := t.TempDir()
	originals = filepath.Join(base, "originals")
	unsorted = filepath.Join(base, "unsorted")
	dups = filepath.Join(base, "dups")
	output = filepath.Join(base, "output")
	for _, d := range []string{originals, unsorted, dups, output} {
		require.NoError(t, os.MkdirAll(d, 0755))
	}

	fixturesDir := filepath.Join(repoRoot(t), "testdata", "media")

	// originals gets 1.jpg and 9.png
	copyFile(t, filepath.Join(fixturesDir, "1.jpg"), filepath.Join(originals, "1.jpg"))
	copyFile(t, filepath.Join(fixturesDir, "9.png"), filepath.Join(originals, "9.png"))

	// unsorted gets dull.jpg (byte-identical to 1.jpg → should match) and 10.jpg (unique)
	copyFile(t, filepath.Join(fixturesDir, "dull.jpg"), filepath.Join(unsorted, "dull.jpg"))
	copyFile(t, filepath.Join(fixturesDir, "10.jpg"), filepath.Join(unsorted, "10.jpg"))

	return
}

func TestE2E_BasicFlow(t *testing.T) {
	bin := buildBinary(t)
	originals, unsorted, dups, output := setupTestDirs(t)
	cachePath := filepath.Join(t.TempDir(), "cache.db")

	cmd := exec.Command(bin,
		"--originals="+originals, "--unsorted="+unsorted,
		"--dups="+dups, "--output="+output, "--cache="+cachePath)
	out, err := cmd.CombinedOutput()
	t.Logf("output:\n%s", out)
	require.NoError(t, err)

	// Parse JSON
	jsonData, err := os.ReadFile(filepath.Join(output, "mltlint.json"))
	require.NoError(t, err)
	var records []map[string]interface{}
	require.NoError(t, json.Unmarshal(jsonData, &records))
	require.GreaterOrEqual(t, len(records), 3) // header + at least 1 record + footer

	require.Equal(t, "header", records[0]["type"])
	footer := records[len(records)-1]
	require.Equal(t, "footer", footer["type"])
	require.Equal(t, false, footer["aborted"])

	hasOrig, hasDup := false, false
	for _, r := range records[1 : len(records)-1] {
		if r["is_original"] == true {
			hasOrig = true
		}
		if r["is_original"] == false {
			hasDup = true
		}
	}
	require.True(t, hasOrig)
	require.True(t, hasDup)

	// Parse shell
	shData, err := os.ReadFile(filepath.Join(output, "mltlint.sh"))
	require.NoError(t, err)
	sh := string(shData)
	require.True(t, strings.HasPrefix(sh, "#!/usr/bin/env bash\n"))
	require.Contains(t, sh, "move_cmd()")
	require.Contains(t, sh, "original_cmd")
	require.Contains(t, sh, "move_cmd '")
}

func TestE2E_EmptyOriginals(t *testing.T) {
	bin := buildBinary(t)
	base := t.TempDir()
	originals := filepath.Join(base, "originals")
	unsorted := filepath.Join(base, "unsorted")
	dups := filepath.Join(base, "dups")
	output := filepath.Join(base, "output")
	for _, d := range []string{originals, unsorted, output} {
		require.NoError(t, os.MkdirAll(d, 0755))
	}
	copyFile(t, filepath.Join(repoRoot(t), "testdata", "media", "1.jpg"), filepath.Join(unsorted, "1.jpg"))

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
	require.Equal(t, float64(0), footer["duplicates"])
	require.Equal(t, float64(0), footer["duplicate_sets"])
}

func TestE2E_AbortedRun(t *testing.T) {
	bin := buildBinary(t)
	originals, unsorted, dups, output := setupTestDirs(t)
	cachePath := filepath.Join(t.TempDir(), "cache.db")

	cmd := exec.Command(bin,
		"--originals="+originals, "--unsorted="+unsorted,
		"--dups="+dups, "--output="+output, "--cache="+cachePath)
	require.NoError(t, cmd.Start())

	// Send SIGINT after process starts
	go func() {
		for cmd.Process == nil {
			// spin until process exists
		}
		cmd.Process.Signal(os.Interrupt)
	}()

	cmd.Wait()

	// If JSON was produced, it should have aborted=true
	jsonPath := filepath.Join(output, "mltlint.json")
	if data, err := os.ReadFile(jsonPath); err == nil {
		var records []map[string]interface{}
		if json.Unmarshal(data, &records) == nil && len(records) > 0 {
			footer := records[len(records)-1]
			if footer["type"] == "footer" {
				require.Equal(t, true, footer["aborted"])
			}
		}
	}
	// If no JSON was written, that's also OK (interrupted during phase 1)
}

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
	require.GreaterOrEqual(t, footer["duplicates"].(float64), float64(1))

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
