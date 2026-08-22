//go:build windows

package fontres

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// fontDirs lists the directories Windows keeps fonts in.
func fontDirs() []string {
	var dirs []string
	if windir := os.Getenv("WINDIR"); windir != "" {
		dirs = append(dirs, filepath.Join(windir, "Fonts"))
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		dirs = append(dirs, filepath.Join(local, "Microsoft", "Windows", "Fonts"))
	}
	return dirs
}

// platformFontFiles reads the registry, which is what names a font
// installed by reference rather than by copy -- a file a directory scan
// never reaches.
func platformFontFiles() []string {
	var out []string
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		key, err := registry.OpenKey(root,
			`SOFTWARE\Microsoft\Windows NT\CurrentVersion\Fonts`, registry.READ)
		if err != nil {
			continue
		}
		names, err := key.ReadValueNames(0)
		if err != nil {
			key.Close() // nolint:errcheck
			continue
		}
		for _, name := range names {
			value, _, err := key.GetStringValue(name)
			if err != nil || value == "" {
				continue
			}
			if !filepath.IsAbs(value) {
				if windir := os.Getenv("WINDIR"); windir != "" {
					value = filepath.Join(windir, "Fonts", value)
				}
			}
			out = append(out, value)
		}
		key.Close() // nolint:errcheck
	}
	return out
}

// genericFamilyFiles has no Windows equivalent: the last resort names files.
func genericFamilyFiles(string) []string { return nil }
