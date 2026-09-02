package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// inspectPrivateRegularFile validates protected runtime files without following
// symbolic links. The gateway is Linux-only, so ownership is checked against
// the effective uid exposed by syscall.Stat_t.
func inspectOwnedRegularFile(path, label string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect %s %s: %w", label, path, err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s must be a regular file and not a symlink: %s", label, path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return false, fmt.Errorf("%s must be owned by the LocalRouter user: %s", label, path)
	}
	return true, nil
}

func inspectPrivateRegularFile(path, label string) (bool, error) {
	exists, err := inspectOwnedRegularFile(path, label)
	if err != nil || !exists {
		return exists, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return false, fmt.Errorf("inspect %s %s: %w", label, path, err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return false, fmt.Errorf("%s must not be group or world accessible: %s", label, path)
	}
	return true, nil
}

func requirePrivateRegularFile(path, label string) error {
	exists, err := inspectPrivateRegularFile(path, label)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%s does not exist: %s", label, path)
	}
	return nil
}

func createPrivateFile(path, label string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, credentialMode)
	if err != nil {
		return nil, fmt.Errorf("create %s %s: %w", label, path, err)
	}
	if err := file.Chmod(credentialMode); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("protect %s %s: %w", label, path, err)
	}
	return file, nil
}

func openPrivateAppendFile(path, label string) (*os.File, error) {
	exists, err := inspectPrivateRegularFile(path, label)
	if err != nil {
		return nil, err
	}
	flags := os.O_WRONLY | os.O_APPEND
	if !exists {
		flags |= os.O_CREATE | os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, credentialMode)
	if err != nil {
		return nil, fmt.Errorf("open %s %s: %w", label, path, err)
	}
	if err := file.Chmod(credentialMode); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("protect %s %s: %w", label, path, err)
	}
	return file, nil
}
