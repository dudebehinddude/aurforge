package internal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Dependency struct {
	Name string `json:"name"`
	AUR  bool   `json:"aur"`
}

type PackageInfo struct {
	Name          string       `json:"name"`
	Version       string       `json:"version"`
	Release       string       `json:"release"`
	SourceKind    string       `json:"source_kind"`
	SourceRef     string       `json:"source_ref"`
	SplitPackages []string     `json:"split_packages"`
	Dependencies  []Dependency `json:"dependencies"`
	Warnings      []string     `json:"warnings"`
}

func (p PackageInfo) JSON() []byte {
	value, _ := json.Marshal(p)
	return value
}

func ParsePackage(path, kind, ref string) (PackageInfo, error) {
	if _, err := os.Stat(filepath.Join(path, "PKGBUILD")); err != nil {
		return PackageInfo{}, fmt.Errorf("PKGBUILD: %w", err)
	}
	info := PackageInfo{SourceKind: kind, SourceRef: ref}
	if srcinfo, err := os.ReadFile(filepath.Join(path, ".SRCINFO")); err == nil {
		parseSRCINFO(string(srcinfo), &info)
	} else {
		pkgbuild, readErr := os.ReadFile(filepath.Join(path, "PKGBUILD"))
		if readErr != nil {
			return PackageInfo{}, readErr
		}
		parsePKGBUILD(string(pkgbuild), &info)
		info.Warnings = append(info.Warnings, ".SRCINFO not found; preview used conservative static PKGBUILD parsing")
	}
	if info.Name == "" {
		return PackageInfo{}, fmt.Errorf("could not determine package name")
	}
	if info.Version == "" {
		info.Version = "unknown"
	}
	if info.Release == "" {
		info.Release = "0"
	}
	if len(info.SplitPackages) == 0 {
		info.SplitPackages = []string{info.Name}
	}
	return info, nil
}

func parseSRCINFO(source string, info *PackageInfo) {
	seen := map[string]bool{}
	for _, raw := range strings.Split(source, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(raw), " = ")
		if !ok {
			continue
		}
		switch key {
		case "pkgbase":
			if info.Name == "" {
				info.Name = value
			}
		case "pkgname":
			info.SplitPackages = append(info.SplitPackages, value)
		case "pkgver":
			info.Version = value
		case "pkgrel":
			info.Release = value
		case "depends", "makedepends":
			name := depName(value)
			if name != "" && !seen[name] {
				info.Dependencies = append(info.Dependencies, Dependency{Name: name})
				seen[name] = true
			}
		}
	}
}

var assignRE = regexp.MustCompile(`(?m)^\s*(pkgname|pkgver|pkgrel|depends|makedepends)\s*=\s*(.+)$`)
var quotedRE = regexp.MustCompile(`[A-Za-z0-9@._+:-]+`)

func parsePKGBUILD(source string, info *PackageInfo) {
	seen := map[string]bool{}
	for _, match := range assignRE.FindAllStringSubmatch(source, -1) {
		key, value := match[1], match[2]
		values := quotedRE.FindAllString(value, -1)
		if len(values) == 0 {
			continue
		}
		switch key {
		case "pkgname":
			info.Name = values[0]
			info.SplitPackages = append(info.SplitPackages, values...)
		case "pkgver":
			info.Version = values[0]
		case "pkgrel":
			info.Release = values[0]
		case "depends", "makedepends":
			for _, value := range values {
				name := depName(value)
				if name != "" && !seen[name] {
					info.Dependencies = append(info.Dependencies, Dependency{Name: name})
					seen[name] = true
				}
			}
		}
	}
}

func depName(value string) string {
	for _, marker := range []string{">=", "<=", "=", ">", "<", ":"} {
		if before, _, ok := strings.Cut(value, marker); ok {
			value = before
			break
		}
	}
	return strings.TrimSpace(value)
}

func HashTree(root string) (string, error) {
	hash := sha256.New()
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		if isBuildArtifact(entry.Name()) {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	for _, path := range paths {
		rel, _ := filepath.Rel(root, path)
		io.WriteString(hash, rel+"\x00")
		file, err := os.Open(path)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(hash, file); err != nil {
			file.Close()
			return "", err
		}
		file.Close()
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func isBuildArtifact(name string) bool {
	return strings.Contains(name, ".pkg.tar.")
}

func CopyTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(source, path)
		if rel == ".git" || strings.HasPrefix(rel, ".git"+string(os.PathSeparator)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() && isBuildArtifact(entry.Name()) {
			return nil
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink is not allowed in imported package: %s", rel)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		stat, err := input.Stat()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, stat.Mode())
		if err != nil {
			return err
		}
		defer output.Close()
		_, err = io.Copy(output, input)
		return err
	})
}

func AuditPackage(path string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(path, "PKGBUILD"))
	if err != nil {
		return nil, err
	}
	content := string(data)
	var warnings []string
	for _, rule := range []struct{ needle, message string }{
		{"SKIP", "checksum verification is skipped"},
		{"sudo", "PKGBUILD invokes sudo"},
		{"/var/run/docker.sock", "PKGBUILD references the Docker socket"},
		{"--privileged", "PKGBUILD references privileged containers"},
	} {
		if strings.Contains(content, rule.needle) {
			warnings = append(warnings, rule.message)
		}
	}
	return warnings, nil
}
