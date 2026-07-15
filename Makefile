BOLD := \033[1m
BLUE := \033[34m
RESET := \033[0m

TOOLS_DIR ?= $(CURDIR)/.local/tools
BAZEL_OUTPUT_USER_ROOT ?= $(CURDIR)/.local/bazel
BAZELISK_HOME ?= $(CURDIR)/.local/bazelisk
BAZEL ?= $(TOOLS_DIR)/bin/bazelisk --output_user_root=$(BAZEL_OUTPUT_USER_ROOT)
BAZEL_BUILD_FLAGS ?=
BAZEL_TEST_FLAGS ?=
BAZEL_UNIT_TEST_FLAGS ?= --test_tag_filters=-integration,-conformance,-mutation,-lint,-manual,-external
BAZEL_BUILD_TARGETS ?= //...
BAZEL_TEST_TARGETS ?= //:test
BAZEL_INTEGRATION_TEST_TARGETS ?= //:integration
BAZEL_VM_INTEGRATION_TEST_TARGETS ?= //tools/scripts:kernel_vm_two_guest_test
BAZEL_VM_RESOURCE_FLAGS ?= --jobs=1 --local_resources=cpu=1 --local_resources=memory=1024
BAZEL_VM_STARTUP_FLAGS ?= --host_jvm_args=-Xmx512m
BAZEL_VM_TEST_TIMEOUT_SECONDS ?= 2400
BAZEL_CONFORMANCE_TEST_TARGETS ?= //:conformance
BAZEL_COVERAGE_TARGETS ?= //:test
BAZEL_DIST_TARGETS ?= //cmd/usbip-go:usbip-go //test/integration/killable:killable
CODEQL_BUILD_OUTPUT ?= $(CURDIR)/build/codeql/usbip-go
CODEQL_BUILD_PACKAGE ?= ./cmd/usbip-go
CODEQL_CACHE_ROOT ?= $(CURDIR)/.local/codeql
CODEQL_GOCACHE ?= $(CODEQL_CACHE_ROOT)/go-build
CODEQL_GOMODCACHE ?= $(CODEQL_CACHE_ROOT)/go-mod
CODEQL_GOTMPDIR ?= $(CODEQL_CACHE_ROOT)/tmp
GO_VENDOR_CACHE_ROOT ?= $(CURDIR)/.local/go-vendor
GO_VENDOR_GOCACHE ?= $(GO_VENDOR_CACHE_ROOT)/go-build
GO_VENDOR_GOMODCACHE ?= $(GO_VENDOR_CACHE_ROOT)/go-mod
GO_VENDOR_GOTMPDIR ?= $(GO_VENDOR_CACHE_ROOT)/tmp
REPO_GO ?= $(TOOLS_DIR)/go/bin/go
CODEQL_GO ?= $(REPO_GO)
KERNEL_VM_CACHE_ROOT ?= $(CURDIR)/.local/kernel-vm

export BAZELISK_HOME

CI_LOCAL_ENV = BAZEL='$(BAZEL)' BAZEL_BUILD_FLAGS='$(BAZEL_BUILD_FLAGS)' BAZEL_TEST_FLAGS='$(BAZEL_TEST_FLAGS)' BAZEL_UNIT_TEST_FLAGS='$(BAZEL_UNIT_TEST_FLAGS)' BAZEL_BUILD_TARGETS='$(BAZEL_BUILD_TARGETS)' BAZEL_TEST_TARGETS='$(BAZEL_TEST_TARGETS)' BAZEL_CONFORMANCE_TEST_TARGETS='$(BAZEL_CONFORMANCE_TEST_TARGETS)' BAZEL_COVERAGE_TARGETS='$(BAZEL_COVERAGE_TARGETS)'

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show available targets
	@awk 'BEGIN { printf "$(BOLD)Targets$(RESET)\n" } /^##@ / { printf "\n$(BOLD)%s$(RESET)\n", substr($$0, 5); next } /^## / { desc = substr($$0, 4); next } /^[[:alnum:]_%-]+:/ && desc { target = $$1; sub(/:.*/, "", target); printf "  $(BLUE)%-32s$(RESET) %s\n", target, desc; desc = "" }' $(MAKEFILE_LIST)

##@ Normal targets

## Build, test, and lint
.PHONY: all
all: build lint test

## Install repo-local Go and Bazelisk
.PHONY: bootstrap
bootstrap:
	@HARNESS_TOOLS_DIR="$(TOOLS_DIR)" HARNESS_BAZELISK_HOME="$(BAZELISK_HOME)" HARNESS_BAZEL_OUTPUT_USER_ROOT="$(BAZEL_OUTPUT_USER_ROOT)" tools/scripts/bootstrap.sh

## Build Bazel targets; override BAZEL_BUILD_TARGETS to narrow the set
.PHONY: build
build: bootstrap
	$(BAZEL) build $(BAZEL_BUILD_FLAGS) $(BAZEL_BUILD_TARGETS)

## Build the production binary for focused CodeQL tracing
.PHONY: build-codeql
build-codeql: bootstrap
	@mkdir -p "$(dir $(CODEQL_BUILD_OUTPUT))" "$(CODEQL_GOCACHE)" "$(CODEQL_GOMODCACHE)" "$(CODEQL_GOTMPDIR)"
	CGO_ENABLED=0 GOCACHE="$(CODEQL_GOCACHE)" GOENV=off GOFLAGS= GOMODCACHE="$(CODEQL_GOMODCACHE)" GOTOOLCHAIN=local GOTMPDIR="$(CODEQL_GOTMPDIR)" GOWORK=off $(CODEQL_GO) build -mod=vendor -trimpath -o "$(CODEQL_BUILD_OUTPUT)" $(CODEQL_BUILD_PACKAGE)

## Generate changelog output
.PHONY: changelog
changelog: bootstrap
	$(BAZEL) run //:changelog

## Check exported API compatibility against baselines
.PHONY: check-api-compatibility
check-api-compatibility: bootstrap
	$(BAZEL) test $(BAZEL_TEST_FLAGS) //:api_compatibility

## Cross-compile Linux release binary targets
.PHONY: check-cross-compile
check-cross-compile: bootstrap
	$(BAZEL) build --config=linux-amd64 $(BAZEL_BUILD_FLAGS) //cmd/usbip-go:usbip-go
	$(BAZEL) build --config=linux-arm64 $(BAZEL_BUILD_FLAGS) //cmd/usbip-go:usbip-go
	$(BAZEL) build --config=linux-arm $(BAZEL_BUILD_FLAGS) //cmd/usbip-go:usbip-go

## Enforce domain import boundaries
.PHONY: check-domain-boundaries
check-domain-boundaries: bootstrap
	$(BAZEL) test $(BAZEL_TEST_FLAGS) //:domain_boundary

## Verify that go.mod and go.sum are tidy and internally consistent
.PHONY: check-go-mod
check-go-mod: bootstrap
	$(BAZEL) test $(BAZEL_TEST_FLAGS) //:go_mod_check

## Enforce pure-Go source policy
.PHONY: check-pure-go
check-pure-go: bootstrap
	$(BAZEL) test $(BAZEL_TEST_FLAGS) //:pure_go

## Verify production Bazel release stamping with deterministic workspace status
.PHONY: check-release-stamping
check-release-stamping: bootstrap
	$(BAZEL) test --config=release --workspace_status_command=tools/scripts/release_workspace_status_fixture.sh $(BAZEL_TEST_FLAGS) //test/release:release_stamping_test

## Run the GitHub CI pipeline locally
.PHONY: ci-local
ci-local: bootstrap
	$(BAZEL) build $(BAZEL_BUILD_FLAGS) //:ci-local
	$(CI_LOCAL_ENV) .local/bazel-links/bin/tools/scripts/ci_local_runner

## Clean Bazel outputs
.PHONY: clean
clean:
	$(BAZEL) clean

## Clean Bazel outputs and repo-local tool/cache state
.PHONY: clean-all
clean-all: clean
	rm -rf "$(CURDIR)/.local"

## Build release/dist targets; override BAZEL_DIST_TARGETS to narrow the set
.PHONY: dist
dist: bootstrap
	$(BAZEL) build --config=release $$(tools/scripts/package_version_flags.sh) $(BAZEL_BUILD_FLAGS) $(BAZEL_DIST_TARGETS)

## Format source files
.PHONY: format
format: bootstrap
	$(BAZEL) run //:format

## Scan the complete Git history for leaked secrets
.PHONY: gitleaks-history
gitleaks-history: bootstrap
	$(BAZEL) run @multitool//tools/gitleaks -- detect --config="$(CURDIR)/.gitleaks.toml" --source="$(CURDIR)"

## Run Go vulnerability check
.PHONY: govulncheck
govulncheck: bootstrap
	$(BAZEL) test $(BAZEL_TEST_FLAGS) //:govulncheck

## Write govulncheck SARIF for code scanning
.PHONY: govulncheck-sarif
govulncheck-sarif: bootstrap
	$(BAZEL) run $(BAZEL_BUILD_FLAGS) //:govulncheck_sarif -- build/govulncheck.sarif -test ./...

## Run lint tests
.PHONY: lint
lint: bootstrap
	$(BAZEL) test $(BAZEL_TEST_FLAGS) //:lint

## Publish a tagged release with Bazel-provisioned GoReleaser
.PHONY: release
release: bootstrap
	$(BAZEL) run $(BAZEL_BUILD_FLAGS) //:release

## Validate GoReleaser configuration
.PHONY: release-check
release-check: bootstrap
	$(BAZEL) run $(BAZEL_BUILD_FLAGS) //:release-check

## Build a local snapshot release with Bazel-provisioned GoReleaser
.PHONY: release-snapshot
release-snapshot: bootstrap
	$(BAZEL) run $(BAZEL_BUILD_FLAGS) //:release-snapshot

## Validate, create, and hand off a manual GitHub release tag
.PHONY: start-release
start-release:
	@tools/scripts/start_release.sh

## Run tests; override BAZEL_TEST_TARGETS to narrow the set
.PHONY: test
test: bootstrap
	$(BAZEL) test $(BAZEL_UNIT_TEST_FLAGS) $(BAZEL_TEST_FLAGS) $(BAZEL_TEST_TARGETS)

## Run all configured quality gates
.PHONY: test-all
test-all: test test-conformance test-coverage test-integration test-race lint govulncheck

## Run conformance tests tagged conformance
.PHONY: test-conformance
test-conformance: bootstrap
	$(BAZEL) test --config=conformance $(BAZEL_TEST_FLAGS) $(BAZEL_CONFORMANCE_TEST_TARGETS)

## Run coverage and enforce configured thresholds
.PHONY: test-coverage
test-coverage: bootstrap
	$(BAZEL) coverage --combined_report=lcov $(BAZEL_UNIT_TEST_FLAGS) $(BAZEL_TEST_FLAGS) $(BAZEL_COVERAGE_TARGETS)
	@mkdir -p build/coverage; coverage_report="$$( $(BAZEL) info output_path )/_coverage/_coverage_report.dat"; rm -f build/coverage/coverage.lcov; cp "$${coverage_report}" build/coverage/coverage.lcov
	$(BAZEL) run $(BAZEL_BUILD_FLAGS) //tools/scripts:coverage_check -- build/coverage/coverage.lcov .testcoverage.yaml

## Run integration tests tagged integration
.PHONY: test-integration
test-integration: bootstrap
	$(BAZEL) test --config=integration $(BAZEL_TEST_FLAGS) $(BAZEL_INTEGRATION_TEST_TARGETS)

## Run the two-guest KVM USB/IP resilience test
.PHONY: test-integration-vm
test-integration-vm: bootstrap
	$(BAZEL) $(BAZEL_VM_STARTUP_FLAGS) test --config=integration --test_env=KERNEL_VM_CACHE_ROOT="$(KERNEL_VM_CACHE_ROOT)" --test_env=KERNEL_VM_WORKSPACE_ROOT="$(CURDIR)" $(BAZEL_TEST_FLAGS) $(BAZEL_VM_RESOURCE_FLAGS) --nocache_test_results --test_timeout=$(BAZEL_VM_TEST_TIMEOUT_SECONDS) $(BAZEL_VM_INTEGRATION_TEST_TARGETS)

## Run mutation tests
.PHONY: test-mutation
test-mutation: bootstrap
	$(BAZEL) test $(BAZEL_TEST_FLAGS) //:mutations

## Run unit tests with Go's race detector
.PHONY: test-race
test-race: bootstrap
	$(BAZEL) test --config=race $(BAZEL_UNIT_TEST_FLAGS) $(BAZEL_TEST_FLAGS) $(BAZEL_TEST_TARGETS)

## Regenerate the pinned Bazel module lock file
.PHONY: update-bazel-lock
update-bazel-lock: bootstrap
	$(BAZEL) mod tidy --lockfile_mode=update

## Regenerate vendored Go dependencies for hermetic linting
.PHONY: update-go-vendor
update-go-vendor: bootstrap
	@mkdir -p "$(GO_VENDOR_GOCACHE)" "$(GO_VENDOR_GOMODCACHE)" "$(GO_VENDOR_GOTMPDIR)"
	GOCACHE="$(GO_VENDOR_GOCACHE)" GOENV=off GOFLAGS= GOMODCACHE="$(GO_VENDOR_GOMODCACHE)" GOTOOLCHAIN=local GOTMPDIR="$(GO_VENDOR_GOTMPDIR)" GOWORK=off $(REPO_GO) mod vendor
