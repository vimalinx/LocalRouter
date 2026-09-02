package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func (registry *protocolRegistry) lockProtocolState(kind, protocolID string) (*os.File, error) {
	dir := filepath.Join(registry.stateDir, "state-locks")
	if err := ensurePrivateDirectory(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.lock", kind, protocolID))
	exists, err := inspectPrivateRegularFile(path, "protocol state lock")
	if err != nil {
		return nil, err
	}
	flags := os.O_RDWR
	if !exists {
		flags |= os.O_CREATE | os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func unlockProtocolState(file *os.File) {
	if file == nil {
		return
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}
