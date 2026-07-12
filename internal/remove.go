package internal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dudebehinddude/aurforge/ent"
	"github.com/dudebehinddude/aurforge/ent/artifact"
	"github.com/dudebehinddude/aurforge/ent/dependency"
	"github.com/dudebehinddude/aurforge/ent/job"
	"github.com/dudebehinddude/aurforge/ent/managedpackage"
	"github.com/dudebehinddude/aurforge/ent/packageversion"
)

// RemovePackageResult summarizes what remove cleaned up.
type RemovePackageResult struct {
	Name         string
	SourceKind   string
	RepoPackages []string
	Artifacts    []string
}

// RemovePackage stops managing name (local or AUR), cancels related jobs,
// removes published packages from the pacman repo, and deletes source snapshots.
func RemovePackage(ctx context.Context, cfg Config, client *ent.Client, name string) (RemovePackageResult, error) {
	pkg, err := client.ManagedPackage.Query().Where(managedpackage.NameEQ(name)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return RemovePackageResult{}, fmt.Errorf("package %q is not managed", name)
		}
		return RemovePackageResult{}, err
	}

	versions, err := client.PackageVersion.Query().
		Where(packageversion.PackageIDEQ(int64(pkg.ID))).
		All(ctx)
	if err != nil {
		return RemovePackageResult{}, err
	}
	versionIDs := make([]int64, 0, len(versions))
	repoNames := map[string]struct{}{pkg.Name: struct{}{}}
	for _, version := range versions {
		versionIDs = append(versionIDs, int64(version.ID))
		for _, split := range splitPackagesFromMetadata(version.Metadata) {
			repoNames[split] = struct{}{}
		}
	}

	var jobs []*ent.Job
	if len(versionIDs) > 0 {
		jobs, err = client.Job.Query().Where(job.PackageVersionIDIn(versionIDs...)).All(ctx)
		if err != nil {
			return RemovePackageResult{}, err
		}
	}
	for _, item := range jobs {
		if item.Status == job.StatusRunning {
			return RemovePackageResult{}, fmt.Errorf("package %q has a running build (job %d); try again when it finishes", name, item.ID)
		}
	}

	jobIDs := make([]int64, 0, len(jobs))
	for _, item := range jobs {
		jobIDs = append(jobIDs, int64(item.ID))
	}

	var artifacts []*ent.Artifact
	if len(jobIDs) > 0 {
		artifacts, err = client.Artifact.Query().Where(artifact.JobIDIn(jobIDs...)).All(ctx)
		if err != nil {
			return RemovePackageResult{}, err
		}
	}
	artifactNames := make([]string, 0, len(artifacts))
	for _, item := range artifacts {
		artifactNames = append(artifactNames, item.Filename)
		if pkgName := packageNameFromArtifact(item.Filename); pkgName != "" {
			repoNames[pkgName] = struct{}{}
		}
	}

	names := sortedKeys(repoNames)
	if err := removeFromRepository(ctx, cfg, names); err != nil {
		return RemovePackageResult{}, err
	}
	for _, filename := range artifactNames {
		_ = os.Remove(filepath.Join(cfg.RepoRoot(), filename))
		_ = os.Remove(filepath.Join(cfg.RepoRoot(), filename+".sig"))
	}

	tx, err := client.Tx(ctx)
	if err != nil {
		return RemovePackageResult{}, err
	}
	defer tx.Rollback()

	if len(jobIDs) > 0 {
		if _, err := tx.Artifact.Delete().Where(artifact.JobIDIn(jobIDs...)).Exec(ctx); err != nil {
			return RemovePackageResult{}, err
		}
		if _, err := tx.Job.Delete().Where(job.IDIn(intsFromInt64(jobIDs)...)).Exec(ctx); err != nil {
			return RemovePackageResult{}, err
		}
	}
	if len(versionIDs) > 0 {
		if _, err := tx.Dependency.Delete().Where(dependency.PackageVersionIDIn(versionIDs...)).Exec(ctx); err != nil {
			return RemovePackageResult{}, err
		}
		if _, err := tx.PackageVersion.Delete().Where(packageversion.IDIn(intsFromInt64(versionIDs)...)).Exec(ctx); err != nil {
			return RemovePackageResult{}, err
		}
	}
	if err := tx.ManagedPackage.DeleteOneID(pkg.ID).Exec(ctx); err != nil {
		return RemovePackageResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return RemovePackageResult{}, err
	}

	for _, item := range jobs {
		_ = os.RemoveAll(filepath.Join(cfg.StagingRoot(), fmt.Sprintf("job-%d", item.ID)))
		_ = os.Remove(logFile(cfg, item.ID))
	}
	_ = os.RemoveAll(filepath.Join(cfg.SourceRoot(), string(pkg.SourceKind), pkg.Name))

	return RemovePackageResult{
		Name:         pkg.Name,
		SourceKind:   string(pkg.SourceKind),
		RepoPackages: names,
		Artifacts:    artifactNames,
	}, nil
}

func removeFromRepository(ctx context.Context, cfg Config, names []string) error {
	if len(names) == 0 {
		return nil
	}
	dbPath := filepath.Join(cfg.RepoRoot(), cfg.RepositoryName+".db.tar.gz")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil
	}
	args := append([]string{dbPath}, names...)
	cmd := exec.CommandContext(ctx, "repo-remove", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// repo-remove fails when a name was never published; still try per-package.
		var first error
		for _, name := range names {
			per := exec.CommandContext(ctx, "repo-remove", dbPath, name)
			per.Stdout = os.Stderr
			per.Stderr = os.Stderr
			if err := per.Run(); err != nil && first == nil {
				first = err
			}
		}
		// Ignore missing entries; files are deleted separately.
		_ = first
	}
	return nil
}

func splitPackagesFromMetadata(metadata map[string]any) []string {
	raw, ok := metadata["split_packages"]
	if !ok {
		return nil
	}
	switch value := raw.(type) {
	case []string:
		return value
	case []any:
		names := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok && text != "" {
				names = append(names, text)
			}
		}
		return names
	default:
		return nil
	}
}

func packageNameFromArtifact(filename string) string {
	base := filepath.Base(filename)
	for _, suffix := range []string{".pkg.tar.zst", ".pkg.tar.xz", ".pkg.tar.gz", ".pkg.tar.bz2", ".pkg.tar"} {
		if strings.HasSuffix(base, suffix) {
			base = strings.TrimSuffix(base, suffix)
			break
		}
	}
	// name-pkgver-pkgrel-arch
	parts := strings.Split(base, "-")
	if len(parts) < 4 {
		return ""
	}
	return strings.Join(parts[:len(parts)-3], "-")
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func intsFromInt64(values []int64) []int {
	out := make([]int, len(values))
	for i, value := range values {
		out[i] = int(value)
	}
	return out
}
