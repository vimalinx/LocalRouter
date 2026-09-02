package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const localRouterAppDir = "localrouter"

type xdgDirectories struct {
	ConfigDir string
	DataDir   string
	StateDir  string
	CacheDir  string
}

func resolveXDGDirectories() (xdgDirectories, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return xdgDirectories{}, errors.New("resolve home directory for XDG paths")
	}
	base := func(variable, fallback string) string {
		value := strings.TrimSpace(os.Getenv(variable))
		if value == "" || !filepath.IsAbs(value) {
			value = filepath.Join(home, fallback)
		}
		return filepath.Clean(value)
	}
	return xdgDirectories{
		ConfigDir: filepath.Join(base("XDG_CONFIG_HOME", ".config"), localRouterAppDir),
		DataDir:   filepath.Join(base("XDG_DATA_HOME", filepath.Join(".local", "share")), localRouterAppDir),
		StateDir:  filepath.Join(base("XDG_STATE_HOME", filepath.Join(".local", "state")), localRouterAppDir),
		CacheDir:  filepath.Join(base("XDG_CACHE_HOME", ".cache"), localRouterAppDir),
	}, nil
}

func resolveDirectoryOverride(variable, fallback string) (string, error) {
	value := strings.TrimSpace(os.Getenv(variable))
	if value == "" {
		return fallback, nil
	}
	resolved, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", variable, err)
	}
	return filepath.Clean(resolved), nil
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, credentialDirMode); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("private path is not a real directory: %s", path)
	}
	return os.Chmod(path, credentialDirMode)
}

func embeddedProtocolFileManaged(relative string) bool {
	return strings.HasPrefix(filepath.ToSlash(relative), "schema/") && strings.HasSuffix(relative, ".json")
}

func migrateLegacyProtocolCatalogFile(target, relative string) (bool, error) {
	legacy := ""
	switch filepath.ToSlash(relative) {
	case "catalogs/" + poolCatalogJSONFile:
		legacy = "hao-pools.json"
	case "catalogs/" + poolCatalogMarkdownFile:
		legacy = "hao-pools.md"
	default:
		return false, nil
	}
	destination := filepath.Join(target, filepath.FromSlash(relative))
	if _, err := os.Lstat(destination); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	legacyPath := filepath.Join(target, "catalogs", legacy)
	info, err := os.Lstat(legacyPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("legacy protocol catalog is not a regular file: %s", legacyPath)
	}
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		return false, err
	}
	if err := writeMaintenanceFileAtomic(destination, data); err != nil {
		return false, err
	}
	return true, nil
}

// The distribution seeds authoring schemas and neutral documentation assets.
// It intentionally contains no provider Pack. User-authored XDG configuration
// remains authoritative across upgrades. Runtime-owned read-only schemas are
// refreshed atomically because drafts cannot edit them.
func seedEmbeddedProtocols(target string) error {
	return fs.WalkDir(webFiles, "protocols", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel("protocols", path)
		if err != nil || relative == "." {
			return err
		}
		destination := filepath.Join(target, filepath.FromSlash(relative))
		if entry.IsDir() {
			return ensurePrivateDirectory(destination)
		}
		migrated, err := migrateLegacyProtocolCatalogFile(target, relative)
		if err != nil {
			return err
		}
		if migrated {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("embedded protocol source contains a symlink: %s", path)
		}
		data, err := fs.ReadFile(webFiles, path)
		if err != nil {
			return err
		}
		if info, statErr := os.Lstat(destination); statErr == nil {
			if !embeddedProtocolFileManaged(relative) {
				return nil
			}
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("managed protocol schema is not a regular file: %s", destination)
			}
			current, readErr := os.ReadFile(destination)
			if readErr != nil {
				return readErr
			}
			if bytes.Equal(current, data) {
				return os.Chmod(destination, credentialMode)
			}
			return writeMaintenanceFileAtomic(destination, data)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		if err := ensurePrivateDirectory(filepath.Dir(destination)); err != nil {
			return err
		}
		file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, credentialMode)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				return nil
			}
			return err
		}
		if _, err := file.Write(data); err != nil {
			_ = file.Close()
			_ = os.Remove(destination)
			return err
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			_ = os.Remove(destination)
			return err
		}
		return file.Close()
	})
}

// Channel protocol profiles are operator configuration, not runtime code.
// Seed the shipped examples only once and preserve every later local edit.
func seedEmbeddedChannelProfiles(destination string) error {
	if info, err := os.Lstat(destination); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("channel profile config is not a regular file: %s", destination)
		}
		return os.Chmod(destination, credentialMode)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := fs.ReadFile(webFiles, "channel-profiles.json")
	if err != nil {
		return err
	}
	if err := ensurePrivateDirectory(filepath.Dir(destination)); err != nil {
		return err
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, credentialMode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(destination)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(destination)
		return err
	}
	return file.Close()
}
