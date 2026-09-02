package main

import (
	"encoding/json"
	"fmt"
	"os"
)

var (
	buildVersion = "dev"
	buildCommit  = "none"
	buildDate    = "unknown"
)

func handleLocalCommand(arguments []string) (bool, error) {
	if len(arguments) == 0 {
		return false, nil
	}
	switch arguments[0] {
	case "version", "--version", "-version":
		fmt.Printf("localrouter %s commit=%s built=%s\n", buildVersion, buildCommit, buildDate)
		return true, nil
	case "paths":
		config, err := loadRuntimeConfig()
		if err != nil {
			return true, err
		}
		paths := map[string]string{
			"config_dir":       config.ConfigDir,
			"config_file":      config.ConfigDir + string(os.PathSeparator) + "config.env",
			"protocol_dir":     config.ProtocolDir,
			"channel_profiles": config.ChannelProfilesPath,
			"data_dir":         config.DataDir,
			"state_dir":        config.StateDir,
			"cache_dir":        config.CacheDir,
			"admin_token_file": config.AdminTokenFile,
			"api_token_file":   config.APITokenFile,
			"database_path":    config.DatabasePath,
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return true, encoder.Encode(paths)
	default:
		return false, nil
	}
}
