package tools

import (
	"os"
	"path/filepath"
	"strings"
)

// resolveToCwd resolves a path relative to the given cwd.
// Handles ~ expansion and absolute paths.
func resolveToCwd(filePath, cwd string) string {
	expanded := expandPath(filePath)
	if filepath.IsAbs(expanded) {
		return expanded
	}
	return filepath.Join(cwd, expanded)
}

func expandPath(filePath string) string {
	normalized := strings.TrimPrefix(filePath, "@")
	if normalized == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if strings.HasPrefix(normalized, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, normalized[2:])
	}
	return normalized
}
