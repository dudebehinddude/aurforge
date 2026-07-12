package internal

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/dudebehinddude/aurforge/ent"
	"github.com/dudebehinddude/aurforge/ent/dependency"
	"github.com/dudebehinddude/aurforge/ent/job"
	"github.com/dudebehinddude/aurforge/ent/managedpackage"
	"github.com/dudebehinddude/aurforge/ent/packageversion"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func OpenDB(ctx context.Context, cfg Config) (*ent.Client, error) {
	database, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := database.PingContext(ctx); err != nil {
		database.Close()
		return nil, err
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, database)))
	if err := client.Schema.Create(ctx); err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}

type Job struct {
	ID             int
	PackageName    string
	SourcePath     string
	PackageVersion int
}

func ClaimJob(ctx context.Context, client *ent.Client) (*Job, error) {
	for {
		tx, err := client.Tx(ctx)
		if err != nil {
			return nil, err
		}
		claimed, err := tx.Job.Query().
			Where(job.StatusEQ(job.StatusPending), job.EligibleAtLTE(time.Now())).
			Order(ent.Asc(job.FieldEligibleAt), ent.Asc(job.FieldID)).
			First(ctx)
		if ent.IsNotFound(err) {
			_ = tx.Rollback()
			return nil, nil
		}
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		version, err := tx.PackageVersion.Get(ctx, int(claimed.PackageVersionID))
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		latest, err := tx.PackageVersion.Query().
			Where(packageversion.PackageIDEQ(version.PackageID)).
			Order(ent.Desc(packageversion.FieldFirstSeenAt), ent.Desc(packageversion.FieldID)).
			First(ctx)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if latest.ID != version.ID {
			if _, err := tx.Job.UpdateOne(claimed).
				SetStatus(job.StatusSkipped).
				SetFinishedAt(time.Now()).
				SetError("superseded by newer revision").
				Save(ctx); err != nil {
				_ = tx.Rollback()
				return nil, err
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			continue
		}
		if _, err := tx.Job.UpdateOne(claimed).
			SetStatus(job.StatusRunning).
			SetClaimedAt(time.Now()).
			SetAttempts(claimed.Attempts + 1).
			Save(ctx); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		pkg, err := tx.ManagedPackage.Get(ctx, int(version.PackageID))
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &Job{ID: claimed.ID, PackageName: pkg.Name, SourcePath: pkg.SourcePath, PackageVersion: version.ID}, nil
	}
}

func MarkJob(ctx context.Context, client *ent.Client, id int, status job.Status, logPath, failure string) error {
	update := client.Job.UpdateOneID(id).SetStatus(status).SetFinishedAt(time.Now())
	if logPath != "" {
		update.SetLogPath(logPath)
	}
	if failure != "" {
		update.SetError(failure)
	}
	return update.Exec(ctx)
}

// EligibleAtFromCommit returns when a revision may be built: commit time plus the
// configured delay. Already-old commits become eligible immediately.
func EligibleAtFromCommit(commitTime time.Time, delay time.Duration, now time.Time) time.Time {
	eligible := commitTime.Add(delay)
	if eligible.After(now) {
		return eligible
	}
	return now
}

func CreatePackage(ctx context.Context, client *ent.Client, pkg PackageInfo, sourcePath, revision, digest string, eligibleAt time.Time) (int, error) {
	tx, err := client.Tx(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	managed, err := tx.ManagedPackage.Query().Where(managedpackage.NameEQ(pkg.Name)).Only(ctx)
	if ent.IsNotFound(err) {
		managed, err = tx.ManagedPackage.Create().
			SetName(pkg.Name).
			SetSourceKind(managedpackage.SourceKind(pkg.SourceKind)).
			SetSourceRef(pkg.SourceRef).
			SetSourcePath(sourcePath).
			SetCreatedAt(time.Now()).
			Save(ctx)
	} else if err == nil {
		managed, err = tx.ManagedPackage.UpdateOne(managed).
			SetSourceKind(managedpackage.SourceKind(pkg.SourceKind)).
			SetSourceRef(pkg.SourceRef).
			SetSourcePath(sourcePath).
			Save(ctx)
	}
	if err != nil {
		return 0, err
	}

	version, err := tx.PackageVersion.Query().Where(
		packageversion.PackageIDEQ(int64(managed.ID)),
		packageversion.RevisionEQ(revision),
	).Only(ctx)
	created := false
	if ent.IsNotFound(err) {
		metadata := map[string]any{
			"name": pkg.Name, "source_kind": pkg.SourceKind, "source_ref": pkg.SourceRef,
			"split_packages": pkg.SplitPackages, "warnings": pkg.Warnings,
		}
		version, err = tx.PackageVersion.Create().
			SetPackageID(int64(managed.ID)).
			SetRevision(revision).
			SetSourceDigest(digest).
			SetPkgver(pkg.Version).
			SetPkgrel(pkg.Release).
			SetMetadata(metadata).
			SetFirstSeenAt(time.Now()).
			Save(ctx)
		created = true
	}
	if err != nil {
		return 0, err
	}
	if created {
		for _, dep := range pkg.Dependencies {
			kind := "repo"
			if dep.AUR {
				kind = "aur"
			}
			if _, err := tx.Dependency.Create().
				SetPackageVersionID(int64(version.ID)).
				SetDependencyName(dep.Name).
				SetDependencyKind(depKind(kind)).
				Save(ctx); err != nil {
				return 0, err
			}
		}
		if err := skipPendingJobsForPackage(ctx, tx, int64(managed.ID), int64(version.ID)); err != nil {
			return 0, err
		}
		if _, err := tx.Job.Create().
			SetPackageVersionID(int64(version.ID)).
			SetEligibleAt(eligibleAt).
			SetCreatedAt(time.Now()).
			Save(ctx); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return version.ID, nil
}

func skipPendingJobsForPackage(ctx context.Context, tx *ent.Tx, packageID, keepVersionID int64) error {
	versions, err := tx.PackageVersion.Query().
		Where(
			packageversion.PackageIDEQ(packageID),
			packageversion.IDNEQ(int(keepVersionID)),
		).
		All(ctx)
	if err != nil {
		return err
	}
	if len(versions) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(versions))
	for _, version := range versions {
		ids = append(ids, int64(version.ID))
	}
	_, err = tx.Job.Update().
		Where(
			job.StatusEQ(job.StatusPending),
			job.PackageVersionIDIn(ids...),
		).
		SetStatus(job.StatusSkipped).
		SetFinishedAt(time.Now()).
		SetError("superseded by newer revision").
		Save(ctx)
	return err
}

func depKind(value string) dependency.DependencyKind {
	if value == "aur" {
		return dependency.DependencyKindAur
	}
	return dependency.DependencyKindRepo
}

func ListPackages(ctx context.Context, client *ent.Client) ([]string, error) {
	packages, err := client.ManagedPackage.Query().Order(ent.Asc(managedpackage.FieldName)).All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(packages))
	for _, pkg := range packages {
		latest, err := client.PackageVersion.Query().Where(packageversion.PackageIDEQ(int64(pkg.ID))).Order(ent.Desc(packageversion.FieldFirstSeenAt)).First(ctx)
		if ent.IsNotFound(err) {
			result = append(result, fmt.Sprintf("%s %s new", pkg.Name, pkg.SourceKind))
			continue
		}
		if err != nil {
			return nil, err
		}
		latestJob, err := client.Job.Query().Where(job.PackageVersionIDEQ(int64(latest.ID))).Order(ent.Desc(job.FieldID)).First(ctx)
		status := "new"
		if err == nil {
			status = string(latestJob.Status)
		} else if !ent.IsNotFound(err) {
			return nil, err
		}
		result = append(result, fmt.Sprintf("%s %s %s", pkg.Name, pkg.SourceKind, status))
	}
	return result, nil
}

func JobArtifacts(ctx context.Context, client *ent.Client, jobID int) error {
	return client.Job.UpdateOneID(jobID).SetStatus(job.StatusPublished).SetFinishedAt(time.Now()).Exec(ctx)
}
