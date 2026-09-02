package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

type protocolPoolCatalog struct {
	SchemaVersion string                     `json:"schema_version"`
	ObservedAt    string                     `json:"observed_at"`
	Source        string                     `json:"source"`
	Summary       map[string]int             `json:"summary"`
	Pools         []protocolPoolCatalogEntry `json:"pools"`
}

type protocolPoolCatalogEntry struct {
	ID      string                 `json:"id"`
	Records int                    `json:"records"`
	Format  string                 `json:"format"`
	Status  string                 `json:"status"`
	Adapter string                 `json:"adapter"`
	Owner   string                 `json:"owner"`
	Note    string                 `json:"note"`
	Pricing []protocolPricingEntry `json:"pricing,omitempty"`
}

const (
	poolCatalogJSONFile     = "pool-catalog.json"
	poolCatalogMarkdownFile = "pool-catalog.md"
)

func loadProtocolPoolCatalog(root string) (protocolPoolCatalog, []byte, error) {
	path := filepath.Join(root, "catalogs", poolCatalogJSONFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return protocolPoolCatalog{}, nil, err
	}
	if len(data) > 1<<20 {
		return protocolPoolCatalog{}, nil, errors.New("pool catalog exceeds 1 MiB")
	}
	var catalog protocolPoolCatalog
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		return protocolPoolCatalog{}, nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return protocolPoolCatalog{}, nil, errors.New("pool catalog contains trailing JSON")
	}
	if catalog.SchemaVersion != "1" || catalog.ObservedAt == "" || catalog.Pools == nil {
		return protocolPoolCatalog{}, nil, errors.New("pool catalog requires schema_version, observed_at, and a pools array")
	}
	seen := make(map[string]bool)
	for _, entry := range catalog.Pools {
		if !protocolIDPattern.MatchString(entry.ID) || seen[entry.ID] || entry.Format == "" || entry.Status == "" || entry.Adapter == "" || entry.Owner == "" || entry.Note == "" {
			return protocolPoolCatalog{}, nil, errors.New("pool catalog contains an invalid or duplicate entry")
		}
		seen[entry.ID] = true
	}
	return catalog, data, nil
}

func validateProtocolPoolCatalog(root string) error {
	if _, err := os.Stat(filepath.Join(root, "catalogs")); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if _, _, err := loadProtocolPoolCatalog(root); err != nil {
		return err
	}
	markdown, err := os.ReadFile(filepath.Join(root, "catalogs", poolCatalogMarkdownFile))
	if err != nil {
		return err
	}
	if len(markdown) == 0 || len(markdown) > 1<<20 {
		return errors.New("pool catalog Markdown must be between 1 byte and 1 MiB")
	}
	return nil
}

func (registry *protocolRegistry) handlePoolCatalogJSON(c *gin.Context) {
	catalog, _, err := loadProtocolPoolCatalog(registry.dir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "pool catalog is unavailable"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, catalog)
}

func (registry *protocolRegistry) handlePoolCatalogMarkdown(c *gin.Context) {
	data, err := os.ReadFile(filepath.Join(registry.dir, "catalogs", poolCatalogMarkdownFile))
	if err != nil {
		c.String(http.StatusInternalServerError, "pool catalog is unavailable\n")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/markdown; charset=utf-8", data)
}
