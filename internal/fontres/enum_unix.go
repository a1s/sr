//go:build !windows && !darwin

package fontres

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// fontDirs lists fontconfig's configured directories, falling back
// to the conventional ones. Distributions agree on neither the filenames
// nor the directories, which is why asking fontconfig comes first.
func fontDirs() []string {
	var dirs []string
	if out, err := exec.Command("fc-list", "--format", "%{file}\n").Output(); err == nil {
		seen := map[string]bool{}
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			dir := filepath.Dir(line)
			if !seen[dir] {
				seen[dir] = true
				dirs = append(dirs, dir)
			}
		}
		sort.Strings(dirs)
	}
	dirs = append(dirs,
		"/usr/share/fonts",
		"/usr/local/share/fonts",
	)
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs,
			filepath.Join(home, ".fonts"),
			filepath.Join(home, ".local", "share", "fonts"),
		)
	}
	return dirs
}

// platformFontFiles adds nothing beyond the directories fontconfig names.
func platformFontFiles() []string { return nil }

// genericFamilyFiles asks the platform for a generic family,
// which is the one place delegating to fontconfig is right:
// the last resort wants a guess, and fc-match always answers.
//
// It must never be used for matching a named family. On a host with only
// Adwaita installed, fc-match returns Adwaita Mono for Helvetica, for
// NoSuchFaceXYZ, and for everything else -- so a chain that let it perform
// the match would resolve every typeface there and record a found face for
// what is really a fallback.
func genericFamilyFiles(generic string) []string {
	out, err := exec.Command("fc-match", "--format", "%{file}", generic).Output()
	if err != nil {
		return nil
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return nil
	}
	return []string{path}
}
