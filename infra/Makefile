TERRAFORM      := terraform
TF_DIR         := terraform
DOCKER_COMPOSE := docker compose -f docker-compose.yml
AWS            := aws --endpoint-url http://localhost:4566 --region us-east-1 --no-cli-pager

.PHONY: start stop init plan apply destroy test smoke-test

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

plan:
	cd $(TF_DIR) && $(TERRAFORM) plan

apply:
	cd $(TF_DIR) && $(TERRAFORM) apply -auto-approve

destroy:
	cd $(TF_DIR) && $(TERRAFORM) destroy -auto-approve

# ── Tests ────────────────────────────────────────────────────────────────────

smoke-test:
	./scripts/smoke-test.sh

# Provision everything then run smoke tests
test: apply smoke-test

# Tear down + stop containers
clean: destroy stop
