package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedProtocolUpgradeRefreshesOnlyManagedSchemas(t *testing.T) {
	target := t.TempDir()
	require.NoError(t, seedEmbeddedProtocols(target))
	schemaPath := filepath.Join(target, "schema", "protocol-pack-v3.schema.json")
	packPath := filepath.Join(target, "operator-pack.json")
	originalSchema, err := os.ReadFile(schemaPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(schemaPath, []byte(`{"stale":true}`), 0o600))
	require.NoError(t, os.WriteFile(packPath, []byte(`{"owner":"user"}`), 0o600))

	require.NoError(t, seedEmbeddedProtocols(target))
	refreshedSchema, err := os.ReadFile(schemaPath)
	require.NoError(t, err)
	assert.Equal(t, originalSchema, refreshedSchema)
	userPack, err := os.ReadFile(packPath)
	require.NoError(t, err)
	assert.JSONEq(t, `{"owner":"user"}`, string(userPack))
	info, err := os.Stat(schemaPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestEmbeddedProtocolSeedMigratesLegacyOperatorPoolCatalog(t *testing.T) {
	target := t.TempDir()
	legacyDir := filepath.Join(target, "catalogs")
	require.NoError(t, os.MkdirAll(legacyDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "hao-pools.json"), []byte(`{"operator":"owned"}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "hao-pools.md"), []byte("operator owned\n"), 0o600))

	require.NoError(t, seedEmbeddedProtocols(target))
	jsonCatalog, jsonErr := os.ReadFile(filepath.Join(legacyDir, poolCatalogJSONFile))
	markdownCatalog, markdownErr := os.ReadFile(filepath.Join(legacyDir, poolCatalogMarkdownFile))
	require.NoError(t, jsonErr)
	require.NoError(t, markdownErr)
	assert.JSONEq(t, `{"operator":"owned"}`, string(jsonCatalog))
	assert.Equal(t, "operator owned\n", string(markdownCatalog))
	assert.FileExists(t, filepath.Join(legacyDir, "hao-pools.json"))
	assert.FileExists(t, filepath.Join(legacyDir, "hao-pools.md"))
}

func TestEmbeddedChannelProfilesAreSeededOnceAndRemainOperatorOwned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channel-profiles.json")
	require.NoError(t, seedEmbeddedChannelProfiles(path))
	first, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(first), `"openai-compatible"`)

	require.NoError(t, os.WriteFile(path, []byte(`{"operator":"owned"}`), 0o600))
	require.NoError(t, seedEmbeddedChannelProfiles(path))
	second, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.JSONEq(t, `{"operator":"owned"}`, string(second))
}
