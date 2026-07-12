package internal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSRCINFO(t *testing.T) {
	directory := t.TempDir()
	contents := "pkgbase = demo\n\tpkgver = 1.2.3\n\tpkgrel = 4\n\tpkgname = demo\n\tpkgname = demo-cli\n\tdepends = glibc\n\tmakedepends = helper>=2\n"
	if err := os.WriteFile(filepath.Join(directory, ".SRCINFO"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "PKGBUILD"), []byte("pkgname=demo"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg, err := ParsePackage(directory, "local", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "demo" || pkg.Version != "1.2.3" || pkg.Release != "4" {
		t.Fatalf("unexpected package: %#v", pkg)
	}
	if len(pkg.SplitPackages) != 2 || len(pkg.Dependencies) != 2 {
		t.Fatalf("unexpected package metadata: %#v", pkg)
	}
}

func TestHashTreeChangesWithContent(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "PKGBUILD")
	if err := os.WriteFile(path, []byte("pkgname=one"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := HashTree(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("pkgname=two"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := HashTree(directory)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("tree digest did not change")
	}
}

func TestAuditPackage(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "PKGBUILD"), []byte("sha256sums=('SKIP')\nsudo true"), 0o644); err != nil {
		t.Fatal(err)
	}
	warnings, err := AuditPackage(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %v", warnings)
	}
}
