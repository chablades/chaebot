// Command Chaebot is the desktop app: it runs the bot and gives the
// streamer controls for it. cmd/chaebot runs the same bot without a window.
package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend
var assets embed.FS

func main() {
	app := &App{}
	err := wails.Run(&options.App{
		Title:            "Chaebot",
		Width:            1120,
		Height:           740,
		MinWidth:         760,
		MinHeight:        520,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 20, G: 19, B: 24, A: 255},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		// Two copies would log the bot in twice and double every reply.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "chaebot-desktop-7c41e2",
			OnSecondInstanceLaunch: app.focus,
		},
		Bind:    []any{app},
		Windows: &windows.Options{Theme: windows.SystemDefault},
	})
	if err != nil {
		log.Fatal(err)
	}
}
