TERRAFORM      := terraform
TF_DIR         := terraform
DOCKER_COMPOSE := docker compose -f docker-compose.yml
AWS            := aws --endpoint-url http://localhost:4566 --region us-east-1 --no-cli-pager

export AWS_ACCESS_KEY_ID     ?= test
export AWS_SECRET_ACCESS_KEY ?= test
export AWS_DEFAULT_REGION    ?= us-east-1
export AWS_ENDPOINT_URL      ?= http://localhost:4566

.PHONY: start stop init plan apply destroy test smoke-test check-drift nuke reset

# ── Docker ──────────────────────────────────────────────────────────────────

start:
	$(DOCKER_COMPOSE) up -d --build
	@echo "Waiting for Nimbus to be healthy..."
	@until curl -sf http://localhost:4566/_nimbus/health >/dev/null; do sleep 1; done
	@echo "Nimbus is ready."

stop:
	$(DOCKER_COMPOSE) down -v

# ── Terraform ────────────────────────────────────────────────────────────────

init:
	cd $(TF_DIR) && $(TERRAFORM) init

plan: init
	cd $(TF_DIR) && $(TERRAFORM) plan

apply: init
	cd $(TF_DIR) && $(TERRAFORM) apply -auto-approve

destroy: init
	cd $(TF_DIR) && $(TERRAFORM) destroy -auto-approve

# ── Tests ────────────────────────────────────────────────────────────────────

smoke-test:
	./scripts/smoke-test.sh

# check-drift — fails when a freshly applied environment would still change on a
# re-apply. Guards a whole class of emulation bug: an attribute Nimbus accepts on
# create but never echoes on read shows up as perpetual Terraform drift, and when
# the attribute is ForceNew it silently recreates the resource on every apply.
#
# Must run before smoke-test, which deletes some Terraform-managed resources
# (the Kinesis stream, the CloudWatch alarm) and would make a plan dirty for
# reasons that are not drift.
check-drift: init
	@echo "── Checking the applied environment is idempotent..."
	@cd $(TF_DIR) && $(TERRAFORM) plan -detailed-exitcode -input=false >/dev/null 2>&1; \
	status=$$?; \
	if [ $$status -eq 0 ]; then \
		echo "✓ no drift — terraform plan is clean"; \
	elif [ $$status -eq 2 ]; then \
		echo "✗ drift: terraform wants changes right after a fresh apply:"; \
		$(TERRAFORM) plan -input=false -no-color | grep -E '^  # ' || true; \
		exit 1; \
	else \
		echo "✗ terraform plan failed"; \
		$(TERRAFORM) plan -input=false -no-color | tail -20; \
		exit 1; \
	fi

# Full clean + provision + smoke tests. Always starts from a clean slate.
test: nuke smoke-test

# Tear down + stop containers
clean: destroy stop

# ── Full reset (no restart required) ─────────────────────────────────────────

# reset — clears all Nimbus in-memory state without restarting containers.
# Also deletes the Terraform state file and cleans up S3/DynamoDB resources
# that Nimbus can't clear internally (S3 volume, DynamoDB Local sidecar).
# After reset, run 'make apply' to provision a fresh environment.
reset:
	@echo "=== Resetting Nimbus in-memory state ==="
	@curl -sf -X POST http://localhost:4566/_nimbus/reset || true
	@echo "=== Clearing S3 data (persisted to volume) ==="
	@$(AWS) s3 ls 2>/dev/null | awk '{print $$3}' | xargs -I{} $(AWS) s3 rb s3://{} --force 2>/dev/null || true
	@echo "=== Clearing DynamoDB tables (separate sidecar) ==="
	@$(AWS) dynamodb list-tables --query 'TableNames[]' --output text 2>/dev/null \
		| tr '\t' '\n' \
		| xargs -I{} $(AWS) dynamodb delete-table --table-name {} 2>/dev/null || true
	@echo "=== Clearing Terraform state ==="
	@rm -f terraform/terraform.tfstate terraform/terraform.tfstate.backup
	@echo "=== Done. Run 'make apply' to provision a fresh environment. ==="

# nuke — full reset then re-provision. Use this before running smoke tests
# to guarantee a completely clean environment.
nuke: start reset apply
