package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	manager := NewProjectManager()

	err := wails.Run(&options.App{
		Title:     "Nala Ctrl",
		Width:     1280,
		Height:    840,
		MinWidth:  880,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 21, G: 26, B: 29, A: 1},
		OnStartup:        manager.startup,
		OnShutdown:       manager.shutdown,
		Bind: []interface{}{
			manager,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
