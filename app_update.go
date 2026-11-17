package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"log"
	goruntime "runtime"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"terminyxx/internal/store"
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
	return updater.Check(ctx, nil, updater.LatestURL, appVersion(), goruntime.GOOS, goruntime.GOARCH)
}

func (a *App) startAutoUpdate() {
	go func() {
		select {
		case <-a.stop:
			return
		case <-time.After(8 * time.Second):
		}
		a.maybeAutoUpdate()
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-a.stop:
				return
			case <-t.C:
				a.maybeAutoUpdate()
			}
		}
	}()
}

func (a *App) maybeAutoUpdate() {
	dir, err := store.DataDir()
	if err != nil {
		return
	}
	if !updater.Due(dir, time.Now()) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	out := updater.AutoCheck(ctx, nil, updater.LatestURL, dir, appVersion(), goruntime.GOOS, goruntime.GOARCH)
	switch out.Status {
	case "skip":
		if out.Err != nil {
			log.Printf("auto-update: %v", out.Err)
		}
		if errors.Is(out.Err, updater.ErrRateLimit) {
			_ = updater.SaveLastCheck(dir, time.Now())
		}
		return
	case "ok", "ready", "manual":
		_ = updater.SaveLastCheck(dir, time.Now())
		if out.Status != "ok" {
			a.emitUpdate("updater:"+out.Status, out)
		}
	}
}

func (a *App) emitUpdate(event string, out updater.Outcome) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, event, map[string]string{
		"version": updater.NormalizeVersion(out.Version),
		"url":     out.URL,
	})
}

func applyPendingUpdate(reexec bool) {
	dir, err := store.DataDir()
	if err != nil {
		return
	}
	if err := updater.ApplyStaged(dir, reexec); err != nil {
		log.Printf("pending update: %v", err)
	}
	if reexec {
		updater.CleanupBackup(dir)
	}
}
