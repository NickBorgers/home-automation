# Agent Guide - Home Automation Project

This document provides guidance for AI agents and developers working on this home automation project.

## Project Overview

This repository contains a home automation system migrating from Node-RED to Golang for improved type safety, testability, and maintainability.

## 🚨 CRITICAL: Test Commands (AI Agents READ THIS)

**NEVER run `go test` directly. ALWAYS use the cached Makefile targets:**

```bash
make unit-tests        # ✅ CORRECT - uses caching
make integration-tests # ✅ CORRECT - uses caching
```

**WRONG (bypasses cache, wastes time):**
```bash
go test ./...                    # ❌ WRONG
go test -race ./...              # ❌ WRONG
cd homeautomation-go && go test  # ❌ WRONG
```

The cache tracks code changes and skips tests when nothing changed. Running `go test` directly always runs the full suite unnecessarily.

---

## 🚨 CRITICAL: Pre-Push Hook Active

**A pre-push git hook runs all tests before every push and BLOCKS if they fail.**

The hook runs: code compilation + all tests + race detector + coverage check (≥65%)

**NEVER use `git push --no-verify`.** Fix the tests instead.

**Key commands:**
```bash
make unit-tests        # Run unit tests WITH CACHING (preferred)
make integration-tests # Run integration tests WITH CACHING (preferred)
make pre-push          # Run full validation (same as CI, no cache)
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

**Testing:** 65% minimum coverage, table-driven tests, always use `-race` flag.

**Error Handling:** Always check errors, wrap with context (`fmt.Errorf("context: %w", err)`), never panic.

**Concurrency:** Protect shared state with mutexes, use `sync.RWMutex` for read-heavy ops, **serialize WebSocket writes with `writeMu`**.

### API Change Protocol

When modifying function signatures:
1. **Search** for all call sites: `grep -r "FunctionName" .`
2. **Update** ALL call sites (code + tests + docs)
3. **Compile check**: `go build ./...`
4. **Run ALL tests**: `make unit-tests && make integration-tests` (uses caching)

### Documentation Maintenance

Update docs when making changes:
- New plugin → Update `VISUAL_ARCHITECTURE.md`, `ARCHITECTURE.md`
- New state variable → Update `migration_mapping.md`, `VISUAL_ARCHITECTURE.md`
- Concurrency fix → Update `CONCURRENCY_LESSONS.md`

Validate Mermaid diagrams: `make validate-mermaid`

### Diagram Maintenance Protocol

**Mermaid diagrams in `docs/human/VISUAL_ARCHITECTURE.md` must stay synchronized with code.**

A Claude Code hook (`.claude/hooks/check-diagrams.sh`) reminds you when plugin code changes or diagram files are edited. The hook is optimized to minimize context window usage:
- Shows full reminder only once per plugin per session
- Subsequent edits to the same plugin produce no output
- Reminds to run validation manually (doesn't auto-run `make validate-mermaid`)

**When to update diagrams:**

| Code Change | Diagram Section to Update |
|-------------|---------------------------|
| New plugin added | System Architecture, Plugin System Architecture |
| Plugin removed | System Architecture, Plugin System Architecture |
| New `Subscribe()` call | State Variable Dependency Graph |
| State variable added/removed | State Variable Dependency Graph |
| Plugin logic changed | Relevant logic flow diagram (Music, Lighting, Energy, etc.) |

**Diagram update checklist:**
1. Identify affected diagrams using the table above
2. Update the Mermaid source in `docs/human/VISUAL_ARCHITECTURE.md`
3. Run `make validate-mermaid` to check syntax
4. Preview rendering (GitHub renders Mermaid natively, or use [Mermaid Live](https://mermaid.live/))
5. Commit diagram updates **in the same PR** as code changes

**Key diagram sections:**
- **System Architecture** - High-level component overview
- **Plugin System Architecture** - Plugin dependencies and data flow
- **State Variable Dependency Graph** - Which plugins read/write which variables
- **Individual Logic Flows** - Decision trees for Music, Lighting, Energy, etc.

## Running Tests

**⚠️ ALWAYS use cached Makefile targets for tests** - they skip redundant runs when code hasn't changed:

```bash
# PREFERRED: Uses test caching (skips if code unchanged since last pass)
make unit-tests           # Unit tests with caching
make integration-tests    # Integration tests with caching

# No caching (runs every time)
make test-go              # All tests with race detection (no cache)
make pre-push             # Full validation (no cache, same as CI)

# Cache management
.githooks/test-cache.sh --status     # Check cache state
.githooks/test-cache.sh --clear      # Force re-run next time
```

**How caching works:** The `.githooks/test-cache.sh` script tracks a content-based hash of the codebase. If nothing changed since the last successful test run, tests are skipped entirely. This saves significant time during development.

**Direct commands (avoid - always bypasses cache):**
```bash
cd homeautomation-go
go test -race ./...                           # Bypasses cache
go test -coverprofile=coverage.out ./...      # Coverage report
go test -v -race ./test/integration/...       # Integration only
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

