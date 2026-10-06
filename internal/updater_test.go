package internal

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func updaterFixture(t *testing.T) string {
	t.Helper()
	// A root-owned testing.T parent is mode 0700, so create the fixture directly
	// beneath /tmp where the child process can traverse its parents.
	directory, err := os.MkdirTemp("", "aurforge-updater-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	if os.Geteuid() == 0 {
		if err := os.Chown(directory, 65534, 65534); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

func TestLocalUpdaterIdentityAndRootFileRecovery(t *testing.T) {
	directory := updaterFixture(t)
	path := filepath.Join(directory, "PKGBUILD")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "set -eu\n[ \"$(id -u)\" != 0 ]\n[ -w \"$HOME\" ]\ncat PKGBUILD\nprintf updated > PKGBUILD\n"
	if err := os.WriteFile(filepath.Join(directory, "update-pkgbuild.sh"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := runLocalUpdater(context.Background(), directory)
	if err != nil {
		t.Fatalf("updater: %v: %s", err, result)
	}
	if string(result) != "old" {
		t.Fatalf("updater could not read original file: %q", result)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "updated" {
		t.Fatalf("update not saved: %q, %v", data, err)
	}
	if os.Geteuid() == 0 {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Sys().(*syscall.Stat_t).Uid != 65534 {
			t.Fatal("root-owned file was not repaired")
		}
	}
}

func TestLocalUpdaterReportsFailure(t *testing.T) {
	directory := updaterFixture(t)
	if err := os.WriteFile(filepath.Join(directory, "update-pkgbuild.sh"), []byte("echo updater-failed >&2\nexit 10\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := runLocalUpdater(context.Background(), directory)
	if err == nil || !strings.Contains(string(result), "updater-failed") {
		t.Fatalf("failure not returned: %v: %s", err, result)
	}
}

func TestLocalUpdaterRootOwnedImport(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires a root scheduler")
	}
	if _, err := user.Lookup("updater"); err != nil {
		t.Skip("requires the runtime updater account")
	}
	directory := updaterFixture(t)
	if err := os.Chown(directory, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "update-pkgbuild.sh"), []byte("set -eu\n[ \"$(id -u)\" != 0 ]\nprintf updated > result\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := runLocalUpdater(context.Background(), directory)
	if err != nil {
		t.Fatalf("root-owned import: %v: %s", err, result)
	}
	if data, err := os.ReadFile(filepath.Join(directory, "result")); err != nil || string(data) != "updated" {
		t.Fatalf("root-owned import not updated: %q, %v", data, err)
	}
}

func TestLocalUpdaterMakepkg(t *testing.T) {
	if _, err := exec.LookPath("makepkg"); err != nil {
		t.Skip("makepkg integration requires the Arch runtime image")
	}
	directory := updaterFixture(t)
	files := map[string]string{
		"PKGBUILD":           "pkgname=updater-test\npkgver=1.2.3\npkgrel=1\npkgdesc='Updater test'\narch=('any')\nlicense=('MIT')\nsource=('input.txt')\nsha256sums=('SKIP')\npackage() { :; }\n",
		"input.txt":          "test source\n",
		"update-pkgbuild.sh": "set -eu\nmakepkg -g > checksums\nmakepkg --printsrcinfo > .SRCINFO\n",
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := runLocalUpdater(context.Background(), directory)
	if err != nil {
		t.Fatalf("makepkg updater: %v: %s", err, result)
	}
	info, err := ParsePackage(directory, "local", directory)
	if err != nil || info.Version != "1.2.3" {
		t.Fatalf("invalid updated metadata: %#v, %v", info, err)
	}
	checksums, err := os.ReadFile(filepath.Join(directory, "checksums"))
	if err != nil || !strings.Contains(string(checksums), "sha256sums=(") || strings.Contains(string(checksums), "SKIP") {
		t.Fatalf("checksums not generated: %q, %v", checksums, err)
	}
}
