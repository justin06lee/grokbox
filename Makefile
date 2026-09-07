# grokbox — a chat room behind a key.
#
#   make          build it, install it, and prove it runs from anywhere
#   make build    just produce ./bin/grokbox
#   make install  put grokbox on $PATH
#   make update   stop any running server, reinstall, start it again
#   make service  run it as a systemd service, surviving reboots (Linux)
#
#   make app      build the desktop app, install it, and open it

BINARY  := grokbox
# --match 'v*' so a feature tag never becomes the version: every completed
# feature is tagged too, and without this a dev build reports the last feature
# name instead of the last release.
VERSION := $(shell git describe --tags --match 'v*' --dirty --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# /usr/local/bin when this machine lets us write there, ~/.local/bin otherwise.
PREFIX ?= $(shell if [ -w /usr/local/bin ]; then echo /usr/local; else echo $(HOME)/.local; fi)
BINDIR := $(PREFIX)/bin

# Command lines of the servers `make update` stopped, so it can start them again.
RESTART := .make-restart

.PHONY: all build install uninstall update service unservice test race fmt vet dist clean stop-servers start-servers check-path \
        app app-build app-bundle app-install app-quit app-dist app-clean

all: build install check-path
	@echo
	@echo "  grokbox $(VERSION) installed to $(BINDIR)/$(BINARY)"
	@echo
	@echo "  host a room:   grokbox serve"
	@echo "  join one:      grokbox join <invite-code> --name <your-name>"
	@echo

build:
	@mkdir -p bin
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) .

install: build
	@mkdir -p $(BINDIR)
	install -m 0755 bin/$(BINARY) $(BINDIR)/$(BINARY)

uninstall:
	@rm -f $(BINDIR)/$(BINARY)

# Refresh a live install: stop the servers running the old binary, replace it,
# and start them again with the arguments they had.
update: stop-servers uninstall build install start-servers check-path
	@echo "  grokbox $(VERSION) is live"

stop-servers:
	@ps -axo pid=,args= 2>/dev/null | sed 's/^ *//' | grep '[g]rokbox serve' > $(RESTART) || true
	@while read -r pid args; do \
		echo "  stopping server $$pid"; \
		kill "$$pid" 2>/dev/null || true; \
	done < $(RESTART)

start-servers:
	@while read -r pid args; do \
		echo "  restarting: $$args"; \
		nohup sh -c "$$args" >/dev/null 2>&1 & \
	done < $(RESTART)
	@rm -f $(RESTART)

# A server on a VM has to outlive the ssh session that started it, and come
# back after a reboot. Kept out of `make` on purpose: installing a service that
# starts itself is a bigger thing to do to a machine than copying a binary.
service: install
	@if [ "$$(uname -s)" != "Linux" ]; then \
		echo "  make service installs a systemd unit, and this machine is not Linux."; \
		echo "  Run grokbox serve under whatever supervises processes here."; \
		exit 1; \
	fi
	@printf '%s\n' \
		'[Unit]' \
		'Description=grokbox — a chat room behind a key' \
		'After=network-online.target' \
		'Wants=network-online.target' \
		'' \
		'[Service]' \
		'ExecStart=$(BINDIR)/$(BINARY) serve' \
		'Environment=GROKBOX_HOME=/var/lib/grokbox' \
		'EnvironmentFile=-/etc/grokbox.env' \
		'Restart=on-failure' \
		'RestartSec=2' \
		'DynamicUser=yes' \
		'StateDirectory=grokbox' \
		'NoNewPrivileges=true' \
		'PrivateTmp=true' \
		'ProtectSystem=strict' \
		'ProtectHome=true' \
		'' \
		'[Install]' \
		'WantedBy=multi-user.target' \
		| sudo tee /etc/systemd/system/grokbox.service >/dev/null
	sudo systemctl daemon-reload
	sudo systemctl enable --now grokbox
	@echo
	@echo "  grokbox is running and will come back after a reboot."
	@echo "  its invites:  sudo $(BINARY) rooms --store /var/lib/grokbox/server"
	@echo "  its log:      journalctl -u grokbox -f"
	@echo "  settings:     /etc/grokbox.env  (GROKBOX_ADDR, GROKBOX_ADVERTISE, GROKBOX_ROOM)"
	@echo

unservice:
	-sudo systemctl disable --now grokbox
	-sudo rm -f /etc/systemd/system/grokbox.service
	-sudo systemctl daemon-reload

check-path:
	@case ":$$PATH:" in \
		*":$(BINDIR):"*) ;; \
		*) echo; echo "  warning: $(BINDIR) is not on your PATH."; \
		   echo "  add this to your shell profile:"; \
		   echo "      export PATH=\"$(BINDIR):\$$PATH\""; echo ;; \
	esac
	@command -v $(BINARY) >/dev/null 2>&1 && $(BINARY) version >/dev/null

