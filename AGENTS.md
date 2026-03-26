# Agent Guide - Home Automation Project

This document provides guidance for AI agents and developers working on this home automation project.

## Project Overview

This repository contains a home automation system migrating from Node-RED to Golang for improved type safety, testability, and maintainability.

## Important: Test Commands and Pre-Push Hook

This repository uses test caching to avoid redundant test runs. Always use the Makefile targets:

```bash
make unit-tests        # ✅ PREFERRED - uses caching
make integration-tests # ✅ PREFERRED - uses caching
make test-go           # For debugging cache issues (no cache)
```

A pre-push git hook validates all changes before pushing. It runs:
- Config validation (yamllint on YAML config files)
- Diagram validation
- Code compilation
- Unit tests (2-5 minutes)
- Integration tests (2-5 minutes)
- Coverage check (≥65%)

**First-time contributors:** Your first push may take 15 minutes. Subsequent pushes with no code changes skip tests via caching.

**Important:** Never bypass hooks without understanding the failure. The pre-push hook prevents pushing broken code. If tests fail, fix them. Use `git push --no-verify` ONLY for emergencies (production outage, urgent hotfix) and open a follow-up PR to fix tests.

**Key commands:**
```bash
make unit-tests        # Run unit tests WITH CACHING (preferred)
make integration-tests # Run integration tests WITH CACHING (preferred)
make pre-push          # Run full validation (reuses unit/integration caches)
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
│   ├── flows/                        # Automation flow diagrams (replaces Node-RED visuals)
│   │   └── DAY_PHASE_MODES.md        # Day phase, music mode, lighting relationships
│   ├── reference/SHADOW_STATE.md     # Shadow state pattern (READ BEFORE WRITING PLUGINS)
│   ├── reference/PLUGIN_SYSTEM.md    # Plugin interfaces
│   ├── reference/migration_mapping.md # State variable mapping
│   └── reference/CONCURRENCY_LESSONS.md # Concurrency patterns
├── docs/archive/flows.json     # Node-RED legacy implementation (archived)
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
| [DAY_PHASE_MODES.md](./docs/flows/DAY_PHASE_MODES.md) | Day phase, music mode, and lighting relationships |

## Understanding Node-RED Behavior

Before implementing features, understand the current Node-RED behavior.

**⚠️ WARNING:** `docs/archive/flows.json` is ~650KB. Do NOT read it all at once. Use targeted searches:

```bash
# Search patterns
grep -A 5 '"label":"Music"' docs/archive/flows.json              # Find a flow
grep -A 20 '"name":"Pick Appropriate Music"' docs/archive/flows.json  # Find function node
grep -n "isNickHome" docs/archive/flows.json                     # Find state variable usage
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

### Shadow State Pattern (Required for All Plugins)

**Every plugin MUST implement shadow state tracking.** See [SHADOW_STATE.md](./docs/reference/SHADOW_STATE.md).

Most plugins use `SubscriptionHelper`, which automatically captures shadow inputs before each handler. This is the recommended approach:

```go
// RECOMMENDED: SubscriptionHelper captures inputs automatically
m.subHelper.SubscribeToState("dayPhase", func(key string, oldValue, newValue interface{}) {
    // Shadow inputs already captured — just process and update outputs
    m.processChange(newValue)
    m.shadowTracker.UpdateSomeOutput(result)
})
m.subHelper.CaptureInitialInputs()
```

For periodic/timer-based plugins that don't use subscriptions (e.g., `dayphase`, `sleephygiene`), call `updateShadowInputs()` manually at the start of each handler.

**Checklist for new plugins:**
- [ ] Add `shadowTracker` field to Manager struct
- [ ] Add `subHelper` field (`*shadowstate.SubscriptionHelper`)
- [ ] Register subscriptions via `subHelper.SubscribeToState()` / `SubscribeToEntity()`
- [ ] Call `subHelper.CaptureInitialInputs()` after setup
- [ ] Test that `/api/shadow/{plugin}` shows populated inputs

### Go Code Standards

**Style:** Follow `gofmt`, use `staticcheck`, 120 char max line length, godoc comments on exports.

