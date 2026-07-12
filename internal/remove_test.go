package internal

import "testing"

func TestPackageNameFromArtifact(t *testing.T) {
	cases := map[string]string{
		"t3-code-bin-0.0.28-1-x86_64.pkg.tar.zst": "t3-code-bin",
		"foo-1.2.3-1-any.pkg.tar.xz":              "foo",
		"bad.pkg.tar.zst":                         "",
	}
	for input, want := range cases {
		if got := packageNameFromArtifact(input); got != want {
			t.Fatalf("%s: got %q, want %q", input, got, want)
		}
	}
}

func TestSplitPackagesFromMetadata(t *testing.T) {
	got := splitPackagesFromMetadata(map[string]any{
		"split_packages": []any{"one", "two"},
	})
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("unexpected split packages: %#v", got)
	}
}
