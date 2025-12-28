# Agent Guide - Home Automation Project

This document provides guidance for AI agents and developers working on this home automation project.

## Project Overview

This repository contains a home automation system built in Golang with improved type safety, testability, and maintainability.

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
└── configs/                    # YAML configuration files
```

## Key Documentation

| Document | Purpose |
|----------|---------|
| [ARCHITECTURE.md](./docs/architecture/ARCHITECTURE.md) | System design, implementation status, migration roadmap |
| [SHADOW_STATE.md](./docs/reference/SHADOW_STATE.md) | **READ BEFORE WRITING PLUGINS** - Shadow state pattern |
| [PLUGIN_SYSTEM.md](./docs/reference/PLUGIN_SYSTEM.md) | Plugin interfaces and lifecycle |
| [migration_mapping.md](./docs/reference/migration_mapping.md) | State variable mapping reference |
| [CONCURRENCY_LESSONS.md](./docs/reference/CONCURRENCY_LESSONS.md) | Concurrency patterns and lessons |

## Plugin to Config Mapping

| Plugin | Config File | Key State Variables |
|--------|-------------|---------------------|
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
| Tests pass locally, fail CI | Race condition or env diff | Run `go test -race ./...` locally |

## Project Status

**Current Phase:** Production ✅

- Go implementation running in production
- All 28 state variables supported
- All plugins fully operational

---

**Last Updated:** 2025-12-28
**Go Version:** 1.23
**Project Status:** Production
