package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/vp/mlt3/internal/cache"
	"github.com/vp/mlt3/internal/comparator"
	"github.com/vp/mlt3/internal/report"
	"github.com/vp/mlt3/internal/scanner"
)

type config struct {
	Originals string
	Unsorted  string
	Dups      string
	Output    string
	CachePath string
	Workers   int
	Debug     bool
}

func defaultCachePath() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "mltlint", "cache.db")
}

func main() {
	os.Exit(run())
}

func run() int {
	var cfg config

	flag.StringVar(&cfg.Originals, "originals", "", "Directory with existing originals (required)")
	flag.StringVar(&cfg.Unsorted, "unsorted", "", "Directory with files to check (required)")
	flag.StringVar(&cfg.Dups, "dups", "", "Directory for move destinations (required)")
	flag.StringVar(&cfg.Output, "output", ".", "Where to write mltlint.json/sh")
	flag.StringVar(&cfg.CachePath, "cache", defaultCachePath(), "Path to cache database")
	flag.IntVar(&cfg.Workers, "workers", runtime.NumCPU(), "Number of parallel workers")
	flag.BoolVar(&cfg.Debug, "debug", false, "Verbose logging to stderr")
	flag.Parse()

	if cfg.Originals == "" || cfg.Unsorted == "" || cfg.Dups == "" {
		fmt.Fprintln(os.Stderr, "mltlint: --originals, --unsorted, and --dups are required")
		flag.Usage()
		return 1
	}

	return runPipeline(cfg)
}

func runPipeline(cfg config) int {
	start := time.Now()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Open cache (create dir if needed).
	if err := os.MkdirAll(filepath.Dir(cfg.CachePath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "mltlint: create cache dir: %v\n", err)
		return 1
	}
	c, err := cache.Open(cfg.CachePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mltlint: open cache: %v\n", err)
		return 1
	}
	defer c.Close()

	sc := scanner.New(c, cfg.Workers, cfg.Debug)

	// Phase 1: originals.
	fmt.Fprintln(os.Stderr, "[phase 1/2] Scanning originals...")
	origFiles, err := sc.Scan(ctx, cfg.Originals)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mltlint: scan originals: %v\n", err)
		return 1
	}

	// Check for interrupt after phase 1 scan.
	select {
	case <-ctx.Done():
		fmt.Fprintln(os.Stderr, "mltlint: interrupted during phase 1")
		return 130
	default:
	}

	fmt.Fprintf(os.Stderr, "[phase 1/2] originals: scanned %d, computing phash (4 rotations)...\n", len(origFiles))
	if err := sc.ComputePHashRotations(ctx, origFiles); err != nil {
		fmt.Fprintf(os.Stderr, "mltlint: compute phash for originals: %v\n", err)
		return 1
	}

	// Check for interrupt after phase 1 phash.
	select {
	case <-ctx.Done():
		fmt.Fprintln(os.Stderr, "mltlint: interrupted during phase 1")
		return 130
	default:
	}

	indexes := comparator.BuildIndexes(origFiles)
	fmt.Fprintf(os.Stderr, "[phase 1/2] originals: indexed %d files\n", len(origFiles))

	// Phase 2: unsorted.
	fmt.Fprintln(os.Stderr, "[phase 2/2] Scanning unsorted...")
	unsortedFiles, err := sc.Scan(ctx, cfg.Unsorted)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mltlint: scan unsorted: %v\n", err)
		return 1
	}

	// Check for interrupt after phase 2 scan.
	aborted := false
	select {
	case <-ctx.Done():
		aborted = true
	default:
	}

	if !aborted {
		fmt.Fprintf(os.Stderr, "[phase 2/2] unsorted: scanned %d, computing phash...\n", len(unsortedFiles))
		if err := sc.ComputePHash(ctx, unsortedFiles); err != nil {
			fmt.Fprintf(os.Stderr, "mltlint: compute phash for unsorted: %v\n", err)
			return 1
		}
		select {
		case <-ctx.Done():
			aborted = true
		default:
		}
	}

	// Classify and group.
	groups := classifyAndGroup(ctx, unsortedFiles, indexes, sc, &aborted)

	// Build stats.
	ignoredFiles := 0
	for _, f := range unsortedFiles {
		if f.Err != nil {
			ignoredFiles++
		}
	}
	for _, f := range origFiles {
		if f.Err != nil {
			ignoredFiles++
		}
	}

	elapsed := time.Since(start)
	stats := report.Stats{
		TotalFiles:   len(origFiles) + len(unsortedFiles),
		IgnoredFiles: ignoredFiles,
		Aborted:      aborted,
		Progress:     100,
		Elapsed:      elapsed,
	}

	// Build report config.
	cwd, _ := os.Getwd()
	args := buildArgs(cfg)
	rpCfg := report.Config{
		Args:      args,
		Cwd:       cwd,
		Originals: cfg.Originals,
		Unsorted:  cfg.Unsorted,
		Dups:      cfg.Dups,
	}

	// Write output files atomically.
	jsonPath := filepath.Join(cfg.Output, "mltlint.json")
	shPath := filepath.Join(cfg.Output, "mltlint.sh")

	if err := os.MkdirAll(cfg.Output, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "mltlint: create output dir: %v\n", err)
		return 1
	}

	if err := writeJSONAtomic(jsonPath, rpCfg, groups, stats); err != nil {
		fmt.Fprintf(os.Stderr, "mltlint: write json: %v\n", err)
		return 1
	}

	if err := writeShellAtomic(shPath, rpCfg, groups); err != nil {
		fmt.Fprintf(os.Stderr, "mltlint: write shell: %v\n", err)
		return 1
	}

	// Console summary.
	if err := report.WriteConsoleSummary(os.Stdout, rpCfg, groups, stats); err != nil {
		fmt.Fprintf(os.Stderr, "mltlint: write console summary: %v\n", err)
	}
	fmt.Fprintf(os.Stdout, "Wrote a json file to: %s\n", jsonPath)
	fmt.Fprintf(os.Stdout, "Wrote a sh file to: %s\n", shPath)

	if aborted {
		return 130
	}
	if ignoredFiles > 0 {
		return 2
	}
	return 0
}

