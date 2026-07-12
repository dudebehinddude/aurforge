package internal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestEligibleAtFromCommit(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	delay := 12 * time.Hour

	t.Run("recent commit waits out remainder", func(t *testing.T) {
		commit := now.Add(-3 * time.Hour)
		got := EligibleAtFromCommit(commit, delay, now)
		want := commit.Add(delay)
		if !got.Equal(want) {
			t.Fatalf("got %s, want %s", got, want)
		}
	})

	t.Run("old commit is eligible immediately", func(t *testing.T) {
		commit := now.Add(-24 * time.Hour)
		got := EligibleAtFromCommit(commit, delay, now)
		if !got.Equal(now) {
			t.Fatalf("got %s, want %s", got, now)
		}
	})

	t.Run("zero delay is eligible immediately", func(t *testing.T) {
		commit := now
		got := EligibleAtFromCommit(commit, 0, now)
		if !got.Equal(now) {
			t.Fatalf("got %s, want %s", got, now)
		}
	})
}

func TestGitCommitTime(t *testing.T) {
	directory := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = directory
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=aurforge",
			"GIT_AUTHOR_EMAIL=aurforge@example.com",
			"GIT_COMMITTER_NAME=aurforge",
			"GIT_COMMITTER_EMAIL=aurforge@example.com",
			"GIT_AUTHOR_DATE=2026-01-02T03:04:05Z",
			"GIT_COMMITTER_DATE=2026-01-02T03:04:05Z",
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_SYSTEM=/dev/null",
		)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	runGit("init")
	runGit("config", "commit.gpgsign", "false")
	runGit("config", "user.name", "aurforge")
	runGit("config", "user.email", "aurforge@example.com")
	if err := os.WriteFile(filepath.Join(directory, "PKGBUILD"), []byte("pkgname=demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "PKGBUILD")
	runGit("commit", "-m", "initial")

	got, err := GitCommitTime(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %s, want %s", got, want)
	}
}
