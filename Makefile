INFRA := $(MAKE) -C infra

.PHONY: build fmt vet pr _fmt-check _build _vet

# ── Go ────────────────────────────────────────────────────────────────────────

build:
	go build ./...

fmt:
	gofmt -w ./...

vet:
	go vet ./...

# ── PR gate ───────────────────────────────────────────────────────────────────
# Runs the full clean-build-test cycle before creating the PR.
# Usage: make pr TITLE="feat: my change" BODY_FILE=/tmp/pr-body.md

pr: _fmt-check _build _vet
ifndef TITLE
	$(error TITLE is required. Usage: make pr TITLE="..." BODY_FILE=/tmp/body.md)
endif
	@echo "── Destroying existing environment..."
	$(INFRA) stop
	@echo "── Starting Nimbus (rebuilding image)..."
	$(INFRA) start
	@echo "── Provisioning resources..."
	$(INFRA) apply
	@echo "── Running smoke tests..."
	$(INFRA) smoke-test
	@echo "── All checks passed. Creating PR..."
	gh pr create --title "$(TITLE)" $(if $(BODY_FILE),--body-file "$(BODY_FILE)")

# ── Internal check targets ────────────────────────────────────────────────────

_fmt-check:
	@echo "Checking gofmt..."
	@unformatted=$$(gofmt -l ./...); \
	if [ -n "$$unformatted" ]; then \
		echo "✗ gofmt: these files need formatting:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi
	@echo "✓ gofmt clean"

_build:
	@echo "Building..."
	@go build ./... && echo "✓ build clean"

_vet:
	@echo "Running go vet..."
	@go vet ./... && echo "✓ vet clean"
