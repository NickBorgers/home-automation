.DEFAULT_GOAL := help

#help: @ List available tasks on this project
help:
	@echo ""
	@echo "\033[1m━━━ Testing (Cached) ━━━\033[0m"
	@echo "\033[36m  unit-tests                    \033[0m Run unit tests with caching (skips if no code changes since last pass)"
	@echo "\033[36m  integration-tests             \033[0m Run integration tests with caching (skips if no code changes since last pass)"
	@echo ""
	@echo "\033[1m━━━ Testing (No Cache) ━━━\033[0m"
	@echo "\033[36m  test-no-cache                 \033[0m Run Go tests with race detection and coverage, bypassing cache (quiet mode for AI tools)"
	@echo "\033[36m  test-no-cache-verbose          \033[0m Run Go tests with verbose output, bypassing cache (for debugging)"
	@echo "\033[36m  pre-push                      \033[0m Run comprehensive pre-push validation (build, tests, race detector, coverage ≥65%)"
	@echo "\033[36m  pre-push-docs-only            \033[0m Run lightweight pre-push validation for documentation-only changes"
	@echo "\033[36m  pre-commit                    \033[0m Run fast pre-commit checks (style, format, lint, build)"
	@echo "\033[36m  check-coverage                \033[0m Check that test coverage meets minimum requirement (≥65%)"
	@echo "\033[36m  test-48hr-simulation          \033[0m Run 48-hour timezone simulation tests with verbose output"
	@echo ""
	@echo "\033[1m━━━ Cache Management ━━━\033[0m"
	@echo "\033[36m  cache-status                  \033[0m Show test cache status (which test suites have cached passing results)"
	@echo "\033[36m  cache-clear                   \033[0m Clear all test caches (forces re-run of all tests)"
	@echo "\033[36m  cache-clear-unit              \033[0m Clear unit test cache only (forces re-run of unit tests)"
	@echo ""
	@echo "\033[1m━━━ Build & Run ━━━\033[0m"
	@echo "\033[36m  build-go                      \033[0m Build the Go application binary"
	@echo "\033[36m  dev-ui                        \033[0m Run application with mock HA server for local UI development"
	@echo "\033[36m  clean-go                      \033[0m Clean Go build artifacts"
	@echo ""
	@echo "\033[1m━━━ Code Quality ━━━\033[0m"
	@echo "\033[36m  format-go                     \033[0m Format Go code with gofmt and goimports"
	@echo "\033[36m  lint-go                       \033[0m Run all Go linters (go vet, staticcheck)"
	@echo ""
	@echo "\033[1m━━━ Docker ━━━\033[0m"
	@echo "\033[36m  docker-build-go               \033[0m Build Docker image for the Go application"
	@echo "\033[36m  docker-run-go                 \033[0m Run the Go application in Docker (requires .env file)"
	@echo "\033[36m  docker-push-go                \033[0m Push Go application image to GitHub Container Registry"
	@echo "\033[36m  docker-smoke-test             \033[0m Run container smoke test (builds image and verifies startup in DEV_MODE)"
	@echo ""
	@echo "\033[1m━━━ Documentation ━━━\033[0m"
	@echo "\033[36m  validate-mermaid              \033[0m Validate all Mermaid diagrams in documentation can be rendered"
	@echo "\033[36m  validate-docs                 \033[0m Validate all documentation (Mermaid diagrams, etc.)"
	@echo "\033[36m  generate-diagrams             \033[0m Generate plugin dependency diagrams from source code using AST analysis"
	@echo "\033[36m  validate-diagrams             \033[0m Validate that generated diagrams are up-to-date with source code"
	@echo ""
	@echo "\033[1m━━━ Config ━━━\033[0m"
	@echo "\033[36m  run-config-tests              \033[0m Run all available tests of the configuration files"
	@echo ""
	@echo "\033[1m━━━ CI Implementation Targets ━━━\033[0m"
	@echo "\033[36m  unit-tests-impl               \033[0m Run unit tests with coverage (implementation target, prefer 'unit-tests')"
	@echo "\033[36m  integration-tests-impl        \033[0m Run integration tests with race detector (implementation target, prefer 'integration-tests')"
	@echo "\033[36m  ci-style-checks               \033[0m Run style/lint checks (used by CI style-checks job)"
	@echo ""

#run-config-tests: @ Run all available tests of the configuration files
run-config-tests: run-yamllint-hue run-yamllint-music run-spotify-validation-music

run-yamllint-hue: build-config-tester
	docker run --rm --mount type=bind,source=${CURDIR}/configs/hue_config.yaml,destination=/app/hue_config.yaml node-red-config-tester yamllint hue_config.yaml

run-yamllint-music: build-config-tester
	docker run --rm --mount type=bind,source=${CURDIR}/configs/music_config.yaml,destination=/app/music_config.yaml node-red-config-tester yamllint music_config.yaml

