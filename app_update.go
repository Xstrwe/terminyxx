package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"runtime"
	"time"

	"terminyxx/internal/updater"
)

// wails.json is the single source of truth for the version: the release
// workflow stamps the tag into it before building.
//
//go:embed wails.json
var wailsConfig []byte

// appVersion is the running version, e.g. "0.1.2".
func appVersion() string {
	var cfg struct {
		Info struct {
			ProductVersion string `json:"productVersion"`
		} `json:"info"`
	}
	if json.Unmarshal(wailsConfig, &cfg) != nil || cfg.Info.ProductVersion == "" {
		return "0.0.0"
	}
	return cfg.Info.ProductVersion
}

// AppVersion returns the running version.
func (a *App) AppVersion() string { return appVersion() }

// CheckForUpdate asks GitHub for the latest release and compares it with
// the running version.
func (a *App) CheckForUpdate() (updater.Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return updater.Check(ctx, nil, updater.LatestURL, appVersion(), runtime.GOOS, runtime.GOARCH)
}
