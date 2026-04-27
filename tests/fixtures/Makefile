SHELL := bash
ROOT  := $(shell cd ../.. && pwd)

GOBIN := $(shell go env GOBIN 2>/dev/null)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

REGISTRY_NAME     ?= imagetest-registry
REGISTRY_PORT     ?= 5005
REGISTRY_HOST     ?= localhost:$(REGISTRY_PORT)
TARGET_REPOSITORY ?= $(REGISTRY_HOST)/imagetest

TF_CLI_CONFIG_FILE := $(CURDIR)/.terraformrc.local
export TF_CLI_CONFIG_FILE

IMAGES       := jre nginx wolfi-base busybox go maven python ruby curl redis
TEST_TARGETS := $(addprefix test/,$(IMAGES))

.PHONY: help
help:
	@echo "Usage:"
	@echo "  make test/<name>          Run one fixture (e.g. make test/jre, make test/curl)"
	@echo "  make test                 Run all fixtures ($(words $(IMAGES)) total)"
	@echo "  make setup                Prepare deps (tools, registry, provider, terraformrc)"
	@echo "  make clean                Remove generated config and TF state; leave registry running"
	@echo "  make clean-all            Also stop the registry container"
	@echo ""
	@echo "Available fixtures:"
	@echo "  $(IMAGES)"
	@echo ""
	@echo "Overrides:"
	@echo "  REGISTRY_PORT     (default $(REGISTRY_PORT))"
	@echo "  TARGET_REPOSITORY (default $(TARGET_REPOSITORY))"

.PHONY: setup
setup: tools registry provider terraformrc

# Install any missing go-containerregistry tools (ko, crane) into $(GOBIN).
.PHONY: tools
tools:
	@command -v ko    >/dev/null 2>&1 || { echo ">> installing ko to $(GOBIN)"   ; go install github.com/google/ko@latest; }
	@command -v crane >/dev/null 2>&1 || { echo ">> installing crane to $(GOBIN)"; go install github.com/google/go-containerregistry/cmd/crane@latest; }

# Ephemeral local registry for the entrypoint bundle + sandbox/DinD helper images.
.PHONY: registry
registry:
	@if curl -sf http://$(REGISTRY_HOST)/v2/ >/dev/null 2>&1; then \
	  echo ">> registry already reachable at $(REGISTRY_HOST)"; \
	elif docker inspect $(REGISTRY_NAME) >/dev/null 2>&1; then \
	  echo ">> starting existing $(REGISTRY_NAME)"; \
	  docker start $(REGISTRY_NAME) >/dev/null; \
	else \
	  echo ">> starting $(REGISTRY_NAME) on $(REGISTRY_HOST)"; \
	  docker run -d -p $(REGISTRY_PORT):5000 --rm --name $(REGISTRY_NAME) registry:2 >/dev/null; \
	fi

# Build the provider from the current working tree into $(GOBIN) so dev_overrides picks it up.
.PHONY: provider
provider:
	@echo ">> go install . (from $(ROOT))"
	@cd $(ROOT) && go install .

# Scoped TF CLI config ($(TF_CLI_CONFIG_FILE)) pointing dev_overrides at the locally built provider.
.PHONY: terraformrc
terraformrc:
	@printf '%s\n' \
	  'provider_installation {' \
	  '  dev_overrides {' \
	  '    "registry.terraform.io/chainguard-dev/imagetest" = "$(GOBIN)"' \
	  '  }' \
	  '  direct {}' \
	  '}' > $(TF_CLI_CONFIG_FILE)

# Build the imagetest entrypoint image via ko and stash its pinned ref for the test targets.
.PHONY: entrypoint
entrypoint: registry tools
	@echo ">> ko build ./cmd/entrypoint → $(TARGET_REPOSITORY)"
	@cd $(ROOT) && KO_DOCKER_REPO=$(TARGET_REPOSITORY) ko build --base-import-paths ./cmd/entrypoint > $(CURDIR)/.entrypoint.ref

.PHONY: init
init: setup
	@terraform init -input=false

.PHONY: test $(TEST_TARGETS)

test: $(TEST_TARGETS)

# Static pattern rule: `make test/<name>` applies only that fixture's module against the live cgr.dev image.
$(TEST_TARGETS): test/%: setup entrypoint init
	@printf ">> applying module.%s (target_repository=%s)\n" "$*" "$(TARGET_REPOSITORY)"
	@IMAGETEST_ENTRYPOINT_REF=$$(cat .entrypoint.ref) \
	  terraform apply \
	    -target=module.$* \
	    -var=target_repository=$(TARGET_REPOSITORY) \
	    -auto-approve

.PHONY: clean
clean:
	-rm -rf .terraform .terraform.lock.hcl $(TF_CLI_CONFIG_FILE) .entrypoint.ref

.PHONY: clean-all
clean-all: clean
	-docker rm -f $(REGISTRY_NAME) 2>/dev/null
