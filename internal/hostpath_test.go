package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostPathFromBindMounts(t *testing.T) {
	mounts := []bindMount{{
		Destination: "/var/lib/aurforge",
		Source:      "/juspool1/services/aurforge/data",
	}}
	path, err := hostPathFromBindMounts("/var/lib/aurforge", mounts)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/juspool1/services/aurforge/data" {
		t.Fatalf("got %q", path)
	}
	nested, err := hostPathFromBindMounts("/var/lib/aurforge/sources/local/pkg", mounts)
	if err != nil {
		t.Fatal(err)
	}
	if nested != "/juspool1/services/aurforge/data/sources/local/pkg" {
		t.Fatalf("got %q", nested)
	}
}

func TestHostPathFromBindMountsLongestPrefix(t *testing.T) {
	mounts := []bindMount{
		{Destination: "/var/lib", Source: "/wrong"},
		{Destination: "/var/lib/aurforge", Source: "/juspool1/services/aurforge/data"},
	}
	path, err := hostPathFromBindMounts("/var/lib/aurforge/cache", mounts)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/juspool1/services/aurforge/data/cache" {
		t.Fatalf("got %q", path)
	}
}

func TestContainerIDFromCgroup(t *testing.T) {
	const cgroup = `0::/system.slice/docker-6f1b2c3d4e5f67890123456789abcdef0123456789abcdef0123456789abcd.scope
`
	directory := t.TempDir()
	path := filepath.Join(directory, "cgroup")
	if err := os.WriteFile(path, []byte(cgroup), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := containerIDFromCgroup(path)
	if err != nil {
		t.Fatal(err)
	}
	if id != "6f1b2c3d4e5f67890123456789abcdef0123456789abcdef0123456789abcd" {
		t.Fatalf("got %q", id)
	}
}

func TestResolveHostDataRootPrefersDockerMount(t *testing.T) {
	original := lookupHostPath
	t.Cleanup(func() { lookupHostPath = original })
	lookupHostPath = func(string) (string, error) {
		return "/juspool1/services/aurforge/data", nil
	}
	path, err := resolveHostDataRoot("/var/lib/aurforge", "./data")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/juspool1/services/aurforge/data" {
		t.Fatalf("got %q", path)
	}
}

func TestResolveHostDataRootRejectsRelativeFallback(t *testing.T) {
	original := lookupHostPath
	t.Cleanup(func() { lookupHostPath = original })
	lookupHostPath = func(string) (string, error) {
		return "", filepath.ErrBadPattern
	}
	_, err := resolveHostDataRoot("/var/lib/aurforge", "./data")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "docker") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveHostDataRootFallsBackToAbsolute(t *testing.T) {
	original := lookupHostPath
	t.Cleanup(func() { lookupHostPath = original })
	lookupHostPath = func(string) (string, error) {
		return "", filepath.ErrBadPattern
	}
	path, err := resolveHostDataRoot("/var/lib/aurforge", "/srv/aurforge")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/srv/aurforge" {
		t.Fatalf("got %q", path)
	}
}
