package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPoolCatalogLoadsGenericOperatorFile(t *testing.T) {
	root := t.TempDir()
	catalogDir := filepath.Join(root, "catalogs")
	require.NoError(t, os.MkdirAll(catalogDir, 0o700))
	generic := `{"schema_version":"1","observed_at":"2026-09-02T00:00:00Z","source":"generic","summary":{"indexed":1},"pools":[{"id":"generic-pool","records":0,"format":"json","status":"configuration-required","adapter":"protocol-pack","owner":"operator","note":"generic distribution data"}]}`
	require.NoError(t, os.WriteFile(filepath.Join(catalogDir, poolCatalogJSONFile), []byte(generic), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(catalogDir, poolCatalogMarkdownFile), []byte("generic\n"), 0o600))

	catalog, _, err := loadProtocolPoolCatalog(root)
	require.NoError(t, err)
	assert.Equal(t, "generic", catalog.Source)
}

func TestPoolCatalogAcceptsAnExplicitlyEmptyDistribution(t *testing.T) {
	root := t.TempDir()
	catalogDir := filepath.Join(root, "catalogs")
	require.NoError(t, os.MkdirAll(catalogDir, 0o700))
	empty := `{"schema_version":"1","observed_at":"2026-09-02T00:00:00Z","source":"empty distribution","summary":{"indexed":0},"pools":[]}`
	require.NoError(t, os.WriteFile(filepath.Join(catalogDir, poolCatalogJSONFile), []byte(empty), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(catalogDir, poolCatalogMarkdownFile), []byte("empty\n"), 0o600))

	catalog, _, err := loadProtocolPoolCatalog(root)
	require.NoError(t, err)
	assert.Empty(t, catalog.Pools)
}
