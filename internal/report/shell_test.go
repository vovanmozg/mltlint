package report_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vp/mlt3/internal/comparator"
	"github.com/vp/mlt3/internal/report"
	"github.com/vp/mlt3/internal/scanner"
)

func TestOutSH(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.WriteShell(&buf, sampleConfig(), []*report.Group{sampleGroup()}))
	require.True(t, strings.HasPrefix(buf.String(), "#!/usr/bin/env bash"), "should start with shebang")
}

func TestOutSH_FUNC(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.WriteShell(&buf, sampleConfig(), []*report.Group{sampleGroup()}))
	require.Contains(t, buf.String(), "move_cmd()")
}

func TestOutSH_ORIGCMD(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.WriteShell(&buf, sampleConfig(), []*report.Group{sampleGroup()}))
	require.Contains(t, buf.String(), "original_cmd '/orig/2023/photo.jpg' # original")
}

func TestOutSH_GROUP(t *testing.T) {
	g2 := &report.Group{
		Original: &scanner.FileInfo{Path: "/orig/beach.jpg", RelativePath: "beach.jpg", Size: 9000, Mtime: 1700000000, MD5: "bbb"},
		Duplicates: []*comparator.Result{{
			Duplicate: &scanner.FileInfo{Path: "/unso/beach_copy.jpg", RelativePath: "beach_copy.jpg", Size: 9000, Mtime: 1690000000, MD5: "bbb"},
			Original:  &scanner.FileInfo{Path: "/orig/beach.jpg"},
			Level:     "similar", Distance: 0, Score: 1.0,
		}},
	}

	var buf bytes.Buffer
	require.NoError(t, report.WriteShell(&buf, sampleConfig(), []*report.Group{sampleGroup(), g2}))
	out := buf.String()
	require.Contains(t, out, "original_cmd '/orig/2023/photo.jpg' # original")
	require.Contains(t, out, "original_cmd '/orig/beach.jpg' # original")
}

func TestOutSH_CHK(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.WriteShell(&buf, sampleConfig(), []*report.Group{sampleGroup()}))
	require.Contains(t, buf.String(), `if [ ! -e "$original" ]`)
}

func TestOutSH_CMD(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.WriteShell(&buf, sampleConfig(), []*report.Group{sampleGroup()}))
	require.Contains(t, buf.String(), "move_cmd '/unso/2023/photo.jpg' '/orig/2023/photo.jpg' '/dups/similar/2023/photo.jpg'")
}

func TestOutSH_CMT(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.WriteShell(&buf, sampleConfig(), []*report.Group{sampleGroup()}))
	require.Contains(t, buf.String(), "# similar (dist=0)")
}

func TestOutSH_HOST(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.WriteShell(&buf, sampleConfig(), []*report.Group{sampleGroup()}))
	require.False(t, strings.Contains(buf.String(), "/vt/"), "output must not contain /vt/")
}
