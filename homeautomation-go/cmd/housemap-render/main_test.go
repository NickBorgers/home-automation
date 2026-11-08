package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	exampleMap = "../../internal/housemap/testdata/example_house_map.yaml"
	exampleRP  = "../../internal/plugins/roompresence/testdata/example_config.yaml"
)

func TestRun_SVG(t *testing.T) {
	out := filepath.Join(t.TempDir(), "map.svg")
	require.NoError(t, run([]string{"-in", exampleMap, "-out", out}, io.Discard))
	data, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(data), "<svg "))
}

func TestRun_HTML(t *testing.T) {
	out := filepath.Join(t.TempDir(), "map.html")
	require.NoError(t, run([]string{"-format", "html", "-in", exampleMap, "-room-presence", exampleRP, "-out", out},
		io.Discard))
	data, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(data), "<title>"))
	assert.Contains(t, string(data), "<code>study</code> <em>unseen</em>")
}

func TestRun_Errors(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "x")
	tests := [][]string{
		{"-bogus"},
		{"-in", exampleMap},
		{"-format", "png", "-in", exampleMap, "-out", out},
		{"-in", filepath.Join(dir, "missing.yaml"), "-out", out},
		{"-format", "html", "-in", exampleMap, "-room-presence", filepath.Join(dir, "missing.yaml"), "-out", out},
		{"-format", "html", "-in", exampleMap, "-room-presence", exampleMap, "-out", out},
		{"-in", exampleMap, "-out", filepath.Join(dir, "no", "such", "dir.svg")},
	}
	for _, args := range tests {
		assert.Error(t, run(args, io.Discard), "%v", args)
	}
}
