NIMBUS_ENDPOINT ?= http://localhost:4566
TF_DIR         := infra/terraform
SMOKE_SCRIPT   := infra/scripts/smoke-test.sh

.PHONY: start stop build fmt vet apply smoke-test pr _fmt-check _build _vet _nimbus-running _smoke-test

# ── Dev lifecycle ─────────────────────────────────────────────────────────────

start:
	docker compose up --build -d
	@echo "Waiting for Nimbus to be healthy..."
	@until curl -sf $(NIMBUS_ENDPOINT)/_nimbus/health > /dev/null; do sleep 2; done
	@echo "Nimbus is ready."

stop:
	docker compose down

build:
	go build ./...

fmt:
	gofmt -w ./...

vet:
	go vet ./...

# ── Terraform ─────────────────────────────────────────────────────────────────

apply:
	cd $(TF_DIR) && terraform apply -auto-approve

# ── Smoke test ────────────────────────────────────────────────────────────────

smoke-test:
	AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test \
	NIMBUS_ENDPOINT=$(NIMBUS_ENDPOINT) \
	bash $(SMOKE_SCRIPT)

# ── PR gate ───────────────────────────────────────────────────────────────────
# Usage: make pr TITLE="feat: my change" BODY_FILE=/tmp/pr-body.md

pr: _fmt-check _build _vet _nimbus-running _smoke-test
ifndef TITLE
	$(error TITLE is required. Usage: make pr TITLE="..." BODY_FILE=/tmp/body.md)
endif
	gh pr create --title "$(TITLE)" $(if $(BODY_FILE),--body-file "$(BODY_FILE)")
	@echo "PR created."

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

_nimbus-running:
	@echo "Checking Nimbus health..."
	@curl -sf $(NIMBUS_ENDPOINT)/_nimbus/health > /dev/null || \
		(echo "✗ Nimbus is not running at $(NIMBUS_ENDPOINT). Run 'make start' first."; exit 1)
	@echo "✓ Nimbus is running"

_smoke-test:
	@echo "Running smoke tests..."
	@AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test \
	NIMBUS_ENDPOINT=$(NIMBUS_ENDPOINT) \
	bash $(SMOKE_SCRIPT)
