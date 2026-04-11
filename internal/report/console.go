package report

import (
	"fmt"
	"io"
)

const (
	colGreen  = "\033[0;32m"
	colYellow = "\033[0;33m"
	colBlue   = "\033[1;34m"
	colReset  = "\033[0m"
)

// WriteConsoleSummary writes a human-readable duplicate summary to w.
func WriteConsoleSummary(w io.Writer, cfg Config, groups []*Group, stats Stats) error {
	_, err := fmt.Fprintln(w, "# Duplicate(s):")
	if err != nil {
		return err
	}

	totalDups := 0
	var totalLintSize int64

	for _, g := range groups {
		if g.Original == nil || len(g.Duplicates) == 0 {
			continue
		}
		if _, err := fmt.Fprintf(w, "    %soriginal%s '%s'\n", colGreen, colReset, g.Original.Path); err != nil {
			return err
		}
		for _, res := range g.Duplicates {
			totalDups++
			totalLintSize += res.Duplicate.Size
			if _, err := fmt.Fprintf(w, "    %s%-8s%s '%s' (dist=%d)\n", colYellow, res.Level, colReset, res.Duplicate.Path, res.Distance); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}

	dupSets := 0
	for _, g := range groups {
		if g.Original != nil && len(g.Duplicates) > 0 {
			dupSets++
		}
	}

	sizeKB := float64(totalLintSize) / 1024.0
	elapsed := stats.Elapsed.Seconds()

	if _, err := fmt.Fprintf(w, "%s==>%s Note: Please use the saved script below for moving, not the above output.\n", colBlue, colReset); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "%s==>%s In total %s%d%s files, whereof %s%d%s are duplicates in %s%d%s groups.\n",
		colBlue, colReset, colYellow, stats.TotalFiles, colReset, colYellow, totalDups, colReset, colYellow, dupSets, colReset); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "%s==>%s This equals %s%.2f KB%s of duplicates which could be moved.\n", colBlue, colReset, colYellow, sizeKB, colReset); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "%s==>%s Scanning took in total %s%.1fs%s.\n", colBlue, colReset, colYellow, elapsed, colReset); err != nil {
		return err
	}
	return nil
}
