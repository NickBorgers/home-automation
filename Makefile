.DEFAULT_GOAL := help

help:
	@awk 'BEGIN {FS = ":.*##"; printf "\n"} /^##@/ { printf "\n\033[1m━━━ %s ━━━\033[0m\n", substr($$0, 5) } /^[a-zA-Z0-9_-]+:.*##/ { printf "  \033[36m%-30s\033[0m%s\n", $$1, $$2 }' $(MAKEFILE_LIST)
	@echo ""

##@ Config

run-config-tests: run-yamllint-hue run-yamllint-music run-yamllint-energy run-yamllint-schedule run-yamllint-sensor run-spotify-validation-music ## Run all available tests of the configuration files (Docker)

yamllint-local: ## Run yamllint on all config files natively (no Docker, requires yamllint installed)
	@echo "🔍 Running yamllint on config files..."
	@for f in configs/*.yaml; do \
		yamllint $$f && echo "  ✅ $$(basename $$f) passed"; \
	done
	@echo "✅ All config files passed yamllint"

run-yamllint-hue: build-config-tester
	docker run --rm --mount type=bind,source=${CURDIR}/.yamllint,destination=/app/.yamllint --mount type=bind,source=${CURDIR}/configs/hue_config.yaml,destination=/app/hue_config.yaml node-red-config-tester yamllint hue_config.yaml

run-yamllint-music: build-config-tester
	docker run --rm --mount type=bind,source=${CURDIR}/.yamllint,destination=/app/.yamllint --mount type=bind,source=${CURDIR}/configs/music_config.yaml,destination=/app/music_config.yaml node-red-config-tester yamllint music_config.yaml

run-yamllint-energy: build-config-tester
	docker run --rm --mount type=bind,source=${CURDIR}/.yamllint,destination=/app/.yamllint --mount type=bind,source=${CURDIR}/configs/energy_config.yaml,destination=/app/energy_config.yaml node-red-config-tester yamllint energy_config.yaml

run-yamllint-schedule: build-config-tester
	docker run --rm --mount type=bind,source=${CURDIR}/.yamllint,destination=/app/.yamllint --mount type=bind,source=${CURDIR}/configs/schedule_config.yaml,destination=/app/schedule_config.yaml node-red-config-tester yamllint schedule_config.yaml

run-yamllint-sensor: build-config-tester
	docker run --rm --mount type=bind,source=${CURDIR}/.yamllint,destination=/app/.yamllint --mount type=bind,source=${CURDIR}/configs/sensor_config.yaml,destination=/app/sensor_config.yaml node-red-config-tester yamllint sensor_config.yaml

run-spotify-validation-music: build-config-tester
	docker run --rm --mount type=bind,source=${CURDIR}/configs/music_config.yaml,destination=/app/music_config.yaml node-red-config-tester python3 -u validate_spotify_uris.py

build-config-tester:
	docker build -t node-red-config-tester ./config-test/

# ============================================================================
# Documentation Validation Targets
# ============================================================================

##@ Documentation

validate-mermaid: ## Validate all Mermaid diagrams in documentation
	@echo "🔍 Validating Mermaid diagrams..."
	@echo ""
	@rm -rf .mermaid-tmp && mkdir -p .mermaid-tmp && chmod 777 .mermaid-tmp
	@# Extract mermaid blocks from all documentation files containing diagrams
	@diagram_num=0; \
	  for mdfile in docs/human/VISUAL_ARCHITECTURE.md docs/flows/*.md; do \
	    if [ -f "$$mdfile" ]; then \
	      basename=$$(basename $$mdfile .md); \
	      awk -v prefix="$$basename" -v num=$$diagram_num '/^```mermaid$$/,/^```$$/' "$$mdfile" | \
	        awk -v prefix="$$basename" -v start=$$diagram_num 'BEGIN{n=start} /^```mermaid$$/{n++;f=".mermaid-tmp/"prefix"-diagram-"n".mmd";next} /^```$$/{close(f);next} {print > f}'; \
	      new_count=$$(ls -1 .mermaid-tmp/*.mmd 2>/dev/null | wc -l); \
	      diagram_num=$$new_count; \
	    fi; \
	  done
	@diagram_count=$$(ls -1 .mermaid-tmp/*.mmd 2>/dev/null | wc -l); \
	  echo "Found $$diagram_count Mermaid diagrams to validate"; \
	  if [ "$$diagram_count" -eq 0 ]; then \
	    echo "⚠️  No diagrams found"; \
	    rm -rf .mermaid-tmp; \
	    exit 0; \
	  fi
	@failed=0; \
	  for f in .mermaid-tmp/*.mmd; do \
	    name=$$(basename $$f); \
	    echo -n "  Validating $$name... "; \
	    if docker run --rm --user $$(id -u):$$(id -g) -v "$${PWD}/.mermaid-tmp:/data" minlag/mermaid-cli:latest \
	      -i /data/$$name -o /data/$${name%.mmd}.png -q 2>/dev/null; then \
	      echo "✅"; \
	    else \
	      echo "❌ FAILED"; \
	      echo "    Error in diagram $$name:"; \
	      docker run --rm --user $$(id -u):$$(id -g) -v "$${PWD}/.mermaid-tmp:/data" minlag/mermaid-cli:latest \
	        -i /data/$$name -o /data/$${name%.mmd}.png 2>&1 | grep -E "(Error|error|Parse)" | head -5 | sed 's/^/    /'; \
	      failed=1; \
	    fi; \
	  done; \
	  rm -rf .mermaid-tmp; \
	  if [ "$$failed" -eq 1 ]; then \
	    echo ""; \
	    echo "❌ Some Mermaid diagrams failed validation"; \
	    exit 1; \
	  fi
	@echo ""
	@echo "✅ All Mermaid diagrams validated successfully"

validate-docs: validate-mermaid ## Validate all documentation (Mermaid diagrams, etc.)
	@echo ""
	@echo "✅ All documentation validation passed"

# ============================================================================
# Generated Diagram Targets
# ============================================================================

generate-diagrams: ## Generate plugin dependency diagrams from source code
	@echo "🔍 Generating diagrams from plugin source code..."
	@cd homeautomation-go && go run ./cmd/diagramgen/...
	@echo "✅ Diagrams generated successfully"

validate-diagrams: ## Validate that generated diagrams are up-to-date with source code
	@echo "🔍 Validating generated diagrams are up-to-date..."
	@$(MAKE) generate-diagrams
	@if ! git diff --quiet docs/generated/; then \
		echo ""; \
		echo "❌ ERROR: Generated diagrams are out of date!"; \
		echo ""; \
		echo "The following files need to be regenerated:"; \
		git diff --name-only docs/generated/; \
		echo ""; \
		echo "Run 'make generate-diagrams' and commit the changes."; \
		echo ""; \
		git diff docs/generated/; \
		exit 1; \
	fi
	@echo "✅ Generated diagrams are up-to-date"

# ============================================================================
# Go Application (homeautomation-go) Targets
# ============================================================================

# Version info for build-time injection
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
GIT_BRANCH := $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")
GIT_DIRTY := $(shell git diff --quiet 2>/dev/null && echo "false" || echo "true")
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
VERSION_PKG := homeautomation/internal/version
LDFLAGS := -X '$(VERSION_PKG).GitCommit=$(GIT_COMMIT)' \
           -X '$(VERSION_PKG).GitBranch=$(GIT_BRANCH)' \
           -X '$(VERSION_PKG).GitDirty=$(GIT_DIRTY)' \
           -X '$(VERSION_PKG).BuildTime=$(BUILD_TIME)'

##@ Build & Run

build-go: ## Build the Go application binary
	cd homeautomation-go && go build -ldflags "$(LDFLAGS)" -o homeautomation ./cmd/main.go

dev-ui: build-go ## Run application with mock HA server for local UI development
	@echo ""
	@echo "════════════════════════════════════════════════════════════════════════════"
	@echo "🚀 Starting Home Automation in DEVELOPMENT MODE"
	@echo ""
	@echo "   Dashboard: http://localhost:8080/dashboard"
	@echo "   API:       http://localhost:8080/api/shadow"
	@echo ""
	@echo "   The mock Home Assistant server provides sample data for all plugins."
	@echo "   Changes to UI files require restarting this command."
	@echo "════════════════════════════════════════════════════════════════════════════"
	@echo ""
	cd homeautomation-go && DEV_MODE=true ./homeautomation

clean-go: ## Clean Go build artifacts
	rm -f homeautomation-go/homeautomation
	rm -f homeautomation-go/coverage.out

##@ Testing (Cached)

unit-tests: ## Run unit tests with caching (skips if no code changes since last pass)
	@.githooks/test-cache.sh unit-tests unit-tests-impl

integration-tests: ## Run integration tests with caching (skips if no code changes since last pass)
	@.githooks/test-cache.sh integration-tests integration-tests-impl

##@ Testing (No Cache)

test-no-cache: ## Run Go tests with race detection and coverage, bypassing cache
	cd homeautomation-go && go test ./... -race -coverprofile=coverage.out
	cd homeautomation-go && go tool cover -func=coverage.out | grep total

test-no-cache-verbose: ## Run Go tests with verbose output, bypassing cache (for debugging)
	cd homeautomation-go && TEST_VERBOSE=true go test ./... -race -v -coverprofile=coverage.out
	cd homeautomation-go && go tool cover -func=coverage.out | grep total

# Backward-compat aliases (deprecated, will be removed in future)
test-go: test-no-cache
test-go-verbose: test-no-cache-verbose

check-coverage: ## Check that test coverage meets minimum requirement (>=65%)
	@echo "📊 Checking test coverage..."
	@cd homeautomation-go && \
	  go test ./... -coverprofile=coverage.out -covermode=atomic > /dev/null 2>&1 && \
	  coverage=$$(go tool cover -func=coverage.out | grep total | awk '{print $$3}' | sed 's/%//') && \
	  echo "Total coverage: $${coverage}%" && \
	  if [ "$$(echo "$$coverage < 65" | bc -l)" = "1" ]; then \
	    echo "❌ ERROR: Test coverage $${coverage}% is below required 65%"; \
	    exit 1; \
	  fi && \
	  echo "✅ Test coverage $${coverage}% meets requirement"

test-48hr-simulation: ## Run 48-hour timezone simulation tests with verbose output
	@echo "🕐 Running 48-hour timezone simulation tests..."
	cd homeautomation-go && go test ./test/integration/... -race -v -run "TestScenario_48Hour" -timeout=10m
	@echo "✅ 48-hour simulation tests passed"

pre-commit: ## Run fast pre-commit checks (style, format, lint, build)
	@echo "🔍 Running pre-commit checks (fast mode)..."
	@echo ""
	@echo "📝 Step 1/5: Checking gofmt formatting..."
	@cd homeautomation-go && \
	  unformatted=$$(gofmt -l .) && \
	  if [ -n "$$unformatted" ]; then \
	    echo "❌ ERROR: The following files are not formatted with gofmt:"; \
	    echo "$$unformatted"; \
	    echo ""; \
	    echo "Run 'make format-go' or 'cd homeautomation-go && gofmt -w .' to fix"; \
	    exit 1; \
	  fi
	@echo "✅ gofmt formatting check passed"
	@echo ""
	@echo "📦 Step 2/5: Checking goimports formatting..."
	@cd homeautomation-go && \
	  if ! command -v goimports >/dev/null 2>&1; then \
	    echo "⚠️  goimports not installed. Installing..."; \
	    go install golang.org/x/tools/cmd/goimports@latest; \
	  fi && \
	  GOIMPORTS=$$(command -v goimports 2>/dev/null || echo "$(HOME)/go/bin/goimports") && \
	  unformatted=$$($$GOIMPORTS -l .) && \
	  if [ -n "$$unformatted" ]; then \
	    echo "❌ ERROR: The following files have import formatting issues:"; \
	    echo "$$unformatted"; \
	    echo ""; \
	    echo "Run 'cd homeautomation-go && goimports -w .' to fix"; \
	    exit 1; \
	  fi
	@echo "✅ goimports formatting check passed"
	@echo ""
	@echo "🔎 Step 3/5: Running go vet static analysis..."
	@cd homeautomation-go && go vet ./...
	@echo "✅ go vet passed"
	@echo ""
	@echo "🔬 Step 4/5: Running staticcheck linting..."
	@cd homeautomation-go && \
	  if ! command -v staticcheck >/dev/null 2>&1; then \
	    echo "⚠️  staticcheck not installed. Installing..."; \
	    go install honnef.co/go/tools/cmd/staticcheck@latest; \
	  fi && \
	  STATICCHECK=$$(command -v staticcheck 2>/dev/null || echo "$(HOME)/go/bin/staticcheck") && \
	  $$STATICCHECK ./...
	@echo "✅ staticcheck passed"
	@echo ""
	@echo "🔨 Step 5/5: Building all packages..."
	@cd homeautomation-go && go build ./...
	@echo "✅ Build successful"
	@echo ""
	@echo "════════════════════════════════════════════════════════════════════"
	@echo "🎉 Pre-commit checks passed!"
	@echo ""
	@echo "ℹ️  Note: Full test suite (tests, race detector, coverage) will run"
	@echo "   automatically on git push via the pre-push hook."
	@echo "════════════════════════════════════════════════════════════════════"

pre-push: ## Run comprehensive pre-push validation (build, tests, race detector, coverage >=65%)
	@echo ""
	@echo "🔍 Running pre-push validation (4 steps)..."
	@echo "════════════════════════════════════════════════════════════════════════════"
	@echo ""
	@echo "⏳ Step 1/4: Validating config files (yamllint)..."
	@$(MAKE) yamllint-local
	@echo ""
	@echo "✅ Step 1/4 complete: Config files valid"
	@echo ""
	@echo "⏳ Step 2/4: Validating generated diagrams are up-to-date..."
	@$(MAKE) validate-diagrams
	@echo ""
	@echo "✅ Step 2/4 complete: Diagrams valid"
	@echo ""
	@echo "⏳ Step 3/4: Running unit tests (build + race detector + coverage ≥65%)..."
	@$(MAKE) unit-tests
	@echo "✅ Step 3/4 complete: Unit tests passed"
	@echo ""
	@echo "⏳ Step 4/4: Running integration tests (race detector)..."
	@$(MAKE) integration-tests
	@echo "✅ Step 4/4 complete: Integration tests passed"
	@echo ""
	@echo "════════════════════════════════════════════════════════════════════════════"
	@echo "🎉 Pre-push validation passed!"
	@echo "════════════════════════════════════════════════════════════════════════════"

pre-push-docs-only: ## Run lightweight pre-push validation for documentation-only changes
	@echo ""
	@echo "🔍 Running pre-push validation (docs-only mode)..."
	@echo "════════════════════════════════════════════════════════════════════════════"
	@echo ""
	@echo "⏳ Step 1/1: Validating generated diagrams are up-to-date..."
	@$(MAKE) validate-diagrams
	@echo ""
	@echo "════════════════════════════════════════════════════════════════════════════"
	@echo "🎉 Pre-push validation passed (docs-only)!"
	@echo ""
	@echo "✅ Generated diagrams are up-to-date"
	@echo ""
	@echo "ℹ️  Tests were skipped because only documentation files changed."
	@echo "   CI will still run the full test suite as a safety net."
	@echo "════════════════════════════════════════════════════════════════════════════"

##@ Cache Management

cache-status: ## Show test cache status
	@.githooks/test-cache.sh --status

cache-clear: ## Clear all test caches
	@.githooks/test-cache.sh --clear

cache-clear-unit: ## Clear unit test cache only
	@.githooks/test-cache.sh --clear-one unit-tests

cache-clear-integration: ## Clear integration test cache only
	@.githooks/test-cache.sh --clear-one integration-tests

##@ Code Quality

format-go: ## Format Go code with gofmt and goimports
	@echo "🎨 Formatting Go code..."
	@cd homeautomation-go && gofmt -w .
	@cd homeautomation-go && \
	  if ! command -v goimports >/dev/null 2>&1; then \
	    echo "⚠️  goimports not installed. Installing..."; \
	    go install golang.org/x/tools/cmd/goimports@latest; \
	  fi && \
	  (command -v goimports >/dev/null 2>&1 && goimports -w . || $(HOME)/go/bin/goimports -w .)
	@echo "✅ Code formatted successfully"

lint-go: ## Run all Go linters (go vet, staticcheck)
	@echo "🔬 Running Go linters..."
	@cd homeautomation-go && go vet ./...
	@cd homeautomation-go && \
	  if ! command -v staticcheck >/dev/null 2>&1; then \
	    echo "⚠️  staticcheck not installed. Installing..."; \
	    go install honnef.co/go/tools/cmd/staticcheck@latest; \
	  fi && \
	  (command -v staticcheck >/dev/null 2>&1 && staticcheck ./... || $(HOME)/go/bin/staticcheck ./...)
	@echo "✅ All linters passed"

##@ Docker

docker-build-go: ## Build Docker image for the Go application
	docker build -t homeautomation:latest ./homeautomation-go/

docker-run-go: docker-build-go ## Run the Go application in Docker (requires .env file)
	@if [ ! -f homeautomation-go/.env ]; then \
		echo "ERROR: homeautomation-go/.env file not found. Copy .env.example and configure it."; \
		exit 1; \
	fi
	docker run --rm -it \
		--name homeautomation \
		--env-file homeautomation-go/.env \
		homeautomation:latest

docker-push-go: docker-build-go ## Push Go application image to GitHub Container Registry
	@echo "Tagging image for GHCR..."
	docker tag homeautomation:latest ghcr.io/nickborgersonlowsecuritynode/home-automation:latest
	@echo "Pushing to ghcr.io/nickborgersonlowsecuritynode/home-automation:latest"
	@echo "Note: You may need to authenticate first with: echo \$$GITHUB_TOKEN | docker login ghcr.io -u USERNAME --password-stdin"
	docker push ghcr.io/nickborgersonlowsecuritynode/home-automation:latest

docker-smoke-test: docker-build-go ## Run container smoke test (builds image and verifies startup in DEV_MODE)
	@echo ""
	@echo "🚀 Running container startup smoke test..."
	@echo ""
	@# Start container in DEV_MODE (uses mock HA server)
	@docker run -d --name smoke-test \
		-e DEV_MODE=true \
		-p 8080:8080 \
		homeautomation:latest
	@# Wait for container to either become healthy or crash
	@echo "Waiting for container to start (max 30 seconds)..."
	@for i in $$(seq 1 30); do \
		if ! docker ps -q --filter "name=smoke-test" | grep -q .; then \
			echo "❌ Container exited unexpectedly!"; \
			echo ""; \
			echo "Container logs:"; \
			docker logs smoke-test; \
			docker rm smoke-test 2>/dev/null || true; \
			exit 1; \
		fi; \
		if curl -sf http://localhost:8080/health >/dev/null 2>&1; then \
			echo "✅ Container started successfully (health check passed)"; \
			break; \
		fi; \
		if [ $$i -eq 30 ]; then \
			echo "❌ Container failed to become healthy within 30 seconds"; \
			echo ""; \
			echo "Container logs:"; \
			docker logs smoke-test; \
			docker stop smoke-test 2>/dev/null || true; \
			docker rm smoke-test 2>/dev/null || true; \
			exit 1; \
		fi; \
		sleep 1; \
	done
	@# Additional verification
	@curl -sf http://localhost:8080/dashboard >/dev/null 2>&1 && echo "✅ Dashboard endpoint responding" || echo "⚠️  Dashboard endpoint not responding"
	@curl -sf http://localhost:8080/api/shadow | jq . >/dev/null 2>&1 && echo "✅ Shadow API returns valid JSON" || echo "⚠️  Shadow API not responding"
	@# Cleanup
	@docker stop smoke-test >/dev/null
	@docker rm smoke-test >/dev/null
	@echo ""
	@echo "✅ Container smoke test passed"

##@ CI Implementation Targets

unit-tests-impl: ## Run unit tests with coverage (implementation target, prefer 'unit-tests')
	@echo "🧪 Running unit tests (excluding integration tests, pkg/testutil, and cmd/diagramgen)..."
	@cd homeautomation-go && go build ./...
	@cd homeautomation-go && go test $$(go list ./... | grep -v /test/integration | grep -v /pkg/testutil | grep -v /cmd/diagramgen) \
	  -race -coverprofile=coverage.out -covermode=atomic -timeout=5m
	@cd homeautomation-go && \
	  coverage=$$(go tool cover -func=coverage.out | grep total | awk '{print $$3}' | sed 's/%//') && \
	  echo "Total coverage: $${coverage}%" && \
	  if [ "$$(echo "$$coverage < 65" | bc -l)" = "1" ]; then \
	    echo "❌ ERROR: Test coverage $${coverage}% is below required 65%"; \
	    exit 1; \
	  fi && \
	  echo "✅ Test coverage $${coverage}% meets requirement"
	@echo "✅ Unit tests passed"

integration-tests-impl: ## Run integration tests with race detector (implementation target, prefer 'integration-tests')
	@echo "🧪 Running integration tests..."
	@cd homeautomation-go && go test ./test/integration/... -race -timeout=10m
	@echo "✅ Integration tests passed"

ci-style-checks: pre-commit ## Run style/lint checks (used by CI style-checks job)
	@echo "✅ CI style checks complete"

# Backward-compat aliases for CI (deprecated, will be removed in future)
ci-unit-tests: unit-tests-impl
ci-integration-tests: integration-tests-impl