test:
	go test ./...

race:
	go test -race ./...

fmt:
	gofmt -l -w .

vet:
	go vet ./...

# Binaries to hand to people who would rather not install Go.
dist: clean
	@mkdir -p dist
	@for platform in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64; do \
		os=$${platform%/*}; arch=$${platform#*/}; ext=""; \
		[ "$$os" = "windows" ] && ext=".exe"; \
		echo "  $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' \
			-o dist/$(BINARY)-$$os-$$arch$$ext . || exit 1; \
	done
	@echo "  binaries in ./dist"

clean: app-clean
	rm -rf bin dist $(RESTART)

# ---------------------------------------------------------- the desktop app
#
# The app is a separate Go module on purpose: it pulls in a GUI framework, and
# `go install github.com/justin06lee/grokbox@latest` must stay a small binary
# whose only dependency outside the standard library is golang.org/x/term.
#
# It links the same internal/client the CLI does, so there is one implementation
# of the protocol and one of the certificate pinning.

APP_NAME   := grokbox
# What a person reads: the menu bar, the dock, and — because the Finder labels
# an app with its file name, not CFBundleName — the bundle directory too. An
# app called "Grok Box" everywhere except the one place people go looking for
# it was the whole of the confusion. The binary inside, the bundle id and the
# CLI all stay grokbox, so pkill patterns and `open -b` still find it.
APP_LABEL  := Grok Box
APP_ID     := com.grokbox.app
APP_DIR    := app
APP_BUNDLE := $(APP_DIR)/bin/$(APP_LABEL).app
# Where it lands, and what it was called before the rename, so an upgrade does
# not leave two of them in /Applications for the Finder to number.
APP_DEST   := /Applications/$(APP_LABEL).app
APP_WAS    := /Applications/$(APP_NAME).app
# 13.0 is what the Wails Objective-C sources are built for; saying so here is
# what silences a screenful of linker warnings about mismatched versions.
export MACOSX_DEPLOYMENT_TARGET := 13.0

app: app-quit app-build app-bundle app-install
	@echo
	@echo "  $(APP_LABEL) $(VERSION) — the desktop app"
ifeq ($(shell uname -s),Darwin)
	@open -a "$(APP_DEST)"
	@echo "  opened from /Applications. It stays in the menu bar when you close the window."
else
	@echo "  installed to $(BINDIR)/$(APP_NAME)-app"
endif
	@echo

app-build:
	@mkdir -p $(APP_DIR)/bin
	cd $(APP_DIR) && go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(APP_NAME) .

# macOS wants a bundle, not a binary: notifications need a bundle identifier,
# and the dock badge needs something to sit on. Everywhere else this is a no-op.
app-bundle:
ifeq ($(shell uname -s),Darwin)
	@rm -rf "$(APP_BUNDLE)" "$(APP_DIR)/bin/$(APP_NAME).app" $(APP_DIR)/bin/icon.iconset
	@mkdir -p "$(APP_BUNDLE)/Contents/MacOS" "$(APP_BUNDLE)/Contents/Resources"
	@cp $(APP_DIR)/bin/$(APP_NAME) "$(APP_BUNDLE)/Contents/MacOS/$(APP_NAME)"
	@mkdir -p $(APP_DIR)/bin/icon.iconset
	@for size in 16 32 128 256 512; do \
		sips -z $$size $$size $(APP_DIR)/build/icon.png \
			--out $(APP_DIR)/bin/icon.iconset/icon_$${size}x$${size}.png >/dev/null; \
		sips -z $$(($$size * 2)) $$(($$size * 2)) $(APP_DIR)/build/icon.png \
			--out $(APP_DIR)/bin/icon.iconset/icon_$${size}x$${size}@2x.png >/dev/null; \
	done
	@iconutil -c icns $(APP_DIR)/bin/icon.iconset -o "$(APP_BUNDLE)/Contents/Resources/icon.icns"
	@printf '%s\n' \
		'<?xml version="1.0" encoding="UTF-8"?>' \
		'<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' \
		'<plist version="1.0"><dict>' \
		'  <key>CFBundlePackageType</key><string>APPL</string>' \
		'  <key>CFBundleName</key><string>$(APP_LABEL)</string>' \
		'  <key>CFBundleDisplayName</key><string>$(APP_LABEL)</string>' \
		'  <key>CFBundleExecutable</key><string>$(APP_NAME)</string>' \
		'  <key>CFBundleIdentifier</key><string>$(APP_ID)</string>' \
		'  <key>CFBundleIconFile</key><string>icon</string>' \
		'  <key>CFBundleVersion</key><string>$(VERSION)</string>' \
		'  <key>CFBundleShortVersionString</key><string>$(VERSION)</string>' \
		'  <key>LSMinimumSystemVersion</key><string>13.0</string>' \
		'  <key>NSHighResolutionCapable</key><true/>' \
		'</dict></plist>' \
		> "$(APP_BUNDLE)/Contents/Info.plist"
	@rm -rf $(APP_DIR)/bin/icon.iconset
	@codesign --force --sign - --identifier $(APP_ID) "$(APP_BUNDLE)" 2>/dev/null \
		|| echo "  note: could not sign the bundle; notifications will use the fallback"
