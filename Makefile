# necorouter: build, tests, archives.
#
#   make              static binaries dist/necorouter-x86_64 and dist/necorouter-aarch64
#   make host         binary for this machine: dist/necorouter (for tests)
#   make test         go vet, unit tests and end-to-end tests (root and unshare required)
#   make archive      one archive release/necorouter-buildN_DATE.tar.gz (N is the build number, incremented on every run):
#                       necorouter/dist/       x86_64 and aarch64 binaries
#                       necorouter/cmd, internal, go.mod, Makefile   sources
#                       necorouter/packaging/  installer and units
#                       necorouter/docs/       documentation (HTML)
#                       necorouter/test/   tests, fixtures and scripts (*_test.go live next to the sources)
#                       necorouter/README.md, BUILD
#   make clean
#
# Version: cmd/necorouter/VERSION (embedded into the binary; for example "0.21").
# Makefile adds the build date (YYYY-MM-DD). The build number is in the BUILD file and grows with every `make archive`.

DATE  := $(shell date +%Y-%m-%d)
BUILD := $(strip $(shell cat BUILD 2>/dev/null))
OUT   ?= release

LDFLAGS := -s -w -X 'necorouter/internal/cli.BuildDate=$(DATE)'
GOBUILD := CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)"

.PHONY: all host test archive clean dist/necorouter dist/necorouter-x86_64 dist/necorouter-aarch64 dist/mkveth

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

# test utilities and tests tagged nltest (veth pairs are needed only by the test bench)
dist/mkveth: | dist
	CGO_ENABLED=0 go build -tags nltest -o $@ ./test/mkveth

test: host dist/mkveth
	go vet -tags nltest ./...
	go test -tags nltest ./...
	unshare -n -u -m python3 test/pty_cli.py
	unshare -n -u -m python3 test/pty_ping.py
	unshare -n -u -m python3 test/pty_mge.py
	unshare -n -u -m python3 test/pty_pager.py
	unshare -n -u -m sh test/e2e.sh
	unshare -n -u -m sh test/persist.sh
	unshare -n -u -m sh test/examples.sh
	unshare -n -u -m sh test/addr_replace.sh
	unshare -n -u -m sh test/security.sh
	unshare -n -u -m sh test/filtermap.sh
	unshare -m sh test/install_alpine.sh
	unshare -m sh test/install_find.sh

archive: all
	@N=$$(( $(BUILD) + 1 )); echo $$N > BUILD; \
	mkdir -p $(OUT); \
	tar --transform='s,^,necorouter/,' -czf $(OUT)/necorouter-build$${N}_$(DATE).tar.gz \
		README.md BUILD Makefile go.mod \
		dist/necorouter-x86_64 dist/necorouter-aarch64 \
		cmd internal packaging docs test; \
	ls -l $(OUT)/necorouter-build$${N}_$(DATE).tar.gz

clean:
	rm -rf dist
