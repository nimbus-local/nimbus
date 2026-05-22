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
# Usage: make pr
# Optional overrides: make pr TITLE="feat: override title" BODY="override body"

pr: _fmt-check _build _vet
	@echo "── Destroying existing environment..."
	$(INFRA) stop
	@echo "── Starting Nimbus (rebuilding image)..."
	$(INFRA) start
	@echo "── Provisioning resources..."
	$(INFRA) apply
	@echo "── Running smoke tests..."
	$(INFRA) smoke-test
	@echo "── All checks passed. Creating PR..."
	gh pr create --fill \
		$(if $(TITLE),--title "$(TITLE)") \
		$(if $(BODY),--body "$(BODY)")

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
