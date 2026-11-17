package main

import (
	"embed"
	"runtime"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Create an instance of the app structure
	app := NewApp()

	// Create application with options
	err := wails.Run(&options.App{
		Title:     "Terminyxx",
		Width:     1280,
		Height:    800,
		MinWidth:  900,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
			// Content-Security-Policy and friends (csp.go); nil under wails dev.
			Middleware: assetMiddleware,
		},
		// DevTools and the browser's own right-click menu ("Inspect") are
		// off in production builds: Wails enables them only for `wails dev`
		// or `wails build -devtools`. Don't ship builds made with -devtools.
		EnableDefaultContextMenu: false,
		BackgroundColour: &options.RGBA{R: 9, G: 10, B: 13, A: 255},
		// Files dragged in from Explorer/Finder are uploaded by the SFTP panel
		// (frontend/src/lib/fileDrop.ts). In-app HTML5 drags are unaffected.
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop: true,
		},
		// Custom title bar: frameless on Windows, inset traffic lights on macOS.
		Frameless: runtime.GOOS == "windows",
		Windows: &windows.Options{
			Theme: windows.Dark,
		},
		Mac: &mac.Options{
			TitleBar:   mac.TitleBarHiddenInset(),
			Appearance: mac.NSAppearanceNameDarkAqua,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