// classifyAndGroup classifies unsorted files and groups them by original path.
// It maintains insertion order for deterministic output.
func classifyAndGroup(
	ctx context.Context,
	unsortedFiles []*scanner.FileInfo,
	indexes *comparator.Indexes,
	hc comparator.HashComputer,
	aborted *bool,
) []*report.Group {
	// Use a slice of keys to maintain insertion order.
	var order []string
	groupMap := make(map[string]*report.Group)

	for _, f := range unsortedFiles {
		if *aborted {
			break
		}

		select {
		case <-ctx.Done():
			*aborted = true
			break
		default:
		}
		if *aborted {
			break
		}

		res, err := comparator.Classify(ctx, f, indexes, hc)
		if err != nil {
			// Soft error — count as ignored but continue.
			f.Err = err
			continue
		}
		if res == nil {
			continue
		}

		origPath := res.Original.Path
		if _, exists := groupMap[origPath]; !exists {
			groupMap[origPath] = &report.Group{Original: res.Original}
			order = append(order, origPath)
		}
		groupMap[origPath].Duplicates = append(groupMap[origPath].Duplicates, res)
	}

	groups := make([]*report.Group, 0, len(order))
	for _, key := range order {
		groups = append(groups, groupMap[key])
	}
	return groups
}

// writeJSONAtomic writes the JSON report to a temp file then renames it.
func writeJSONAtomic(path string, cfg report.Config, groups []*report.Group, stats report.Stats) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := report.WriteJSON(f, cfg, groups, stats); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// writeShellAtomic writes the shell script to a temp file then renames it.
func writeShellAtomic(path string, cfg report.Config, groups []*report.Group) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := report.WriteShell(f, cfg, groups); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// buildArgs reconstructs a CLI argument string from the config for reporting.
func buildArgs(cfg config) string {
	parts := []string{"mltlint"}
	parts = append(parts, fmt.Sprintf("--originals=%s", cfg.Originals))
	parts = append(parts, fmt.Sprintf("--unsorted=%s", cfg.Unsorted))
	parts = append(parts, fmt.Sprintf("--dups=%s", cfg.Dups))
	if cfg.Output != "." {
		parts = append(parts, fmt.Sprintf("--output=%s", cfg.Output))
	}
	if cfg.Workers != runtime.NumCPU() {
		parts = append(parts, fmt.Sprintf("--workers=%d", cfg.Workers))
	}
	if cfg.Debug {
		parts = append(parts, "--debug")
	}
	return strings.Join(parts, " ")
}