## Production Debugging

**Dashboard (view current state):** https://home-automation.featherback-mermaid.ts.net/

**View recent logs:**
```bash
ssh nborgers@dockergeneric.featherback-mermaid.ts.net docker logs --since=1m home-automation
```

Adjust `--since=1m` as needed (e.g., `--since=5m`, `--since=1h`, or omit for all logs).

## Testing UI Changes

The Go application includes a dashboard at `/dashboard` for viewing shadow state. To test UI changes without requiring a real Home Assistant instance, use **DEV_MODE**.

### Quick Start: Local UI Testing

```bash
# Start the app with mock Home Assistant data
make dev-ui

# Dashboard available at: http://localhost:8080/dashboard
# API endpoint at:        http://localhost:8080/api/shadow
```

DEV_MODE starts a mock Home Assistant WebSocket server with realistic sample data for all plugins. Changes to HTML/CSS in `homeautomation-go/internal/api/templates/` require restarting `make dev-ui`.

### Capturing Screenshots with Playwright

Use Playwright to capture screenshots for PR documentation or visual regression testing:

**1. Start the dev server:**
```bash
make dev-ui
```

**2. Capture a screenshot (example - laptop resolution):**
```bash
# Write a Playwright script
cat > /tmp/playwright-screenshot.js << 'EOF'
const { chromium } = require('playwright');

(async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();

  // Set viewport (common sizes: 1920x1080 desktop, 1280x800 laptop, 375x667 mobile)
  await page.setViewportSize({ width: 1280, height: 800 });

  await page.goto('http://localhost:8080/dashboard', { waitUntil: 'networkidle' });
  await page.waitForTimeout(500); // Allow animations to settle

  await page.screenshot({
    path: 'docs/screenshots/dashboard-screenshot.png',
    fullPage: true
  });

  console.log('Screenshot saved');
  await browser.close();
})();
EOF

# Execute with Playwright skill (if installed)
cd ~/.claude/plugins/cache/playwright-skill/playwright-skill/*/skills/playwright-skill && \
  node run.js /tmp/playwright-screenshot.js

# Or with system playwright
npx playwright test /tmp/playwright-screenshot.js
```

**3. Responsive testing (multiple viewports):**
```bash
cat > /tmp/playwright-responsive.js << 'EOF'
const { chromium } = require('playwright');

const viewports = [
  { name: 'desktop', width: 1920, height: 1080 },
  { name: 'laptop', width: 1280, height: 800 },
  { name: 'tablet', width: 768, height: 1024 },
  { name: 'mobile', width: 375, height: 667 }
];

(async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();

  for (const vp of viewports) {
    await page.setViewportSize({ width: vp.width, height: vp.height });
    await page.goto('http://localhost:8080/dashboard', { waitUntil: 'networkidle' });
    await page.waitForTimeout(300);
    await page.screenshot({
      path: `docs/screenshots/dashboard-${vp.name}.png`,
      fullPage: true
    });
    console.log(`Captured ${vp.name} (${vp.width}x${vp.height})`);
  }

  await browser.close();
})();
EOF
```

### Dashboard UI Details

**Location:** `homeautomation-go/internal/api/templates/dashboard.html`

**Key features:**
- Responsive grid: 2 columns on laptop+, 1 column on mobile (< 640px)
- Auto-refresh: Polls `/api/shadow` every 30 seconds
- Collapsible plugin cards with inputs/outputs/metadata sections
- Stale data indicators (warning at 5 min, error at 15 min)

**Sample data (DEV_MODE):** Configured in `homeautomation-go/internal/devserver/devserver.go`

### Adding Screenshots to PRs

When making UI changes, capture and commit screenshots:

```bash
# 1. Start dev server and capture
make dev-ui &
sleep 3
# (run playwright script)

# 2. Commit the screenshot
git add docs/screenshots/your-screenshot.png
git commit -m "docs: Add screenshot of [feature]"

# 3. Reference in PR comment with raw GitHub URL
# ![Description](https://raw.githubusercontent.com/OWNER/REPO/BRANCH/docs/screenshots/your-screenshot.png)
```

## Common CI Failures

| Error | Cause | Fix |
|-------|-------|-----|
| "not enough arguments in call" | Signature changed, call sites not updated | `grep -r "FunctionName" .` and update all |
| "undefined: X" | Missing dependency | `go mod tidy && go mod download` |
| Test timeout/deadlock | Missing mutex | Review with `-race` flag |
| Tests pass locally, fail CI | Race condition or env diff | `make unit-tests && make integration-tests` locally |

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

**Last Updated:** 2026-01-02
**Go Version:** 1.24
**Project Status:** Parallel Testing Phase
