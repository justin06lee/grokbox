# grokbox — a chat room behind a key.
#
#   make          build it, install it, and prove it runs from anywhere
#   make build    just produce ./bin/grokbox
#   make install  put grokbox on $PATH
#   make update   stop any running server, reinstall, start it again
#   make service  run it as a systemd service, surviving reboots (Linux)

BINARY  := grokbox
VERSION := $(shell git describe --tags --dirty --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# /usr/local/bin when this machine lets us write there, ~/.local/bin otherwise.
PREFIX ?= $(shell if [ -w /usr/local/bin ]; then echo /usr/local; else echo $(HOME)/.local; fi)
BINDIR := $(PREFIX)/bin

# Command lines of the servers `make update` stopped, so it can start them again.
RESTART := .make-restart

.PHONY: all build install uninstall update service unservice test race fmt vet dist clean stop-servers start-servers check-path

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

clean:
	rm -rf bin dist $(RESTART)
