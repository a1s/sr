REPO      := github.com/a1s/sr
NAME      := sr
VERSION   ?= $(shell grep -E '^## \[' CHANGELOG.md | head -1 | sed -e 's/^## \[//' -e 's/\].*$$//')
SHA       ?= $(shell git rev-parse --short HEAD)
DATE      ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

BUILD_DIR ?= out
DIST_DIR  ?= dist
TARBALL   := $(DIST_DIR)/$(NAME)-$(VERSION).tar.gz

ifeq ($(RELEASE),)
	DEV_VERSION = -dev-$(SHA)
endif

### Build settings
GO ?= go

# Local environment
ifneq ($(wildcard env.mk),)
	include env.mk
endif

# Linker ldflags: stamp Version and Date into the meta package at build time.
# Path is module-prefixed so go-link resolves correctly.
LDFLAGS := -w -s -extldflags='-static' \
  -X '$(REPO)/meta.Version=$(VERSION)$(DEV_VERSION)' \
  -X '$(REPO)/meta.Date=$(DATE)'

.PHONY: test vet fmt lint

all: build

build-dir:
	test -d $(BUILD_DIR) || mkdir -p $(BUILD_DIR)

build:

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
