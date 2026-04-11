package report_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vp/mlt3/internal/comparator"
	"github.com/vp/mlt3/internal/report"
	"github.com/vp/mlt3/internal/scanner"
)

func sampleConfig() report.Config {
	return report.Config{
		Args: "mltlint --originals=/orig --unsorted=/unso --dups=/dups",
		Cwd:  "/work", Originals: "/orig", Unsorted: "/unso", Dups: "/dups",
	}
}

func sampleGroup() *report.Group {
	return &report.Group{
		Original: &scanner.FileInfo{
			Path: "/orig/2023/photo.jpg", RelativePath: "2023/photo.jpg",
			Size: 8000, Mtime: 1700000000, MD5: "aaa", PHash: 100, Width: 250, Height: 250,
		},
		Duplicates: []*comparator.Result{{
			Duplicate: &scanner.FileInfo{
				Path: "/unso/2023/photo.jpg", RelativePath: "2023/photo.jpg",
				Size: 8000, Mtime: 1690000000, MD5: "aaa", PHash: 100, Width: 250, Height: 250,
			},
			Original: &scanner.FileInfo{Path: "/orig/2023/photo.jpg", RelativePath: "2023/photo.jpg", Size: 8000, Mtime: 1700000000, MD5: "aaa"},
			Level:    "similar", Distance: 0, Score: 1.0,
		}},
	}
}

func sampleStats() report.Stats {
	return report.Stats{TotalFiles: 10, Progress: 100}
}

func parseJSON(t *testing.T, data []byte) []map[string]interface{} {
	t.Helper()
	var result []map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &result))
	return result
}

func TestOutJSON(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.WriteJSON(&buf, sampleConfig(), []*report.Group{sampleGroup()}, sampleStats()))
	records := parseJSON(t, buf.Bytes())
	require.GreaterOrEqual(t, len(records), 3)
}

func TestOutJSON_HDR(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.WriteJSON(&buf, sampleConfig(), []*report.Group{sampleGroup()}, sampleStats()))
	records := parseJSON(t, buf.Bytes())
	hdr := records[0]
	require.Equal(t, "header", hdr["type"])
	require.NotEmpty(t, hdr["description"])
	require.Equal(t, "phash-dct64", hdr["checksum_type"])
}

func TestOutJSON_FTR(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.WriteJSON(&buf, sampleConfig(), []*report.Group{sampleGroup()}, sampleStats()))
	records := parseJSON(t, buf.Bytes())
	ftr := records[len(records)-1]
	require.Equal(t, "footer", ftr["type"])
	require.NotNil(t, ftr["total_files"])
	require.NotNil(t, ftr["duplicates"])
}

func TestOutJSON_REC(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.WriteJSON(&buf, sampleConfig(), []*report.Group{sampleGroup()}, sampleStats()))
	records := parseJSON(t, buf.Bytes())
	// Find a duplicate record (is_original == false)
	var dup map[string]interface{}
	for _, r := range records {
		if r["type"] == "duplicate_file" && r["is_original"] == false {
			dup = r
			break
		}
	}
	require.NotNil(t, dup, "expected a duplicate record")
	require.Equal(t, "/unso/2023/photo.jpg", dup["path"])
	require.NotEmpty(t, dup["move_to"])
}

func TestOutJSON_SIM(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.WriteJSON(&buf, sampleConfig(), []*report.Group{sampleGroup()}, sampleStats()))
	records := parseJSON(t, buf.Bytes())
	var dup map[string]interface{}
	for _, r := range records {
		if r["type"] == "duplicate_file" && r["is_original"] == false {
			dup = r
			break
		}
	}
	require.NotNil(t, dup)
	sim, ok := dup["similarity"].(map[string]interface{})
	require.True(t, ok, "similarity should be an object")
	require.NotEmpty(t, sim["level"])
	require.NotNil(t, sim["distance"])
	require.NotNil(t, sim["score"])
}

func TestOutJSON_ORIG(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.WriteJSON(&buf, sampleConfig(), []*report.Group{sampleGroup()}, sampleStats()))
	records := parseJSON(t, buf.Bytes())
	var orig map[string]interface{}
	for _, r := range records {
		if r["type"] == "duplicate_file" && r["is_original"] == true {
			orig = r
			break
		}
	}
	require.NotNil(t, orig, "expected an original record")
	require.Equal(t, "/orig/2023/photo.jpg", orig["path"])
}

func TestOutJSON_TWINS(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.WriteJSON(&buf, sampleConfig(), []*report.Group{sampleGroup()}, sampleStats()))
	records := parseJSON(t, buf.Bytes())
	for _, r := range records {
		if r["type"] == "duplicate_file" {
			require.Equal(t, float64(2), r["twins"], "twins should be 2 (original + 1 dup)")
		}
	}
}

func TestOutJSON_HOST(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.WriteJSON(&buf, sampleConfig(), []*report.Group{sampleGroup()}, sampleStats()))
	require.False(t, strings.Contains(buf.String(), "/vt/"), "output must not contain /vt/")
}
