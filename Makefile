# grokbox — a chat room behind a key.
#
#   make          build it, install it, and prove it runs from anywhere
#   make build    just produce ./bin/grokbox
#   make install  put grokbox on $PATH
#   make update   stop any running server, reinstall, start it again

BINARY  := grokbox
VERSION := $(shell git describe --tags --dirty --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# /usr/local/bin when this machine lets us write there, ~/.local/bin otherwise.
PREFIX ?= $(shell if [ -w /usr/local/bin ]; then echo /usr/local; else echo $(HOME)/.local; fi)
BINDIR := $(PREFIX)/bin

# Command lines of the servers `make update` stopped, so it can start them again.
RESTART := .make-restart

.PHONY: all build install uninstall update test race fmt vet dist clean stop-servers start-servers check-path

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
