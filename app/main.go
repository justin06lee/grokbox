// Command grokbox-app is the grokbox chat room as a desktop application.
//
// It is not a second client: it links the same internal/client the CLI does,
// reads the same rooms out of the same config file, and pins the server's
// certificate the same way. What it adds is the two things a terminal cannot
// do — tell you when somebody says your name while you are looking at
// something else, and hold every room you are in open at once.
package main

import (
	"embed"
	"log"
	"os"
	"runtime"
	"runtime/debug"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/dock"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

//go:embed all:frontend
var assets embed.FS

//go:embed build/tray.png
var trayIcon []byte

//go:embed build/icon.png
var appIcon []byte

// bundleID must match CFBundleIdentifier in the bundle the Makefile builds:
// it is how a notification click finds the app again, and how macOS keeps one
// instance from becoming two.
const bundleID = "com.grokbox.app"

// version is stamped at build time by the Makefile.
var version = ""

// demoMode is set only in the dedicated presentation build.
var demoMode = ""

func resolveVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				return s.Value[:7]
			}
		}
	}
	return "dev"
}

func main() {
	version = resolveVersion()
	if demoMode == "1" {
		runDemo()
		return
	}
	for _, arg := range os.Args[1:] {
		if arg == "--demo" {
			runDemo()
			return
		}
	}

	rooms := NewManager()
	notes := notifications.New()
	dk := dock.New()
	api := &API{m: rooms}

	app := application.New(application.Options{
		// The name macOS shows: in the menu bar, the dock, and the
		// notification banners. The binary, the bundle and the CLI stay
		// "grokbox" — this is only what a person reads.
		Name:        "Grok Box",
		Description: "A chat room behind a key.",
		Icon:        appIcon,
		Services: []application.Service{
			application.NewService(api),
			application.NewService(notes),
			application.NewService(dk),
		},
		// BundledAssetFileServer is what serves /wails/runtime.js, which is
		// how the page talks to this process. Without it the frontend would
		// need a build step and an npm dependency to get the same file.
		Assets: application.AssetOptions{Handler: application.BundledAssetFileServer(assets)},
		Mac: application.MacOptions{
			// Closing the window must not end the app: the streams it holds
			// are the reason a notification arrives at all.
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		// One instance, or two copies of the app fight each other for every
		// room's name and neither stays connected.
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: bundleID,
		},
		OnShutdown: rooms.Shutdown,
	})

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "Grok Box",
		Width:            1080,
		Height:           720,
		MinWidth:         720,
		MinHeight:        460,
		BackgroundColour: application.NewRGB(252, 252, 252), // --sand-bg-base
		URL:              "/",
		Mac: application.MacWindow{
			// The room header sits under the traffic lights, so the window
			// reads as one surface rather than a web page in a frame. 48pt
			// is the strip Grok Bot leaves above its search field.
			TitleBar:                application.MacTitleBarHiddenInset,
			InvisibleTitleBarHeight: 48,
		},
	})

	rooms.attach(app, notes, dk)
	// The clipboard and the file dialog hang off the application, which does
	// not exist until now — the service was registered before it did.
	api.app = app

	// Closing the window hides it. Quitting is the tray menu or ⌘Q, and both
	// are deliberate acts — clicking the red button to stop being told when
	// you are named would be a trap.
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		window.Hide()
		e.Cancel()
	})
	window.RegisterHook(events.Common.WindowFocus, func(*application.WindowEvent) {
		rooms.SetFocus(true)
	})
	window.RegisterHook(events.Common.WindowLostFocus, func(*application.WindowEvent) {
		rooms.SetFocus(false)
	})

	show := func() {
		window.Show()
		window.Focus()
	}

	tray := app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(trayIcon)
	} else {
		tray.SetIcon(trayIcon)
	}
	tray.SetTooltip("Grok Box")
	menu := app.NewMenu()
	menu.Add("Open Grok Box").OnClick(func(*application.Context) { show() })
	menu.AddSeparator()
	menu.Add("Quit Grok Box").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(menu)
	tray.OnClick(show)
	rooms.tray = tray

	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		// Asking up front means the permission dialog arrives while the app
		// is in front, rather than in the middle of somebody saying your name.
		go func() {
			ok, err := notes.RequestNotificationAuthorization()
			rooms.AllowNotifications(ok && err == nil)
			if err != nil {
				app.Logger.Warn("no notification permission, using the fallback", "error", err)
			}
		}()
		if err := rooms.Load(); err != nil {
			app.Logger.Error("cannot read saved rooms", "error", err)
		}
		// Your picture, if this machine is signed in to GitHub anywhere. It
		// runs behind the window rather than in front of it: a missing avatar
		// is a coloured disc, not a reason to wait.
		rooms.findPhoto()
	})

	if err := app.Run(); err != nil {
		log.Println("grokbox:", err)
		os.Exit(1)
	}
}

// runDemo keeps the presentation separate from saved rooms, notifications and
// bot webhooks. It can run beside the real app without joining any rooms.
func runDemo() {
	app := application.New(application.Options{
		Name:        "Grok Box",
		Description: "A scripted peer-room conversation.",
		Icon:        appIcon,
		Assets:      application.AssetOptions{Handler: application.BundledAssetFileServer(assets)},
		Mac:         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "main", Title: "Grok Box", Width: 1080, Height: 720,
		MinWidth: 720, MinHeight: 460, URL: "/?demo=1",
		Mac:              application.MacWindow{TitleBar: application.MacTitleBarHiddenInset, InvisibleTitleBarHeight: 48},
		BackgroundColour: application.NewRGB(252, 252, 252),
	})
	if err := app.Run(); err != nil {
		log.Println("grokbox demo:", err)
		os.Exit(1)
	}
}