run-spotify-validation-music: build-config-tester
	docker run --rm --mount type=bind,source=${CURDIR}/configs/music_config.yaml,destination=/app/music_config.yaml node-red-config-tester python3 -u validate_spotify_uris.py

build-config-tester:
	docker build -t node-red-config-tester ./config-test/

# ============================================================================
# Documentation Validation Targets
# ============================================================================

#validate-mermaid: @ Validate all Mermaid diagrams in documentation can be rendered
validate-mermaid:
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

#validate-docs: @ Validate all documentation (Mermaid diagrams, etc.)
validate-docs: validate-mermaid
	@echo ""
	@echo "✅ All documentation validation passed"

# ============================================================================
# Generated Diagram Targets
# ============================================================================

#generate-diagrams: @ Generate plugin dependency diagrams from source code using AST analysis
generate-diagrams:
	@echo "🔍 Generating diagrams from plugin source code..."
	@cd homeautomation-go && go run ./cmd/diagramgen/...
	@echo "✅ Diagrams generated successfully"

#validate-diagrams: @ Validate that generated diagrams are up-to-date with source code
validate-diagrams:
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

#build-go: @ Build the Go application binary
build-go:
	cd homeautomation-go && go build -ldflags "$(LDFLAGS)" -o homeautomation ./cmd/main.go

#dev-ui: @ Run application with mock HA server for local UI development
dev-ui: build-go
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

#test-no-cache: @ Run Go tests with race detection and coverage, bypassing cache (quiet mode for AI tools)
test-no-cache:
	cd homeautomation-go && go test ./... -race -coverprofile=coverage.out
	cd homeautomation-go && go tool cover -func=coverage.out | grep total

#test-no-cache-verbose: @ Run Go tests with verbose output, bypassing cache (for debugging)
test-no-cache-verbose:
	cd homeautomation-go && TEST_VERBOSE=true go test ./... -race -v -coverprofile=coverage.out
	cd homeautomation-go && go tool cover -func=coverage.out | grep total

# Backward-compat aliases (deprecated, will be removed in future)
test-go: test-no-cache
test-go-verbose: test-no-cache-verbose

#docker-build-go: @ Build Docker image for the Go application
docker-build-go:
	docker build -t homeautomation:latest ./homeautomation-go/

#docker-run-go: @ Run the Go application in Docker (requires .env file)
docker-run-go: docker-build-go
	@if [ ! -f homeautomation-go/.env ]; then \
		echo "ERROR: homeautomation-go/.env file not found. Copy .env.example and configure it."; \
		exit 1; \
	fi
	docker run --rm -it \
		--name homeautomation \
		--env-file homeautomation-go/.env \
		homeautomation:latest

#docker-push-go: @ Push Go application image to GitHub Container Registry
docker-push-go: docker-build-go
	@echo "Tagging image for GHCR..."
	docker tag homeautomation:latest ghcr.io/nickborgersonlowsecuritynode/home-automation:latest
	@echo "Pushing to ghcr.io/nickborgersonlowsecuritynode/home-automation:latest"
	@echo "Note: You may need to authenticate first with: echo \$$GITHUB_TOKEN | docker login ghcr.io -u USERNAME --password-stdin"
	docker push ghcr.io/nickborgersonlowsecuritynode/home-automation:latest

#clean-go: @ Clean Go build artifacts
clean-go:
	rm -f homeautomation-go/homeautomation
	rm -f homeautomation-go/coverage.out

#docker-smoke-test: @ Run container smoke test (builds image and verifies startup in DEV_MODE)
docker-smoke-test: docker-build-go
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

#check-coverage: @ Check that test coverage meets minimum requirement (≥65%)
check-coverage:
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

#pre-commit: @ Run fast pre-commit checks (style, format, lint, build)
pre-commit:
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

#format-go: @ Format Go code with gofmt and goimports
format-go:
	@echo "🎨 Formatting Go code..."
	@cd homeautomation-go && gofmt -w .
	@cd homeautomation-go && \
	  if ! command -v goimports >/dev/null 2>&1; then \
	    echo "⚠️  goimports not installed. Installing..."; \
	    go install golang.org/x/tools/cmd/goimports@latest; \
	  fi && \
	  (command -v goimports >/dev/null 2>&1 && goimports -w . || $(HOME)/go/bin/goimports -w .)
	@echo "✅ Code formatted successfully"

#lint-go: @ Run all Go linters (go vet, staticcheck)
lint-go:
	@echo "🔬 Running Go linters..."
	@cd homeautomation-go && go vet ./...
	@cd homeautomation-go && \
	  if ! command -v staticcheck >/dev/null 2>&1; then \
	    echo "⚠️  staticcheck not installed. Installing..."; \
	    go install honnef.co/go/tools/cmd/staticcheck@latest; \
	  fi && \
	  (command -v staticcheck >/dev/null 2>&1 && staticcheck ./... || $(HOME)/go/bin/staticcheck ./...)
	@echo "✅ All linters passed"

