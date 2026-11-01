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

# Target platform. Defaults to the host, so a plain `make build`
# behaves as before; release builds set GOOS/GOARCH to cross-compile.
# Only the targets that name a file after the platform
# ask Go for it, so the others run on a machine without Go.
# Go reads an empty GOOS or GOARCH as unset.
ifneq ($(filter all build dist dist-binary,$(or $(MAKECMDGOALS),all)),)
GOOS   := $(or $(GOOS),$(shell $(GO) env GOOS))
GOARCH := $(or $(GOARCH),$(shell $(GO) env GOARCH))
endif
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

.PHONY: all build build-dir install test vet fmt clean \
        lint lint-code lint-actions audit audit-packages audit-actions \
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

# Found on PATH, like golangci-lint.
# .github/zizmor/requirements.txt holds the version CI installs.
ZIZMOR      ?= zizmor
GOVULNCHECK ?= govulncheck

# Each platform the release ships a binary for, as GOOS/GOARCH.
# The build matrix in release.yml lists the same ones.
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 \
             windows/amd64 windows/arm64

# Their operating systems.  Font enumeration compiles a file of its own
# on each, and no file in the tree is chosen by architecture.
PLATFORM_OSES := $(sort $(foreach platform,$(PLATFORMS),\
                   $(firstword $(subst /, ,$(platform)))))

# The Go code and the workflows, each half reporting even when the other
# fails.  `pkg` narrows the Go half to one package, and the workflows are
# not one, so zizmor runs only when it is unset.  Without zizmor installed,
# `make lint-code` runs the Go half alone.
lint:
	@$(MAKE) --no-print-directory -k lint-code $(if $(pkg),,lint-actions)

# golangci-lint reads .golangci.yml, which adds gosec to its standard set.
# It reads only the files its GOOS selects, so each one gets a run.
lint-code:
	@status=0; for os in $(PLATFORM_OSES); do \
	    echo "golangci-lint: GOOS=$$os"; \
	    GOOS=$$os golangci-lint run $(PACKAGE) || status=1; \
	done; exit $$status

# zizmor reads the workflows and the Dependabot config.
# Offline, because its online audits ask GitHub about advisories and commits,
# and their answers change while the commit under test does not.
# `make audit-actions` runs them.
lint-actions:
	$(ZIZMOR) --offline .

clean:
	rm -rf $(BUILD_DIR) $(DIST_DIR)

# -------- known vulnerabilities --------

# Known vulnerabilities, in the modules and in the actions.
# Not part of `lint`, and not run by test.yml:
# the advisories change from day to day,
# so their answer for one commit does too.
# audit.yml runs them apart from the checks a release waits for.
# Each half reports even when the other fails, and
# `pkg` leaves out the actions, as it does for `lint`.
audit:
	@$(MAKE) --no-print-directory -k audit-packages $(if $(pkg),,audit-actions)

# govulncheck reports a vulnerability only where the code can reach it.
# Font enumeration compiles a file of its own on each operating system,
# and the standard library has code of its own for each architecture,
# so every platform the release ships a binary for gets a run.
# Each one reports, rather than the first failure hiding the rest.
audit-packages:
	@status=0; for platform in $(PLATFORMS); do \
	    echo "govulncheck: $$platform"; \
	    GOOS=$${platform%/*} GOARCH=$${platform#*/} \
	        $(GOVULNCHECK) $(PACKAGE) || status=1; \
	done; exit $$status

# The actions the workflows pin, through the zizmor audits `lint` leaves out:
# a pin to a commit that is not in the action's own repository,
# a version with an advisory, and a ref that is both a branch and a tag.
# They ask GitHub, which wants a token in GH_TOKEN,
# and without one zizmor quietly runs offline and passes,
# so the token is checked first.
audit-actions:
	@test -n "$$GH_TOKEN$$GITHUB_TOKEN$$ZIZMOR_GITHUB_TOKEN" \
	    || { echo "audit-actions: set GH_TOKEN to a GitHub token" >&2; exit 1; }
	$(ZIZMOR) --cache-dir $(BUILD_DIR)/zizmor .

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
