package internal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dudebehinddude/aurforge/ent"
	"github.com/dudebehinddude/aurforge/ent/artifact"
	entjob "github.com/dudebehinddude/aurforge/ent/job"
	"github.com/dudebehinddude/aurforge/ent/managedpackage"
	"github.com/dudebehinddude/aurforge/ent/packageversion"
)

func RunController(ctx context.Context, db *ent.Client) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, request *http.Request) {
		if _, err := db.ManagedPackage.Query().Limit(1).All(request.Context()); err != nil {
			http.Error(writer, err.Error(), http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/v1/packages", func(writer http.ResponseWriter, request *http.Request) {
		packages, err := ListPackages(request.Context(), db)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, item := range packages {
			fmt.Fprintln(writer, item)
		}
	})
	server := &http.Server{Addr: envOr("AURFORGE_HTTP_ADDR", "0.0.0.0:8080"), Handler: mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	return server.ListenAndServe()
}

func RunScheduler(ctx context.Context, cfg Config, db *ent.Client, logger *slog.Logger) error {
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	for {
		if err := refreshAUR(ctx, cfg, db, logger); err != nil {
			logger.Error("scheduler refresh failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func refreshAUR(ctx context.Context, cfg Config, db *ent.Client, logger *slog.Logger) error {
	packages, err := db.ManagedPackage.Query().Where(managedpackage.SourceKindEQ(managedpackage.SourceKindAur), managedpackage.EnabledEQ(true)).All(ctx)
	if err != nil {
		return err
	}
	for _, tracked := range packages {
		name, ref := tracked.Name, tracked.SourceRef
		remote, err := output(ctx, "git", "ls-remote", "https://aur.archlinux.org/"+ref+".git", "HEAD")
		if err != nil {
			logger.Warn("AUR poll failed", "package", name, "error", err)
			continue
		}
		fields := strings.Fields(remote)
		if len(fields) == 0 {
			continue
		}
		exists, err := db.PackageVersion.Query().Where(packageversion.PackageIDEQ(int64(tracked.ID)), packageversion.RevisionEQ(fields[0])).Exist(ctx)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		preview, revision, err := PreviewAUR(ctx, cfg, ref)
		if err != nil {
			logger.Warn("AUR preview failed", "package", name, "error", err)
			continue
		}
		if _, err := AcceptAUR(ctx, cfg, db, preview, revision); err != nil {
			return err
		}
		logger.Info("queued AUR update", "package", name, "revision", revision)
	}
	return nil
}

func RunWorker(ctx context.Context, cfg Config, db *ent.Client, logger *slog.Logger) error {
	hostRoot, err := resolveHostDataRoot(cfg.DataRoot, cfg.HostDataRoot)
	if err != nil {
		return err
	}
	cfg.HostDataRoot = hostRoot
	logger.Info("using host data root", "path", hostRoot)

	for {
		job, err := ClaimJob(ctx, db)
		if err != nil {
			return err
		}
		if job == nil {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(10 * time.Second):
				continue
			}
		}
		if err := buildJob(ctx, cfg, *job); err != nil {
			logger.Error("build failed", "job", job.ID, "error", err)
			_ = MarkJob(ctx, db, job.ID, entjob.StatusFailed, logFile(cfg, job.ID), err.Error())
			NotifyFailure(ctx, cfg, job.PackageName, err)
			continue
		}
		if err := MarkJob(ctx, db, job.ID, entjob.StatusBuilt, logFile(cfg, job.ID), ""); err != nil {
			return err
		}
	}
}

func buildJob(parent context.Context, cfg Config, job Job) error {
	ctx, cancel := context.WithTimeout(parent, cfg.BuildTimeout)
	defer cancel()
	relativeSource, err := filepath.Rel(cfg.DataRoot, job.SourcePath)
	if err != nil || strings.HasPrefix(relativeSource, "..") {
		return fmt.Errorf("invalid source path %q", job.SourcePath)
	}
	hostSource := filepath.Join(cfg.HostDataRoot, relativeSource)
	hostOutput := filepath.Join(cfg.HostDataRoot, "staging", fmt.Sprintf("job-%d", job.ID))
	hostCache := filepath.Join(cfg.HostDataRoot, "cache", "pacman")
	outputDir := filepath.Join(cfg.StagingRoot(), fmt.Sprintf("job-%d", job.ID))
	cacheDir := filepath.Join(cfg.CacheRoot(), "pacman")
	buildCPUs, err := resolveCPUCap(ctx, cfg.BuildCPUs)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		return err
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	if err := os.Chown(outputDir, 1000, 1000); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "bridge", "--read-only",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", cfg.BuildPIDs,
		"--cpus", buildCPUs, "--cpu-shares", cfg.BuildCPUShares, "--memory", cfg.BuildMemory,
		"--tmpfs", "/tmp:rw,exec,nosuid,size=2g", "--tmpfs", "/build:rw,exec,nosuid,mode=1777,size=12g",
		"--mount", "type=bind,src="+hostSource+",dst=/input,readonly",
		"--mount", "type=bind,src="+hostOutput+",dst=/output",
		"--mount", "type=bind,src="+hostCache+",dst=/var/cache/pacman/pkg",
		cfg.BuilderImage, "bash", "-lc", "cp -a /input/. /build/ && cd /build && makepkg --syncdeps --noconfirm --cleanbuild")
	logPath := logFile(cfg, job.ID)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o750); err != nil {
		return err
	}
	log, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer log.Close()
	command.Stdout, command.Stderr = log, log
	return command.Run()
}

func resolveCPUCap(ctx context.Context, value string) (string, error) {
	if !strings.HasSuffix(value, "%") {
		return value, nil
	}
	percent, err := strconv.ParseFloat(strings.TrimSuffix(value, "%"), 64)
	if err != nil || percent <= 0 || percent > 100 {
		return "", fmt.Errorf("invalid CPU percentage %q", value)
	}
	countText, err := output(ctx, "docker", "info", "--format", "{{.NCPU}}")
	if err != nil {
		return "", fmt.Errorf("read Docker CPU count: %w", err)
	}
	count, err := strconv.ParseFloat(strings.TrimSpace(countText), 64)
	if err != nil || count <= 0 {
		return "", fmt.Errorf("invalid Docker CPU count %q", countText)
	}
	return strconv.FormatFloat(count*percent/100, 'f', 2, 64), nil
}

func RunPublisher(ctx context.Context, cfg Config, db *ent.Client, logger *slog.Logger) error {
	for {
		jobs, err := db.Job.Query().Where(entjob.StatusEQ(entjob.StatusBuilt)).Order(ent.Asc(entjob.FieldID)).All(ctx)
		if err != nil {
			return err
		}
		for _, built := range jobs {
			id := built.ID
			if err := publishJob(ctx, cfg, db, id); err != nil {
				logger.Error("publish failed", "job", id, "error", err)
				_ = MarkJob(ctx, db, id, entjob.StatusFailed, "", err.Error())
				NotifyFailure(ctx, cfg, fmt.Sprintf("job %d", id), err)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(10 * time.Second):
		}
	}
}

func publishJob(ctx context.Context, cfg Config, db *ent.Client, jobID int) error {
	source := filepath.Join(cfg.StagingRoot(), fmt.Sprintf("job-%d", jobID))
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.RepoRoot(), 0o755); err != nil {
		return err
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.Contains(entry.Name(), ".pkg.tar") {
			continue
		}
		input, err := os.Open(filepath.Join(source, entry.Name()))
		if err != nil {
			return err
		}
		outputPath := filepath.Join(cfg.RepoRoot(), entry.Name())
		output, err := os.Create(outputPath)
		if err != nil {
			input.Close()
			return err
		}
		hash := sha256.New()
		size, copyErr := io.Copy(io.MultiWriter(output, hash), input)
		input.Close()
		output.Close()
		if copyErr != nil {
			return copyErr
		}
		exists, err := db.Artifact.Query().Where(artifact.JobIDEQ(int64(jobID)), artifact.FilenameEQ(entry.Name())).Exist(ctx)
		if err != nil {
			return err
		}
		if !exists {
			if _, err := db.Artifact.Create().SetJobID(int64(jobID)).SetFilename(entry.Name()).SetSha256(hex.EncodeToString(hash.Sum(nil))).SetSizeBytes(size).SetPublishedAt(time.Now()).Save(ctx); err != nil {
				return err
			}
		}
		files = append(files, outputPath)
	}
	if len(files) == 0 {
		return fmt.Errorf("job produced no package artifacts")
	}
	args := append([]string{"-R", filepath.Join(cfg.RepoRoot(), cfg.RepositoryName+".db.tar.gz")}, files...)
	if err := exec.CommandContext(ctx, "repo-add", args...).Run(); err != nil {
		return err
	}
	return JobArtifacts(ctx, db, jobID)
}

func NotifyFailure(ctx context.Context, cfg Config, subject string, failure error) {
	if cfg.NtfyURL == "" || cfg.NtfyTopic == "" {
		return
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.NtfyURL+"/"+cfg.NtfyTopic, strings.NewReader("Aurforge build failure for "+subject+"\n"+failure.Error()))
	if err != nil {
		return
	}
	request.Header.Set("Title", "Aurforge build failed")
	request.Header.Set("Priority", "high")
	if cfg.NtfyToken != "" {
		request.Header.Set("Authorization", "Bearer "+cfg.NtfyToken)
	}
	_, _ = http.DefaultClient.Do(request)
}

func logFile(cfg Config, jobID int) string {
	return filepath.Join(cfg.DataRoot, "logs", fmt.Sprintf("job-%d.log", jobID))
}
