package main

import (
	"strings"
	"testing"
)

func TestUsageIncludesFlagsAndDefaults(t *testing.T) {
	got := usage("test-version")
	for _, want := range []string{
		"singularity-mcp test-version",
		"Usage:",
		"-token string",
		"SINGULARITY_TOKEN",
		"-base-url string",
		"https://api.singularity-app.com",
		"-timeout duration",
		"30s",
		"-require-write-approval",
		"-version",
		"-help, -h",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("usage() missing %q in:\n%s", want, got)
		}
	}
}
