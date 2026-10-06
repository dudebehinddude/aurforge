package internal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// The scheduler needs root access to managed storage, but makepkg refuses root.
// Run trusted import updaters as the directory owner, with no supplementary groups.
func runLocalUpdater(ctx context.Context, directory string) ([]byte, error) {
	command := exec.CommandContext(ctx, "sh", filepath.Join(directory, "update-pkgbuild.sh"))
	command.Dir = directory
	if os.Geteuid() != 0 {
		return command.CombinedOutput()
	}
	info, err := os.Stat(directory)
	if err != nil {
		return nil, err
	}
	owner := info.Sys().(*syscall.Stat_t)
	uid, gid := owner.Uid, owner.Gid
	if uid == 0 {
		account, err := user.Lookup("updater")
		if err != nil {
			return nil, fmt.Errorf("root-owned import requires the non-root updater account: %w", err)
		}
		userID, err := strconv.ParseUint(account.Uid, 10, 32)
		if err != nil {
			return nil, err
		}
		groupID, err := strconv.ParseUint(account.Gid, 10, 32)
		if err != nil {
			return nil, err
		}
		uid, gid = uint32(userID), uint32(groupID)
		if uid == 0 {
			return nil, fmt.Errorf("updater account must not be root")
		}
	}
	// Earlier root-run updaters can leave private root-owned files behind when
	// they replace PKGBUILD with mktemp + mv. Restore those files to the owner.
	if err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink is not allowed in imported package: %s", path)
		}
		if entry.Name() == ".git" && entry.IsDir() {
			return filepath.SkipDir
		}
		stat, err := entry.Info()
		if err != nil {
			return err
		}
		if stat.Sys().(*syscall.Stat_t).Uid == 0 {
			return os.Lchown(path, int(uid), int(gid))
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("prepare updater ownership: %w", err)
	}
	home, err := os.MkdirTemp("", "aurforge-updater-home-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(home)
	if err := os.Chown(home, int(uid), int(gid)); err != nil {
		return nil, err
	}
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "HOME=") {
			command.Env = append(command.Env, value)
		}
	}
	command.Env = append(command.Env, "HOME="+home)
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: gid}}
	return command.CombinedOutput()
}
