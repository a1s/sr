package fontres

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// enumerateFontFiles lists every candidate font file on the machine.
//
// The sources are merged into one table rather than tried in turn,
// and files within a directory are visited in Unicode order by name
// so that a tie between two faces claiming one key resolves the same way
// on every run.
func enumerateFontFiles(override []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(path string) {
		clean := filepath.Clean(path)
		if seen[clean] {
			return
		}
		seen[clean] = true
		out = append(out, clean)
	}
	dirs := override
	if len(dirs) == 0 {
		for _, path := range platformFontFiles() {
			add(path)
		}
		dirs = fontDirs()
	}
	for _, dir := range dirs {
		for _, path := range scanDir(dir) {
			add(path)
		}
	}
	return out
}

// scanDir lists the regular files in a directory tree, in Unicode order
// by name. A directory that does not exist is not an error.
func scanDir(dir string) []string {
	var out []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		return nil
	}
	sort.Strings(out)
	return out
}