# ============================================================================
# CI Targets (used by GitHub Actions workflows)
# These targets match what CI runs, allowing local verification before push
# ============================================================================

#test-48hr-simulation: @ Run 48-hour timezone simulation tests with verbose output
test-48hr-simulation:
	@echo "🕐 Running 48-hour timezone simulation tests..."
	cd homeautomation-go && go test ./test/integration/... -race -v -run "TestScenario_48Hour" -timeout=10m
	@echo "✅ 48-hour simulation tests passed"

#ci-style-checks: @ Run style/lint checks (used by CI style-checks job)
ci-style-checks: pre-commit
	@echo "✅ CI style checks complete"

#unit-tests: @ Run unit tests with caching (skips if no code changes since last pass)
unit-tests:
	@.githooks/test-cache.sh unit-tests unit-tests-impl

#integration-tests: @ Run integration tests with caching (skips if no code changes since last pass)
integration-tests:
	@.githooks/test-cache.sh integration-tests integration-tests-impl

#unit-tests-impl: @ Run unit tests with coverage, excluding integration tests (implementation target)
unit-tests-impl:
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

#integration-tests-impl: @ Run integration tests with race detector (implementation target)
integration-tests-impl:
	@echo "🧪 Running integration tests..."
	@cd homeautomation-go && go test ./test/integration/... -race -timeout=10m
	@echo "✅ Integration tests passed"

# Backward-compat aliases for CI (deprecated, will be removed in future)
ci-unit-tests: unit-tests-impl
ci-integration-tests: integration-tests-impl

#pre-push-docs-only: @ Run lightweight pre-push validation for documentation-only changes
pre-push-docs-only:
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

#pre-push: @ Run comprehensive pre-push validation (build, tests, race detector, coverage ≥65%)
pre-push:
	@echo ""
	@echo "🔍 Running pre-push validation (5 steps)..."
	@echo "════════════════════════════════════════════════════════════════════════════"
	@echo ""
	@echo "⏳ Step 1/5: Validating generated diagrams are up-to-date..."
	@$(MAKE) validate-diagrams
	@echo ""
	@echo "✅ Step 1/5 complete: Diagrams valid"
	@echo ""
	@echo "⏳ Step 2/5: Compiling all code (including tests)..."
	@cd homeautomation-go && go build ./...
	@echo "✅ Step 2/5 complete: All code compiles"
	@echo ""
	@echo "⏳ Step 3/5: Running unit tests with race detector and coverage..."
	@echo "   (excluding integration tests, testutil, and diagramgen - same as CI)"
	@echo "   This may take 2-5 minutes on first run."
	@cd homeautomation-go && go test $$(go list ./... | grep -v /test/integration | grep -v /pkg/testutil | grep -v /cmd/diagramgen) \
	  -race -coverprofile=coverage.out -covermode=atomic -timeout=5m
	@echo "✅ Step 3/5 complete: Unit tests passed with race detector"
	@echo ""
	@echo "⏳ Step 4/5: Checking test coverage (≥65%)..."
	@cd homeautomation-go && \
	  coverage=$$(go tool cover -func=coverage.out | grep total | awk '{print $$3}' | sed 's/%//') && \
	  echo "Total coverage: $${coverage}%" && \
	  if [ "$$(echo "$$coverage < 65" | bc -l)" = "1" ]; then \
	    echo "❌ ERROR: Test coverage $${coverage}% is below required 65%"; \
	    rm -f coverage.out; \
	    exit 1; \
	  fi && \
	  echo "✅ Step 4/5 complete: Test coverage $${coverage}% meets requirement" && \
	  rm -f coverage.out
	@echo ""
	@echo "⏳ Step 5/5: Running integration tests with race detector..."
	@echo "   This may take 3-10 minutes on first run."
	@cd homeautomation-go && go test ./test/integration/... -race -timeout=10m
	@echo "✅ Step 5/5 complete: Integration tests passed"
	@echo ""
	@echo "════════════════════════════════════════════════════════════════════════════"
	@echo "🎉 Pre-push validation passed!"
	@echo ""
	@echo "✅ Generated diagrams are up-to-date"
	@echo "✅ All code compiles"
	@echo "✅ Unit tests passed with race detector"
	@echo "✅ Test coverage meets minimum requirement (≥65%)"
	@echo "✅ Integration tests passed"
	@echo "════════════════════════════════════════════════════════════════════════════"

# ============================================================================
# Cache Management Targets
# ============================================================================

#cache-status: @ Show test cache status (which test suites have cached passing results)
cache-status:
	@.githooks/test-cache.sh --status

#cache-clear: @ Clear all test caches (forces re-run of all tests)
cache-clear:
	@.githooks/test-cache.sh --clear

#cache-clear-unit: @ Clear unit test cache only (forces re-run of unit tests)
cache-clear-unit:
	@.githooks/test-cache.sh --clear-one unit-tests