endif

# The old bundle goes before the new one arrives: macOS keys an app's
# notification permission to its bundle id and signature, and leaving the stale
# copy in place is what makes a rebuilt app stop being allowed to notify.
app-install: app-bundle
ifeq ($(shell uname -s),Darwin)
	@rm -rf "$(APP_DEST)" "$(APP_WAS)"
	@cp -R "$(APP_BUNDLE)" "$(APP_DEST)"
else
	@mkdir -p $(BINDIR)
	install -m 0755 $(APP_DIR)/bin/$(APP_NAME) $(BINDIR)/$(APP_NAME)-app
	@mkdir -p $(HOME)/.local/share/applications $(HOME)/.local/share/icons
	@cp $(APP_DIR)/build/icon.png $(HOME)/.local/share/icons/$(APP_NAME).png
	@printf '%s\n' \
		'[Desktop Entry]' \
		'Type=Application' \
		'Name=$(APP_LABEL)' \
		'Comment=A chat room behind a key' \
		'Exec=$(BINDIR)/$(APP_NAME)-app' \
		'Icon=$(APP_NAME)' \
		'Categories=Network;InstantMessaging;' \
		> $(HOME)/.local/share/applications/$(APP_NAME).desktop
endif

app-quit:
ifeq ($(shell uname -s),Darwin)
	@osascript -e 'quit app "$(APP_LABEL)"' 2>/dev/null || true
	@pkill -f "Contents/MacOS/$(APP_NAME)" 2>/dev/null || true
else
	@pkill -f "$(APP_NAME)-app" 2>/dev/null || true
endif

# Windows cross-compiles from anywhere because its webview binding is pure Go.
# Linux does not: it needs cgo against webkit2gtk, so that one is built on Linux
# or not at all. Saying so here beats a target that silently produces nothing.
app-dist: app-build app-bundle
	@mkdir -p dist
ifeq ($(shell uname -s),Darwin)
	@cd $(APP_DIR)/bin && zip -qry ../../dist/$(APP_NAME)-app-macos.zip "$(APP_LABEL).app"
	@echo "  dist/$(APP_NAME)-app-macos.zip"
endif
	@GOOS=windows GOARCH=amd64 CGO_ENABLED=0 sh -c 'cd $(APP_DIR) && go build -trimpath -ldflags "$(LDFLAGS)" -o ../dist/$(APP_NAME)-app-windows-amd64.exe .'
	@echo "  dist/$(APP_NAME)-app-windows-amd64.exe"
ifeq ($(shell uname -s),Linux)
	@cp $(APP_DIR)/bin/$(APP_NAME) dist/$(APP_NAME)-app-linux-$(shell go env GOARCH)
	@echo "  dist/$(APP_NAME)-app-linux-$(shell go env GOARCH)"
else
	@echo "  linux: build on a Linux machine — its webview needs cgo and webkit2gtk headers"
endif

app-clean:
	rm -rf $(APP_DIR)/bin

# A portable demo, ready to double-click or share. No server or account needed.
.PHONY: demo
demo:
	VERSION=$(VERSION) bun run app/demo-build.ts

# Build a separate native presentation without replacing the installed app.
# APP_NAME changes too: sharing bin/grokbox with the real app would leave a
# demo-only binary sitting where the next app-dist expects the real one.
.PHONY: app-demo
app-demo: demo
	$(MAKE) app-build app-bundle APP_NAME=grokbox-demo APP_LABEL='Grok Box Demo' APP_ID=com.grokbox.demo LDFLAGS='$(LDFLAGS) -X main.demoMode=1'

# The demo as release downloads. The single HTML file is the one to hand
# somebody: it opens in a browser, so it has no bundle to unzip and nothing
# for Gatekeeper to quarantine.
.PHONY: demo-dist
demo-dist: app-demo
	@mkdir -p dist
ifeq ($(shell uname -s),Darwin)
	@cd $(APP_DIR)/bin && zip -qry "../../dist/$(BINARY)-demo-app-macos.zip" "Grok Box Demo.app"
	@echo "  dist/$(BINARY)-demo-app-macos.zip"
endif
	@echo "  dist/Grok Box Demo.html"
