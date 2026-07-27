INFRA := $(MAKE) -C infra

.PHONY: setup build fmt vet pr _branch-check _fmt-check _build _vet

# ── Setup ─────────────────────────────────────────────────────────────────────

# Wire up the committed git hooks. Run once after cloning.
setup:
	git config core.hooksPath .githooks

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

pr: _branch-check _fmt-check _build _vet
	@echo "── Destroying existing environment..."
	$(INFRA) stop
	@echo "── Starting Nimbus (rebuilding image)..."
	$(INFRA) start
	@echo "── Provisioning resources..."
	$(INFRA) apply
	@echo "── Checking Terraform idempotency..."
	$(INFRA) check-drift
	@echo "── Running smoke tests..."
	$(INFRA) smoke-test
	@echo "── All checks passed. Pushing branch..."
	git push -u origin HEAD
	@echo "── Creating PR..."
	gh pr create --fill \
		$(if $(TITLE),--title "$(TITLE)") \
		$(if $(BODY),--body "$(BODY)")

# ── Internal check targets ────────────────────────────────────────────────────

# Guards the push in `pr`: a feature branch is required so the target can never
# push straight to the default branch.
_branch-check:
	@echo "Checking branch..."
	@branch=$$(git rev-parse --abbrev-ref HEAD); \
	if [ "$$branch" = "master" ] || [ "$$branch" = "main" ] || [ "$$branch" = "HEAD" ]; then \
		echo "✗ 'make pr' needs a feature branch (on: $$branch)"; \
		exit 1; \
	fi; \
	echo "✓ on branch $$branch"

_fmt-check:
	@echo "Checking gofmt..."
	@unformatted=$$(gofmt -l .) || exit 1; \
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
