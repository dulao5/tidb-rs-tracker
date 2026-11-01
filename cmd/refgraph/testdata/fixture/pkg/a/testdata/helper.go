package testdata

import "example.com/fx/pkg/a"

// Helper is a package in a testdata dir, which "./pkg/..." skips.
func Helper() int { return a.Max }
