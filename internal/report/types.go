package report

import (
	"time"

	"github.com/vp/mlt3/internal/comparator"
	"github.com/vp/mlt3/internal/scanner"
)

// Config holds configuration for report generation.
type Config struct {
	Args, Cwd, Originals, Unsorted, Dups string
}

// Group represents one original file and its duplicates.
type Group struct {
	Original   *scanner.FileInfo
	Duplicates []*comparator.Result
}

// Stats holds scan statistics for reporting.
type Stats struct {
	TotalFiles, IgnoredFiles, IgnoredFolders int
	Aborted                                  bool
	Progress                                 int
	Elapsed                                  time.Duration
}