**Scope Discipline:** Solve what was asked, nothing more. Don't add helpers or abstractions for one-time operations. Don't refactor adjacent code. Three similar lines is better than a premature abstraction.

**Testing:** 65% minimum coverage, table-driven tests, always use `-race` flag.

**Error Handling:** Always check errors, wrap with context (`fmt.Errorf("context: %w", err)`), never panic.

**Concurrency:** Protect shared state with mutexes, use `sync.RWMutex` for read-heavy ops, **serialize WebSocket writes with `writeMu`**.

### TDD for Cross-Plugin Features

**When implementing features that span multiple plugins, write user story tests FIRST.**

Cross-plugin bugs often slip through unit tests because unit tests set up state atomically and don't test timing windows between state changes. User story tests validate behavior from the user's perspective.

**When to write user story tests:**
- Features involving 2+ plugins (e.g., sleephygiene + music, lighting + presence)
- Features with timing dependencies (e.g., "when alarm triggers, music fades out over 5 minutes")
- Features with invariants that must ALWAYS hold (e.g., "sleep music never restarts during wake sequence")

**TDD workflow for cross-plugin features:**

```
1. Write the user story: "When my alarm goes off, I want gentle wake-up music"
2. Identify invariants: "sleep music must NOT restart while isWakeSequenceActive=true"
3. Write a test in test/integration/scenario_*_test.go that will FAIL (it will)
4. Implement the feature across plugins
5. Test passes when user expectation is met
```

**Test structure (GIVEN/WHEN/THEN):**

```go
func TestScenario_UserStory_WakeSequenceDoesNotRestartSleepMusic(t *testing.T) {
    // GIVEN: Master is asleep, playing sleep music, alarm time reached
    t.Log("GIVEN: Someone is asleep with sleep music playing")
    server.SetState("input_boolean.master_asleep", "on", nil)
    server.SetState("input_text.music_playback_type", "sleep", nil)

    // WHEN: Wake sequence begins
    t.Log("WHEN: Wake sequence starts (isWakeSequenceActive=true)")
    server.SetState("input_boolean.wake_sequence_active", "on", nil)

    // THEN: Music plugin must NOT restart sleep music
    t.Log("THEN: Sleep music is NOT restarted")
    // ... assertions validating the invariant holds
}
```

**Key patterns for user story tests:**
- **Timeline testing**: Simulate T+0, T+2min, T+5min states to catch timing bugs
- **Invariant assertions**: Document rules that should ALWAYS hold in comments
- **Intermediate state testing**: Don't just test end states—test what happens during transitions

**Reference:** See `homeautomation-go/test/integration/scenario_sleephygiene_test.go:154-197` for examples of regression tests with GIVEN/WHEN/THEN structure and timeline testing.

### API Change Protocol

When modifying function signatures:
1. **Search** for all call sites: `grep -r "FunctionName" .`
2. **Update** ALL call sites (code + tests + docs)
3. **Compile check**: `go build ./...`
4. **Run ALL tests**: `make unit-tests && make integration-tests` (uses caching)

### Documentation Maintenance

Update docs when making changes:
- New plugin → Update `docs/human/VISUAL_ARCHITECTURE.md`, `docs/architecture/ARCHITECTURE.md`, consider adding flow doc to `docs/flows/`
- New state variable → Update `docs/reference/migration_mapping.md`, `docs/human/VISUAL_ARCHITECTURE.md`
- Plugin logic change → Update relevant flow doc in `docs/flows/` (e.g., `DAY_PHASE_MODES.md`)
- Concurrency fix → Update `docs/reference/CONCURRENCY_LESSONS.md`

Validate Mermaid diagrams: `make validate-mermaid`

### Diagram Maintenance Protocol

**Mermaid diagrams in `docs/human/VISUAL_ARCHITECTURE.md` and `docs/flows/` must stay synchronized with code.**

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
| Plugin logic changed | Relevant logic flow diagram (Music, Lighting, Energy, etc.) or `docs/flows/` |
| Day phase/schedule changes | `docs/flows/DAY_PHASE_MODES.md` |

