//go:build darwin

package fontres

import (
	"os"
	"path/filepath"
)

// fontDirs lists the standard macOS font directories.
func fontDirs() []string {
	dirs := []string{
		"/System/Library/Fonts",
		"/System/Library/Fonts/Supplemental",
		"/Library/Fonts",
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "Library", "Fonts"))
	}
	return dirs
}

// platformFontFiles adds nothing: the directories are the enumeration.
func platformFontFiles() []string { return nil }

// genericFamilyFiles has no macOS equivalent: the last resort names files.
func genericFamilyFiles(string) []string { return nil }
