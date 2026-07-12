package internal

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dudebehinddude/aurforge/ent"
)

type AURResult struct {
	Name        string `json:"Name"`
	Version     string `json:"Version"`
	Description string `json:"Description"`
}

type aurResponse struct {
	Results []AURResult `json:"results"`
}

func SearchAUR(ctx context.Context, query string) ([]AURResult, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://aur.archlinux.org/rpc/v5/search/"+query+"?by=name-desc", nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("AUR search returned %s", response.Status)
	}
	var result aurResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Results, nil
}

type Preview struct {
	Package PackageInfo
	Digest  string
	Path    string
	Audit   []string
}

type AURNode struct {
	Preview  Preview
	Revision string
}

func PreviewLocal(cfg Config, path string) (Preview, error) {
	cleanRoot, err := filepath.Abs(cfg.LocalImportRoot)
	if err != nil {
		return Preview{}, err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cleanRoot, path)
	}
	cleanPath, err := filepath.Abs(path)
	if err != nil {
		return Preview{}, err
	}
	if cleanPath != cleanRoot && !strings.HasPrefix(cleanPath, cleanRoot+string(os.PathSeparator)) {
		return Preview{}, fmt.Errorf("local import must be below %s", cleanRoot)
	}
	info, err := ParsePackage(cleanPath, "local", cleanPath)
	if err != nil {
		return Preview{}, err
	}
	digest, err := HashTree(cleanPath)
	if err != nil {
		return Preview{}, err
	}
	audit, err := AuditPackage(cleanPath)
	if err != nil {
		return Preview{}, err
	}
	return Preview{Package: info, Digest: digest, Path: cleanPath, Audit: audit}, nil
}

func PreviewAUR(ctx context.Context, cfg Config, name string) (Preview, string, error) {
	root := filepath.Join(cfg.SourceRoot(), "aur", name, "preview")
	if err := os.RemoveAll(root); err != nil {
		return Preview{}, "", err
	}
	if err := os.MkdirAll(filepath.Dir(root), 0o750); err != nil {
		return Preview{}, "", err
	}
	if err := run(ctx, "git", "clone", "--depth=1", "https://aur.archlinux.org/"+name+".git", root); err != nil {
		return Preview{}, "", err
	}
	revision, err := output(ctx, "git", "-C", root, "rev-parse", "HEAD")
	if err != nil {
		return Preview{}, "", err
	}
	info, err := ParsePackage(root, "aur", name)
	if err != nil {
		return Preview{}, "", err
	}
	if err := classifyAURDependencies(ctx, &info); err != nil {
		return Preview{}, "", err
	}
	digest, err := HashTree(root)
	if err != nil {
		return Preview{}, "", err
	}
	audit, err := AuditPackage(root)
	if err != nil {
		return Preview{}, "", err
	}
	return Preview{Package: info, Digest: digest, Path: root, Audit: audit}, strings.TrimSpace(revision), nil
}

func ResolveAURGraph(ctx context.Context, cfg Config, root string) ([]AURNode, error) {
	visited := map[string]bool{}
	var nodes []AURNode
	var visit func(string) error
	visit = func(name string) error {
		if visited[name] {
			return nil
		}
		visited[name] = true
		preview, revision, err := PreviewAUR(ctx, cfg, name)
		if err != nil {
			return err
		}
		nodes = append(nodes, AURNode{Preview: preview, Revision: revision})
		for _, dep := range preview.Package.Dependencies {
			if dep.AUR {
				if err := visit(dep.Name); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(root); err != nil {
		return nil, err
	}
	return nodes, nil
}

func classifyAURDependencies(ctx context.Context, info *PackageInfo) error {
	for index := range info.Dependencies {
		found, err := AURPackageExists(ctx, info.Dependencies[index].Name)
		if err != nil {
			return err
		}
		info.Dependencies[index].AUR = found
	}
	return nil
}

func AURPackageExists(ctx context.Context, name string) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://aur.archlinux.org/rpc/v5/info?arg[]="+name, nil)
	if err != nil {
		return false, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("AUR info returned %s", response.Status)
	}
	var result aurResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return false, err
	}
	for _, item := range result.Results {
		if item.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func AcceptLocal(ctx context.Context, cfg Config, db *ent.Client, preview Preview) (int, error) {
	target := filepath.Join(cfg.SourceRoot(), "local", preview.Package.Name, preview.Digest)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		if err := os.MkdirAll(target, 0o750); err != nil {
			return 0, err
		}
		if err := CopyTree(preview.Path, target); err != nil {
			return 0, err
		}
	}
	return CreatePackage(ctx, db, preview.Package, target, preview.Digest, preview.Digest, cfg.UpdateDelay)
}

func AcceptAUR(ctx context.Context, cfg Config, db *ent.Client, preview Preview, revision string) (int, error) {
	target := filepath.Join(cfg.SourceRoot(), "aur", preview.Package.Name, revision)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return 0, err
		}
		if err := os.Rename(preview.Path, target); err != nil {
			if err := CopyTree(preview.Path, target); err != nil {
				return 0, err
			}
		}
	}
	return CreatePackage(ctx, db, preview.Package, target, revision, preview.Digest, cfg.UpdateDelay)
}

func PromptConfirm(reader *bufio.Reader, writer *os.File, prompt string, yes bool) (bool, error) {
	if yes {
		return true, nil
	}
	fmt.Fprintf(writer, "%s [Y/n] ", prompt)
	answer, err := reader.ReadString('\n')
	if err != nil {
		return false, err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer == "" || answer == "y" || answer == "yes" {
		return true, nil
	}
	return false, nil
}

func run(ctx context.Context, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdout = os.Stderr
	command.Stderr = os.Stderr
	return command.Run()
}

func output(ctx context.Context, name string, args ...string) (string, error) {
	childCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	value, err := exec.CommandContext(childCtx, name, args...).Output()
	return strings.TrimSpace(string(value)), err
}