**Diagram update checklist:**
1. Identify affected diagrams using the table above
2. Update the Mermaid source in `docs/human/VISUAL_ARCHITECTURE.md` or relevant `docs/flows/*.md` file
3. Run `make validate-mermaid` to check syntax
4. Preview rendering (GitHub renders Mermaid natively, or use [Mermaid Live](https://mermaid.live/))
5. Commit diagram updates **in the same PR** as code changes

**Key diagram sections:**
- **System Architecture** - High-level component overview
- **Plugin System Architecture** - Plugin dependencies and data flow
- **State Variable Dependency Graph** - Which plugins read/write which variables
- **Individual Logic Flows** - Decision trees for Music, Lighting, Energy, etc.
- **Automation Flows** (`docs/flows/`) - Detailed behavior documentation with Mermaid diagrams

## Running Tests

**Use cached Makefile targets for tests** - they skip redundant runs when code hasn't changed:

```bash
# PREFERRED: Uses test caching (skips if code unchanged since last pass)
make unit-tests           # Unit tests with caching
make integration-tests    # Integration tests with caching

# No caching (runs every time)
make test-no-cache        # All tests with race detection (no cache)

# Cache management (see "Troubleshooting Test Cache" below for details)
make cache-status                # Check cache state
make cache-clear                 # Force re-run next time
make cache-clear-unit            # Clear unit test cache only
make cache-clear-integration     # Clear integration test cache only
```

**How caching works:** The `.githooks/test-cache.sh` script tracks a content-based hash of the codebase. If nothing changed since the last successful test run, tests are skipped entirely. This saves significant time during development.

**Troubleshooting Test Cache:**

```bash
# Check cache status
.githooks/test-cache.sh --status

# Clear cache if tests seem stale
.githooks/test-cache.sh --clear       # Clear all caches
.githooks/test-cache.sh --clear-one unit-tests  # Clear unit test cache only

# Debug cache misses
DEBUG_CACHE=1 make unit-tests
# Shows why cache missed (hash mismatch, file changes, etc.)
```

**When to clear cache:**
- After `git pull` (especially if others changed tests)
- When test fixtures or mock data change
- When debugging inconsistent test results
- Never needed for normal development (cache auto-invalidates on code changes)

**Direct commands (for one-off debugging):**
```bash
cd homeautomation-go
go test -race ./...                           # Run tests directly
go test -coverprofile=coverage.out ./...      # Coverage report
go test -v -race ./test/integration/...       # Integration only
```

**Test Status:** All 11 integration tests passing ✅

### Troubleshooting Test Cache

**Check cache status:**
```bash
make cache-status                # Show all cache hashes and current state
```

**Clear cache when tests seem stale:**
```bash
make cache-clear-unit            # Clear unit test cache only
make cache-clear-integration     # Clear integration test cache only
make cache-clear                 # Clear all test caches
```

**Debug cache misses:**
```bash
DEBUG_CACHE=1 make unit-tests
# Shows: current state hash, cached state hash, cache file path
# Useful for understanding why the cache missed (hash mismatch, missing cache file, etc.)
```

**When to clear cache:**
- After `git pull` (especially if others committed code)
- When test fixtures or mock data change outside the Go source tree
- When debugging inconsistent test results
- Never needed for normal development (cache auto-invalidates on code changes)

**How the cache works:** The cache computes a content-based hash of the entire working tree (using `git rev-parse HEAD^{tree}` for clean trees, or `git stash create` for dirty trees). If the hash matches the last successful test run, tests are skipped. Any code change automatically invalidates the cache.

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

## CI Runner (Self-Hosted GitHub Actions)

Self-hosted GitHub Actions runner on `dockergeneric` for home-automation CI. Two containers share a network namespace: Tailscale sidecar (`ci-runner-ts`) and the runner itself (`ci-runner`).

- **Network isolation:** `tag:ci-runner` ACL grants Internet-only access — no access to any internal Tailscale services
- **Docker:** Runner mounts the host Docker socket (`/var/run/docker.sock`) and bind-mounts `/tmp/runner-work` so CI workflow bind mounts resolve correctly
- **Image:** `myoung34/github-runner:2.332.0-ubuntu-jammy`
- **Config:** `/etc/container-configs/ci-runner/.env` on `dockergeneric` (contains GitHub PAT)
- **Deployment:** `make deploy-dockergeneric`
- **Details:** See `services/dockergeneric/ci-runner/README.md`

**Query CI runner logs in Gravwell:**
```bash
curl -s -X POST \
  -H "Gravwell-Token: $(tr -d '\n' < ./gravwell.token)" \
  -H 'query: tag=ci-runner | text' \
  -H 'duration: 1h' \
  -H 'format: text' \
  'https://gravwell.featherback-mermaid.ts.net/api/search/direct'
```

## Production Debugging

**Dashboard (view current state):** https://home-automation.featherback-mermaid.ts.net/

**SoCo-CLI HTTP API (Sonos speaker control):** https://soco-cli.featherback-mermaid.ts.net/

**View logs via Gravwell:** https://gravwell.featherback-mermaid.ts.net/

Logs are centralized in Gravwell. Authenticate using the token stored in `./gravwell.token`.

**Log tags:**
| Tag | Description |
|-----|-------------|
| `tag=home-automation` | Home automation Go application logs |
| `tag=home-assistant` | Home Assistant entity state changes (excludes `sensor.*` entities - too noisy) |
| `tag=soco` | SoCo-CLI HTTP API server logs (Tidal playback via Sonos) |
| `tag=ci-runner` | Self-hosted GitHub Actions runner logs |

**Version Verification (first debugging step):**

When debugging production issues, first confirm the running code matches what you expect:

```bash
curl -s -X POST \
  -H "Gravwell-Token: $(tr -d '\n' < ./gravwell.token)" \
  -H "query: tag=home-automation grep \"Home Automation starting\"" \
  -H "duration: 24h" \
  -H "format: text" \
  "https://gravwell.featherback-mermaid.ts.net/api/search/direct"
```

Example output:
```json
{"level":"info","ts":"2026-03-09T00:30:56.858Z","msg":"Home Automation starting","commit":"2df98a08...","branch":"main","build_time":"2026-03-08T19:09:45Z","dirty":"false"}
```

- **`commit`**: Compare against `git log` in this repo to verify the deployed version
- **`branch`**: Should be `main` in production
- **`build_time`**: When the binary was compiled
- **`dirty`**: If `true`, the binary was built from a working tree with uncommitted changes — the `commit` hash alone won't fully represent what's running

**Example queries:**
```
tag=home-automation
tag=home-assistant
tag=soco                            # SoCo-CLI logs
tag=home-automation,home-assistant  # Both sources
```

**API Access (for Claude/programmatic use):**

Use the Direct Query API to fetch logs programmatically. Documentation: https://docs.gravwell.io/search/directquery/directquery.html

```bash
# Query home-automation logs (last hour, text format)
curl -s -X POST \
  -H "Gravwell-Token: $(tr -d '\n' < ./gravwell.token)" \
  -H "query: tag=home-automation" \
  -H "duration: 1h" \
  -H "format: text" \
  "https://gravwell.featherback-mermaid.ts.net/api/search/direct"

# Query home-assistant logs (last 5 minutes)
curl -s -X POST \
  -H "Gravwell-Token: $(tr -d '\n' < ./gravwell.token)" \
  -H "query: tag=home-assistant" \
  -H "duration: 5m" \
  -H "format: text" \
  "https://gravwell.featherback-mermaid.ts.net/api/search/direct"

# Query SoCo-CLI logs (last 30 minutes)
curl -s -X POST \
  -H "Gravwell-Token: $(tr -d '\n' < ./gravwell.token)" \
  -H "query: tag=soco" \
  -H "duration: 30m" \
  -H "format: text" \
  "https://gravwell.featherback-mermaid.ts.net/api/search/direct"

# Filter for error-level logs using JSON field extraction
curl -s -X POST \
  -H "Gravwell-Token: $(tr -d '\n' < ./gravwell.token)" \
  -H "query: tag=home-automation json level==error" \
  -H "duration: 1h" \
  -H "format: text" \
  "https://gravwell.featherback-mermaid.ts.net/api/search/direct"
```

**Example output (home-automation):** Structured JSON logs from the Go application
```json
{"level":"info","ts":"2026-01-03T16:02:18.729Z","logger":"energy","msg":"Starting calibration cycle","device_count":3}
{"level":"info","ts":"2026-01-03T16:00:33.761Z","logger":"energy","msg":"Calibration cycle complete"}
{"level":"info","ts":"2026-01-03T15:59:15.675Z","logger":"energy","msg":"Determined battery energy level","percentage":83,"level":"green"}
```

**Example output (home-assistant):** Entity state changes from Home Assistant (excludes `sensor.*`)
```
<14>1 2026-01-03T10:47:31-06:00 home-assistant homeassistant - - - media_player.sony_xr_65a80k: on → off
<14>1 2026-01-03T10:46:52-06:00 home-assistant homeassistant - - - select.span_right_hvac_and_well_kids_hot_water_heater_circuit_priority: unavailable → Nice To Have
<14>1 2026-01-03T10:45:37-06:00 home-assistant homeassistant - - - binary_sensor.span_right_hvac_and_well_door_state: unavailable → unknown
```

**Example output (soco):** SoCo-CLI HTTP API server logs
```
<27>1 2026-03-09T00:33:43Z dockergeneric soco 875 soco - INFO:     Uvicorn running on http://0.0.0.0:8000 (Press CTRL+C to quit)
<27>1 2026-03-09T00:33:43Z dockergeneric soco 875 soco - INFO:     Application startup complete.
<27>1 2026-03-09T00:33:43Z dockergeneric soco 875 soco - INFO:     Started server process [1]
```

**API Parameters:**
| Parameter | Description |
|-----------|-------------|
| `query` | Gravwell query (e.g., `tag=home-automation grep pattern`) |
| `duration` | Time range using Go duration syntax (`5m`, `1h`, `24h`) |
| `format` | Output format: `text`, `json`, `csv` |

## Testing SoCo-CLI Changes

When modifying the SoCo-CLI integration (`homeautomation-go/internal/plugins/music/sococli.go` or `speaker_commands.go`), you can test against the live API at `https://soco-cli.featherback-mermaid.ts.net/`.

### API Overview

The SoCo-CLI HTTP API (v0.4.82) provides direct UPnP control of Sonos speakers. All endpoints are `GET` requests with path-based parameters. Swagger docs are available at `/docs`.

**Response format** (Go struct `SoCoResponse` in `sococli.go` captures all fields except `args`):
```json
{"speaker": "Kitchen", "action": "volume", "args": [], "exit_code": 0, "result": "9", "error_msg": ""}
```

### Read-Only Test Commands

These commands query state without modifying anything:

```bash
# List all discovered speakers
curl -s https://soco-cli.featherback-mermaid.ts.net/speakers | jq

# Get current volume (read-only)
curl -s https://soco-cli.featherback-mermaid.ts.net/Kitchen/volume | jq
curl -s "https://soco-cli.featherback-mermaid.ts.net/Front%20Room/volume" | jq

# Check playback status
curl -s https://soco-cli.featherback-mermaid.ts.net/Bedroom/playback | jq

# View speaker groups
curl -s https://soco-cli.featherback-mermaid.ts.net/Kitchen/groups | jq

# Force speaker rediscovery
curl -s https://soco-cli.featherback-mermaid.ts.net/rediscover | jq
```

### Write Commands (Use With Care)

These commands change speaker state. Use only when actively testing:

```bash
# Set volume (0-100)
curl -s https://soco-cli.featherback-mermaid.ts.net/Kitchen/volume/10 | jq

# Mute / unmute
curl -s https://soco-cli.featherback-mermaid.ts.net/Kitchen/mute | jq
curl -s https://soco-cli.featherback-mermaid.ts.net/Kitchen/mute/off | jq

# Group / ungroup speakers
curl -s "https://soco-cli.featherback-mermaid.ts.net/Kitchen/group/Front%20Room" | jq
curl -s https://soco-cli.featherback-mermaid.ts.net/Kitchen/ungroup | jq

# Playback control
curl -s https://soco-cli.featherback-mermaid.ts.net/Kitchen/play | jq
curl -s https://soco-cli.featherback-mermaid.ts.net/Kitchen/pause | jq

# Shuffle / repeat
curl -s https://soco-cli.featherback-mermaid.ts.net/Kitchen/shuffle/on | jq
curl -s https://soco-cli.featherback-mermaid.ts.net/Kitchen/repeat/all | jq

# Tidal playback (the full sequence used by PlayShareLink)
curl -s "https://soco-cli.featherback-mermaid.ts.net/Front%20Room/clear_queue" | jq
curl -s "https://soco-cli.featherback-mermaid.ts.net/Front%20Room/sharelink/https%3A%2F%2Ftidal.com%2Fbrowse%2Fplaylist%2F..." | jq
curl -s "https://soco-cli.featherback-mermaid.ts.net/Front%20Room/play_from_queue" | jq
```

### Available Speakers

These speakers are discovered by SoCo and match the `player_name` values in `configs/music_config.yaml`:

| Speaker | In Music Config | Notes |
|---------|----------------|-------|
| Front Room | Yes | Typical group leader |
| Kitchen | Yes | |
| Kids Bathroom | Yes | |
| Bedroom | Yes | Excluded from some zones when master is asleep |
| Sitting Room | Yes | |
| Primary Bathroom | Yes | |
| Barn | No | Not used in music automation |
| Soundbar | No | Not used in music automation |

### Key Implementation Details

- **URL encoding:** Speaker names with spaces use `url.PathEscape()` (e.g., `Front%20Room`). The API handles this correctly.
- **Fallback:** If `SOCO_CLI_URL` env var is unset, all speaker commands fall back to Home Assistant WebSocket API (`speaker_commands.go` dual-path routing).
- **Timeouts:** Standard operations use 5s per-attempt timeout; `sharelink` uses 30s (downloads and enqueues content).
- **Retries:** Network errors and HTTP 5xx/429 retry up to 3 total attempts with 500ms delay. Application errors (`exit_code != 0`) and HTTP 4xx do not retry.
- **Read-only mode:** When `READ_ONLY=true`, all SoCo commands are logged but not executed.

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

Use Playwright to capture screenshots for PR documentation or visual regression testing.

**Quick Method (using Playwright skill if installed):**
```bash
make dev-ui
# Ask Claude Code to use the playwright-skill to capture a screenshot
```

**Manual Method (requires Node.js):**

**1. Start the dev server:**
```bash
make dev-ui
```

**2. Install Playwright:**
```bash
npm install -D @playwright/test playwright
```

**3. Capture a screenshot (laptop resolution example):**
```bash
cat > /tmp/capture-dashboard.js << 'EOF'
const { chromium } = require('playwright');

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage();

  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto('http://localhost:8080/dashboard', { waitUntil: 'networkidle' });

  await page.screenshot({
    path: 'docs/screenshots/dashboard.png',
    fullPage: true
  });

  console.log('Screenshot saved to docs/screenshots/dashboard.png');
  await browser.close();
})();
EOF

node /tmp/capture-dashboard.js
```

**4. Responsive testing (multiple viewports):**
```bash
cat > /tmp/capture-responsive.js << 'EOF'
const { chromium } = require('playwright');

const viewports = [
  { name: 'desktop', width: 1920, height: 1080 },
  { name: 'laptop', width: 1280, height: 800 },
  { name: 'tablet', width: 768, height: 1024 },
  { name: 'mobile', width: 375, height: 667 }
];

(async () => {
  const browser = await chromium.launch();
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

node /tmp/capture-responsive.js
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
| Test timeout after 5m/10m | CI under heavy load, or infinite loop | Set `TEST_WAIT_TIMEOUT=5s`, check for deadlocks with `go test -race -v` |

## Status

**Current Phase:** Production ✅

- Go implementation is the primary automation system
- All 41 state variables supported
- Node-RED deprecated (flows.json archived for reference)

---

**Last Updated:** 2026-01-10
**Go Version:** 1.24
**Project Status:** Production
