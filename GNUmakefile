REPO      := github.com/a1s/sr
NAME      := sr
VERSION   ?= $(shell grep -E '^## \[' CHANGELOG.md | head -1 | sed -e 's/^## \[//' -e 's/\].*$$//')
SHA       ?= $(shell git rev-parse --short HEAD)
DATE      ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

BUILD_DIR ?= out
DIST_DIR  ?= dist
TARBALL   := $(DIST_DIR)/$(NAME)-$(VERSION).tar.gz

### Build settings
GO ?= go

# Local environment
ifneq ($(wildcard env.mk),)
	include env.mk
endif

# Target platform. Defaults to the host, so a plain `make build` behaves as
# before; release builds set GOOS/GOARCH to cross-compile.
GOOS   := $(or $(GOOS),$(shell $(GO) env GOOS))
GOARCH := $(or $(GOARCH),$(shell $(GO) env GOARCH))
export GOOS GOARCH

# Windows needs the extension to run the file at all.
ifeq ($(GOOS),windows)
	EXE := .exe
endif
BINARY    := $(BUILD_DIR)/$(NAME)$(EXE)

# One archive per platform, in the format that platform's users expect.
STAGE_DIR := $(DIST_DIR)/$(NAME)-$(VERSION)-$(GOOS)-$(GOARCH)
ifeq ($(GOOS),windows)
	ARCHIVE := $(STAGE_DIR).zip
else
	ARCHIVE := $(STAGE_DIR).tar.gz
endif

# macOS ships shasum where Linux ships sha256sum.
SHA256 ?= $(shell command -v sha256sum >/dev/null 2>&1 && echo sha256sum || echo shasum -a 256)

ifeq ($(RELEASE),)
	DEV_VERSION = -dev-$(SHA)
endif

# Linker ldflags: stamp Version and Date into the meta package at build time.
# Path is module-prefixed so go-link resolves correctly.
LDFLAGS := -w -s -extldflags='-static' \
  -X '$(REPO)/meta.Version=$(VERSION)$(DEV_VERSION)' \
  -X '$(REPO)/meta.Date=$(DATE)'

.PHONY: all build build-dir install test vet fmt lint clean \
        version dist dist-dir dist-source dist-binary checksums

all: build

build-dir:
	test -d $(BUILD_DIR) || mkdir -p $(BUILD_DIR)

# -trimpath keeps build paths out of the binary, so two machines
# building one commit produce the same file.
build: build-dir
	$(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd/$(NAME)

install:
	$(GO) install -trimpath -ldflags="$(LDFLAGS)" ./cmd/$(NAME)

# -------- dev hygiene --------

PACKAGE = ./$(or $(pkg),...)

test:
	$(GO) test -v $(PACKAGE)

vet:
	$(GO) vet $(PACKAGE)

fmt:
	$(GO) fmt $(PACKAGE)

lint:
	golangci-lint run $(PACKAGE)

clean:
	rm -rf $(BUILD_DIR) $(DIST_DIR)

# -------- release packaging --------

# The version the build stamps in.
# Release tooling reads this to check the tag and the changelog agree.
version:
	@echo $(VERSION)

dist-dir:
	test -d $(DIST_DIR) || mkdir -p $(DIST_DIR)

# The source archive comes straight from git, so it holds exactly what
# is committed at this revision and nothing left over in the working tree.
dist-source: dist-dir
	git archive --format=tar.gz --prefix=$(NAME)-$(VERSION)/ -o $(TARBALL) HEAD

# One binary archive for the current GOOS/GOARCH,
# carrying the docs a user needs to make sense of the binary.
dist-binary: build dist-dir
	rm -rf $(STAGE_DIR)
	mkdir -p $(STAGE_DIR)
	cp $(BINARY) README.md LICENSE CHANGELOG.md $(STAGE_DIR)/
	cp -r doc $(STAGE_DIR)/doc
	rm -f $(ARCHIVE)
ifeq ($(GOOS),windows)
	cd $(DIST_DIR) && zip -q -r $(notdir $(ARCHIVE)) $(notdir $(STAGE_DIR))
else
	tar -czf $(ARCHIVE) -C $(DIST_DIR) $(notdir $(STAGE_DIR))
endif
	rm -rf $(STAGE_DIR)

dist: dist-source dist-binary

# SHA-256 over every archive present, so a download can be checked.
checksums:
	rm -f $(DIST_DIR)/SHA256SUMS
	cd $(DIST_DIR) && $(SHA256) $$(ls -1 | grep -E '\.(tar\.gz|zip)$$') > SHA256SUMS
