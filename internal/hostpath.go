package internal

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type bindMount struct {
	Destination string
	Source      string
}

var containerIDPattern = regexp.MustCompile(`(?i)(?:docker-|cri-containerd-)?([0-9a-f]{12,64})(?:\.scope)?$`)

// lookupHostPath resolves a container path to the host path Docker used for its
// bind mount. Tests may replace this.
var lookupHostPath = hostPathFor

func hostPathFor(containerPath string) (string, error) {
	id, err := currentContainerID()
	if err != nil {
		return "", err
	}
	mounts, err := dockerContainerMounts(id)
	if err != nil {
		return "", err
	}
	return hostPathFromBindMounts(containerPath, mounts)
}

func hostPathFromBindMounts(containerPath string, mounts []bindMount) (string, error) {
	containerPath, err := absPath(containerPath)
	if err != nil {
		return "", err
	}
	var best *bindMount
	for index := range mounts {
		mount := &mounts[index]
		if mount.Source == "" || !pathHasPrefix(containerPath, mount.Destination) {
			continue
		}
		if best == nil || len(mount.Destination) > len(best.Destination) {
			best = mount
		}
	}
	if best == nil {
		return "", fmt.Errorf("no docker bind mount covers %q", containerPath)
	}
	relative, err := filepath.Rel(best.Destination, containerPath)
	if err != nil {
		return "", err
	}
	if relative == "." {
		return best.Source, nil
	}
	return filepath.Join(best.Source, relative), nil
}

func dockerContainerMounts(containerID string) ([]bindMount, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	text, err := output(ctx, "docker", "inspect", "-f", `{{range .Mounts}}{{.Destination}}{{"\t"}}{{.Source}}{{"\n"}}{{end}}`, containerID)
	if err != nil {
		return nil, fmt.Errorf("docker inspect mounts for %s: %w", containerID, err)
	}
	var mounts []bindMount
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		destination, source, ok := strings.Cut(line, "\t")
		if !ok || destination == "" || source == "" {
			continue
		}
		mounts = append(mounts, bindMount{
			Destination: filepath.Clean(destination),
			Source:      source,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(mounts) == 0 {
		return nil, fmt.Errorf("container %s has no bind mounts", containerID)
	}
	return mounts, nil
}

func currentContainerID() (string, error) {
	if id, err := containerIDFromCgroup("/proc/self/cgroup"); err == nil {
		return id, nil
	}
	hostname, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("determine container id: %w", err)
	}
	if hostname == "" {
		return "", fmt.Errorf("determine container id: empty hostname")
	}
	return hostname, nil
}

func containerIDFromCgroup(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, ":")
		payload := line
		if len(parts) >= 3 {
			payload = parts[len(parts)-1]
		}
		for _, segment := range strings.Split(payload, "/") {
			if match := containerIDPattern.FindStringSubmatch(segment); match != nil {
				return match[1], nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("container id not found in cgroup")
}

func pathHasPrefix(path, prefix string) bool {
	path = filepath.Clean(path)
	prefix = filepath.Clean(prefix)
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+string(os.PathSeparator))
}

func absPath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	return filepath.Abs(path)
}

func resolveHostDataRoot(dataRoot, configured string) (string, error) {
	if source, err := lookupHostPath(dataRoot); err == nil && filepath.IsAbs(source) {
		return source, nil
	} else if err != nil && configured != "" && !filepath.IsAbs(configured) {
		return "", fmt.Errorf("resolve host data root for %q: %w (set an absolute AURFORGE_DATA_ROOT or ensure the worker can docker inspect itself)", dataRoot, err)
	}
	if filepath.IsAbs(configured) {
		return configured, nil
	}
	if configured == "" {
		return "", fmt.Errorf("AURFORGE_HOST_DATA_ROOT is required when %q is not a docker bind mount", dataRoot)
	}
	return "", fmt.Errorf("AURFORGE_HOST_DATA_ROOT %q is not absolute and docker bind-mount discovery failed for %q", configured, dataRoot)
}
