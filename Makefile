.DEFAULT_GOAL := help

#help: @ List available tasks on this project
help: 
	@grep -E '[a-zA-Z\.\-]+:.*?@ .*$$' $(MAKEFILE_LIST)| tr -d '#' | sed -E 's/Makefile.//' | awk 'BEGIN {FS = ":.*?@ "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

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
	@# Extract and validate each mermaid block from VISUAL_ARCHITECTURE.md
	@rm -rf .mermaid-tmp && mkdir -p .mermaid-tmp && chmod 777 .mermaid-tmp
	@awk '/^```mermaid$$/,/^```$$/' docs/human/VISUAL_ARCHITECTURE.md | \
	  awk 'BEGIN{n=0} /^```mermaid$$/{n++;f=".mermaid-tmp/diagram-"n".mmd";next} /^```$$/{close(f);next} {print > f}'
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
# Go Application (homeautomation-go) Targets
# ============================================================================

#build-go: @ Build the Go application binary
build-go:
	cd homeautomation-go && go build -o homeautomation ./cmd/main.go

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

#test-go: @ Run Go tests with race detection and coverage
test-go:
	cd homeautomation-go && go test ./... -race -v -coverprofile=coverage.out
	cd homeautomation-go && go tool cover -func=coverage.out | grep total

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

#check-coverage: @ Check that test coverage meets minimum requirement (≥70%)
check-coverage:
	@echo "📊 Checking test coverage..."
	@cd homeautomation-go && \
	  go test ./... -coverprofile=coverage.out -covermode=atomic > /dev/null 2>&1 && \
	  coverage=$$(go tool cover -func=coverage.out | grep total | awk '{print $$3}' | sed 's/%//') && \
	  echo "Total coverage: $${coverage}%" && \
	  if [ "$$(echo "$$coverage < 70" | bc -l)" = "1" ]; then \
	    echo "❌ ERROR: Test coverage $${coverage}% is below required 70%"; \
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

#ci-style-checks: @ Run style/lint checks (used by CI style-checks job)
ci-style-checks: pre-commit
	@echo "✅ CI style checks complete"

#ci-unit-tests: @ Run unit tests with coverage, excluding integration tests (used by CI)
ci-unit-tests:
	@echo "🧪 Running unit tests (excluding integration tests and pkg/testutil)..."
	@cd homeautomation-go && go build ./...
	@cd homeautomation-go && go test $$(go list ./... | grep -v /test/integration | grep -v /pkg/testutil) \
	  -race -v -coverprofile=coverage.out -covermode=atomic -timeout=5m
	@cd homeautomation-go && \
	  coverage=$$(go tool cover -func=coverage.out | grep total | awk '{print $$3}' | sed 's/%//') && \
	  echo "Total coverage: $${coverage}%" && \
	  if [ "$$(echo "$$coverage < 70" | bc -l)" = "1" ]; then \
	    echo "❌ ERROR: Test coverage $${coverage}% is below required 70%"; \
	    exit 1; \
	  fi && \
	  echo "✅ Test coverage $${coverage}% meets requirement"
	@echo "✅ Unit tests passed"

#ci-integration-tests: @ Run integration tests with race detector (used by CI)
ci-integration-tests:
	@echo "🧪 Running integration tests..."
	@cd homeautomation-go && go test ./test/integration/... -race -v -timeout=5m
	@echo "✅ Integration tests passed"

#pre-push: @ Run comprehensive pre-push validation (build, tests, race detector, coverage ≥70%)
pre-push:
	@echo ""
	@echo "🔍 Running pre-push validation..."
	@echo "════════════════════════════════════════════════════════════════════════════"
	@echo ""
	@echo "📦 Step 1/3: Compiling all code (including tests)..."
	@cd homeautomation-go && go build ./...
	@echo "✅ All code compiles"
	@echo ""
	@echo "🧪 Step 2/3: Running all tests with race detector and coverage..."
	@cd homeautomation-go && go test ./... -race -coverprofile=coverage.out -covermode=atomic -timeout=5m
	@echo "✅ All tests passed with race detector"
	@echo ""
	@echo "📊 Step 3/3: Checking test coverage (≥70%)..."
	@cd homeautomation-go && \
	  coverage=$$(go tool cover -func=coverage.out | grep total | awk '{print $$3}' | sed 's/%//') && \
	  echo "Total coverage: $${coverage}%" && \
	  if [ "$$(echo "$$coverage < 70" | bc -l)" = "1" ]; then \
	    echo "❌ ERROR: Test coverage $${coverage}% is below required 70%"; \
	    rm -f coverage.out; \
	    exit 1; \
	  fi && \
	  echo "✅ Test coverage $${coverage}% meets requirement" && \
	  rm -f coverage.out
	@echo ""
	@echo "════════════════════════════════════════════════════════════════════════════"
	@echo "🎉 Pre-push validation passed!"
	@echo ""
	@echo "✅ All code compiles"
	@echo "✅ All tests passed with race detector"
	@echo "✅ Test coverage meets minimum requirement (≥70%)"
	@echo "════════════════════════════════════════════════════════════════════════════"
