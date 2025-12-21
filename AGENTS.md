# Agent Guide - Home Automation Project

This document provides guidance for AI agents and developers working on this home automation project.

## Project Overview

This repository contains a home automation system migrating from Node-RED to Golang for improved type safety, testability, and maintainability.

## 🚨 CRITICAL: Pre-Push Hook Active

**A pre-push git hook runs all tests before every push and BLOCKS if they fail.**

The hook runs: code compilation + all tests + race detector + coverage check (≥70%)

**NEVER use `git push --no-verify`.** Fix the tests instead.

**Key commands:**
```bash
make pre-push          # Run full validation (same as CI)
make pre-commit        # Fast checks only (formatting, linting, build)
make format-go         # Auto-format code
make lint-go           # Run linters
```

---

## Repository Structure

```
├── homeautomation-go/          # Golang implementation
│   ├── cmd/main.go             # Application entry point
│   ├── internal/ha/            # Home Assistant WebSocket client
│   ├── internal/state/         # State management layer
│   ├── internal/plugins/       # Plugin implementations
│   └── test/integration/       # Integration test suite
├── docs/
│   ├── architecture/ARCHITECTURE.md  # System design & migration status
│   ├── reference/SHADOW_STATE.md     # Shadow state pattern (READ BEFORE WRITING PLUGINS)
│   ├── reference/PLUGIN_SYSTEM.md    # Plugin interfaces
│   ├── reference/migration_mapping.md # State variable mapping
│   └── reference/CONCURRENCY_LESSONS.md # Concurrency patterns
├── flows.json                  # Node-RED legacy implementation (~650KB)
└── configs/                    # YAML configuration files
```

## Key Documentation

| Document | Purpose |
|----------|---------|
| [ARCHITECTURE.md](./docs/architecture/ARCHITECTURE.md) | System design, implementation status, migration roadmap |
| [SHADOW_STATE.md](./docs/reference/SHADOW_STATE.md) | **READ BEFORE WRITING PLUGINS** - Shadow state pattern |
| [PLUGIN_SYSTEM.md](./docs/reference/PLUGIN_SYSTEM.md) | Plugin interfaces and lifecycle |
| [migration_mapping.md](./docs/reference/migration_mapping.md) | State variable mapping from Node-RED |
| [CONCURRENCY_LESSONS.md](./docs/reference/CONCURRENCY_LESSONS.md) | Concurrency patterns and lessons |

## Understanding Node-RED Behavior

Before implementing features, understand the current Node-RED behavior.

**⚠️ WARNING:** `flows.json` is ~650KB. Do NOT read it all at once. Use targeted searches:

```bash
# Generate flow screenshots for visual overview
make generate-screenshots
# View: ./automated-rendering/screenshot-capture/screenshots/

# Search patterns
grep -A 5 '"label":"Music"' flows.json              # Find a flow
grep -A 20 '"name":"Pick Appropriate Music"' flows.json  # Find function node
grep -n "isNickHome" flows.json                     # Find state variable usage

# Live instance: https://node-red.featherback-mermaid.ts.net/
```

**Flow to Config Mapping:**

| Flow | Config File | Key State Variables |
|------|-------------|---------------------|
| State Tracking | N/A | isNickHome, isCarolineHome, isToriHere, isMasterAsleep |
| Lighting Control | hue_config.yaml | dayPhase, sunevent, isAnyoneHome |
| Music | music_config.yaml | musicPlaybackType, currentlyPlayingMusic |
| Sleep Hygiene | schedule_config.yaml | isMasterAsleep, alarmTime |
| Energy State | energy_config.yaml | batteryEnergyLevel, currentEnergyLevel |

## Development Standards

### Shadow State Pattern (CRITICAL)

**Every plugin MUST implement shadow state tracking.** See [SHADOW_STATE.md](./docs/reference/SHADOW_STATE.md).

```go
// EVERY handler must update shadow inputs at the start
func (m *Manager) handleSomeChange(entityID string, oldState, newState *ha.State) {
    if newState == nil {
        return
    }
    m.updateShadowInputs()  // 1. FIRST: Capture what triggered this
    // 2. Process the change
    // 3. Update state variables
    // 4. Update shadow state outputs
}
```

**Checklist for new plugins:**
- [ ] Add `shadowTracker` field to Manager struct
- [ ] Implement `updateShadowInputs()` method
- [ ] Call `updateShadowInputs()` at START of every handler
- [ ] Test that `/api/shadow/{plugin}` shows populated inputs

### Go Code Standards

**Style:** Follow `gofmt`, use `staticcheck`, 120 char max line length, godoc comments on exports.

**Testing:** 70% minimum coverage, table-driven tests, always use `-race` flag.

**Error Handling:** Always check errors, wrap with context (`fmt.Errorf("context: %w", err)`), never panic.

**Concurrency:** Protect shared state with mutexes, use `sync.RWMutex` for read-heavy ops, **serialize WebSocket writes with `writeMu`**.

### API Change Protocol

When modifying function signatures:
1. **Search** for all call sites: `grep -r "FunctionName" .`
2. **Update** ALL call sites (code + tests + docs)
3. **Compile check**: `go build ./...`
4. **Run ALL tests**: `go test -race ./...`

### Documentation Maintenance

Update docs when making changes:
- New plugin → Update `VISUAL_ARCHITECTURE.md`, `ARCHITECTURE.md`
- New state variable → Update `migration_mapping.md`, `VISUAL_ARCHITECTURE.md`
- Concurrency fix → Update `CONCURRENCY_LESSONS.md`

Validate Mermaid diagrams: `make validate-mermaid`

## Running Tests

```bash
cd homeautomation-go

# All tests with race detection (CI requirement)
go test -race ./...

# Coverage report
go test -coverprofile=coverage.out ./... && go tool cover -html=coverage.out

# Integration tests only
go test -v -race ./test/integration/...
```

**Test Status:** All 11 integration tests passing ✅

## Building and Running

```bash
cd homeautomation-go
go build -o homeautomation ./cmd/main.go
./homeautomation
```

**Environment:** Create `.env` from `.env.example`:
```env
HA_URL=wss://your-homeassistant/api/websocket
HA_TOKEN=your_long_lived_access_token
READ_ONLY=true
```

## Common CI Failures

| Error | Cause | Fix |
|-------|-------|-----|
| "not enough arguments in call" | Signature changed, call sites not updated | `grep -r "FunctionName" .` and update all |
| "undefined: X" | Missing dependency | `go mod tidy && go mod download` |
| Test timeout/deadlock | Missing mutex | Review with `-race` flag |
| Tests pass locally, fail CI | Race condition or env diff | Run `go test -race ./...` locally |

## Migration Status

**Current Phase:** MVP Complete + Integration Testing ✅

- Go implementation ready for parallel testing (READ_ONLY mode)
- All 28 state variables supported
- All critical bugs fixed (concurrent writes, subscription leak)

**Next Steps:**
1. Validate behavior matches Node-RED
2. Migrate helper functions
3. Switch to read-write mode
4. Deprecate Node-RED

---

**Last Updated:** 2025-12-21
**Go Version:** 1.23
**Project Status:** Parallel Testing Phase
