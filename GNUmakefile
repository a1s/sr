REPO      := github.com/a1s/sr
NAME      := sr
VERSION   ?= $(shell grep -E '^## \[' CHANGELOG.md | head -1 | sed -e 's/^## \[//' -e 's/\].*$$//')
SHA       ?= $(shell git rev-parse --short HEAD)
DATE      ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

BUILD_DIR ?= out
DIST_DIR  ?= dist
TARBALL   := $(DIST_DIR)/$(NAME)-$(VERSION)-source.tar.gz

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

# Reproducible archives need GNU tar 1.28 or newer, for --sort and
# --mtime.  macOS ships bsdtar as tar, so set TAR=gtar there.
TAR ?= tar

# Flags that leave GNU tar output depending on the file contents alone:
# a fixed entry order, no owner names, and one pinned timestamp.
TAR_REPRO := --sort=name --owner=0 --group=0 --numeric-owner --mtime='$(DATE)'

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
#
# Everything that would otherwise vary between runs is pinned:
# the staged files take DATE for their mtime, the entries go in
# sorted order, and gzip -n keeps its own timestamp out of the header.
# Two runs of one tag then produce byte-identical archives.
dist-binary: build dist-dir
	@$(TAR) --version | head -1 | grep -q 'GNU tar' \
		|| { echo 'dist-binary needs GNU tar 1.28+; set TAR=gtar' >&2; exit 1; }
	rm -rf $(STAGE_DIR)
	mkdir -p $(STAGE_DIR)
	cp $(BINARY) README.md LICENSE CHANGELOG.md $(STAGE_DIR)/
	cp -r doc $(STAGE_DIR)/doc
	find $(STAGE_DIR) -exec touch -d '$(DATE)' {} +
	rm -f $(ARCHIVE)
ifeq ($(GOOS),windows)
	cd $(DIST_DIR) && find $(notdir $(STAGE_DIR)) | sort | zip -q -X -@ $(notdir $(ARCHIVE))
else
	$(TAR) $(TAR_REPRO) -C $(DIST_DIR) -cf - $(notdir $(STAGE_DIR)) | gzip -n > $(ARCHIVE)
endif
	rm -rf $(STAGE_DIR)

dist: dist-source dist-binary

# SHA-256 over every archive present, so a download can be checked.
#
# grep exiting 1 on an empty dist breaks the && chain and fails the
# recipe, rather than leaving sha256sum to read stdin and hash nothing.
checksums:
	cd $(DIST_DIR) && archives=$$(ls -1 | grep -E '\.(tar\.gz|zip)$$') \
		&& $(SHA256) $$archives > SHA256SUMS
