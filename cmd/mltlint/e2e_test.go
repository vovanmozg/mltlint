package main_test

import (
	"encoding/json"
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
