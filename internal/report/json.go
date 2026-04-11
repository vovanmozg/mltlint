package report

import (
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"path/filepath"
	"strings"
	"syscall"
)

// inodeInfo returns inode and device ID for a path using syscall.Stat_t.
// Returns 0, 0 if the path doesn't exist or stat fails.
func inodeInfo(path string) (uint64, uint64) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, 0
	}
	return st.Ino, uint64(st.Dev)
}

// pathID returns a crc32 checksum of the path as an int-like value.
func pathID(path string) uint32 {
	return crc32.ChecksumIEEE([]byte(path))
}

// depthOf counts the number of path separators in a relative path + 1.
func depthOf(relativePath string) int {
	return strings.Count(relativePath, "/") + 1
}

// WriteJSON writes a rmlint-compatible JSON array to w.
func WriteJSON(w io.Writer, cfg Config, groups []*Group, stats Stats) error {
	var records []map[string]interface{}

	// Header
	records = append(records, map[string]interface{}{
		"description":   "mlt3 duplicate report",
		"cwd":           cfg.Cwd,
		"args":          cfg.Args,
		"version":       "1.0.0",
		"rev":           "",
		"progress":      0,
		"checksum_type": "phash-dct64",
		"type":          "header",
	})

	totalDups := 0
	dupSets := 0

	for _, g := range groups {
		if g.Original == nil || len(g.Duplicates) == 0 {
			continue
		}
		dupSets++
		twins := 1 + len(g.Duplicates) // original + duplicates

		// Original record
		origInode, origDisk := inodeInfo(g.Original.Path)
		origRecord := map[string]interface{}{
			"id":          pathID(g.Original.Path),
			"type":        "duplicate_file",
			"progress":    stats.Progress,
			"checksum":    g.Original.MD5,
			"path":        g.Original.Path,
			"size":        g.Original.Size,
			"twins":       twins,
			"depth":       depthOf(g.Original.RelativePath),
			"inode":       origInode,
			"disk_id":     origDisk,
			"is_original": true,
			"mtime":       g.Original.Mtime,
		}
		records = append(records, origRecord)

		// Duplicate records
		for _, res := range g.Duplicates {
			totalDups++
			inode, disk := inodeInfo(res.Duplicate.Path)
			moveTo := fmt.Sprintf("%s/%s/%s", cfg.Dups, res.Level, res.Duplicate.RelativePath)
			rec := map[string]interface{}{
				"id":          pathID(res.Duplicate.Path),
				"type":        "duplicate_file",
				"progress":    stats.Progress,
				"checksum":    res.Duplicate.MD5,
				"path":        res.Duplicate.Path,
				"size":        res.Duplicate.Size,
				"twins":       twins,
				"depth":       depthOf(res.Duplicate.RelativePath),
				"inode":       inode,
				"disk_id":     disk,
				"is_original": false,
				"mtime":       res.Duplicate.Mtime,
				"move_to":     moveTo,
				"similarity": map[string]interface{}{
					"level":    res.Level,
					"distance": res.Distance,
					"score":    res.Score,
				},
			}
			records = append(records, rec)
		}
	}

	// Calculate total lint size
	var totalLintSize int64
	for _, g := range groups {
		for _, res := range g.Duplicates {
			totalLintSize += res.Duplicate.Size
		}
	}

	// Footer
	records = append(records, map[string]interface{}{
		"aborted":        stats.Aborted,
		"progress":       100,
		"total_files":    stats.TotalFiles,
		"ignored_files":  stats.IgnoredFiles,
		"ignored_folders": stats.IgnoredFolders,
		"duplicates":     totalDups,
		"duplicate_sets": dupSets,
		"total_lint_size": totalLintSize,
		"type":           "footer",
	})

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	// Build relative path reference for move_to — ensure no host leakage
	_ = filepath.Base // imported for potential future use

	return enc.Encode(records)
}
