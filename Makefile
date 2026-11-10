# necorouter: build and tests.
#
#   make              static binaries dist/necorouter-x86_64 and dist/necorouter-aarch64
#   make host         binary for this machine: dist/necorouter (for tests)
#   make docs         rewrite the command tree in docs/index.html from the code
#   make test         go vet, unit tests and end-to-end tests (root and unshare required)
#   make clean
#
# Version: cmd/necorouter/VERSION (embedded into the binary; for example "0.21").
# Makefile adds the build date (YYYY-MM-DD).

DATE  := $(shell date +%Y-%m-%d)

LDFLAGS := -s -w -X 'necorouter/internal/cli.BuildDate=$(DATE)'
GOBUILD := CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)"

.PHONY: all host test clean dist/necorouter dist/necorouter-x86_64 dist/necorouter-aarch64 dist/mkveth

all: dist/necorouter-x86_64 dist/necorouter-aarch64

dist:
	mkdir -p dist

dist/necorouter-x86_64: | dist
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o $@ ./cmd/necorouter

dist/necorouter-aarch64: | dist
	GOOS=linux GOARCH=arm64 $(GOBUILD) -o $@ ./cmd/necorouter

host: dist/necorouter

dist/necorouter: | dist
	$(GOBUILD) -o $@ ./cmd/necorouter

# test utilities and tests tagged iproute2test (veth pairs are needed only by the test bench)
dist/mkveth: | dist
	CGO_ENABLED=0 go build -tags iproute2test -o $@ ./test/mkveth

docs:
	go run ./cmd/gendocs docs/index.html

test: host dist/mkveth
	go vet -tags iproute2test ./...
	go test -tags iproute2test ./...
	unshare -n -u -m sh test/isolate.sh python3 test/pty_cli.py
	unshare -n -u -m sh test/isolate.sh python3 test/pty_ping.py
	unshare -n -u -m sh test/isolate.sh python3 test/pty_mge.py
	unshare -n -u -m sh test/isolate.sh python3 test/pty_pager.py
	unshare -n -u -m sh test/isolate.sh python3 test/pty_follow.py
	unshare -n -u -m sh test/isolate.sh python3 test/pty_paste.py
	unshare -n -u -m sh test/e2e.sh
	unshare -n -u -m sh test/persist.sh
	unshare -n -u -m sh test/examples.sh
	unshare -n -u -m sh test/addr_replace.sh
	unshare -n -u -m sh test/security.sh
	unshare -n -u -m sh test/filtermap.sh
	unshare -m sh test/install_alpine.sh
	unshare -m sh test/install_find.sh

clean:
	rm -rf dist
