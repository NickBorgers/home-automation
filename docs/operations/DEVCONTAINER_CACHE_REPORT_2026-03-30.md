# Devcontainer Cache Report – GitHub Actions run 23728700999

**Run:** https://github.com/NickBorgers/home-automation/actions/runs/23728700999  
**Jobs analysed:** `build-devcontainer`, `resolve-issue`  
**Report generated on:** 2026-04-01 using `tools/devcontainer_cache_report.py`

## Summary
- The initial `build-devcontainer` job reused **20 existing registry layers** and pushed **9 new layers**. All base apt/Node.js provisioning layers (`#11`–`#15`) were served directly from BuildKit cache.
- The only steps that rebuilt were the expected **Claude Code installation**, **Playwright plugin installation**, and **Playwright browser download** (`#16`–`#24`), which contain time-sensitive network fetches.
- The follow-up `resolve-issue` job hit the cache for every build step (`#11`–`#24`) and therefore **pushed zero layers**, confirming that subsequent runs skip the expensive steps entirely when the cache is warm.

## Method
```
gh run view 23728700999 --log > /tmp/run-23728700999.log
python3 tools/devcontainer_cache_report.py \
  --log /tmp/run-23728700999.log \
  --job build-devcontainer --json > /tmp/build-devcontainer.json
python3 tools/devcontainer_cache_report.py \
  --log /tmp/run-23728700999.log \
  --job resolve-issue --json > /tmp/resolve-issue.json
```

The helper script parses BuildKit step numbers, cache hits, and the registry push output (`Layer already exists` vs `Pushed`) so we do not have to hand-audit the log each time.

## BuildKit Step Breakdown (`build-devcontainer`)

| Step | Cached? | Purpose | Duration |
|------|---------|---------|----------|
| #11 | ✅ | Base apt packages, fonts, utilities | served from cache |
| #12 | ✅ | GitHub CLI apt repository + install | served from cache |
| #13 | ✅ | NodeSource Node.js 20 installation | served from cache |
| #14 | ✅ | Create vscode cache directories | served from cache |
| #15 | ✅ | Switch to `/home/vscode` | served from cache |
| #16 | ❌ | `curl https://claude.ai/install.sh` (installs Claude Code 2.1.75) | 12.4s |
| #17 | ❌ | Install `playwright-skill` plugin via Claude marketplace | 3.5s |
| #18 | ❌ | `npm install` + `npx playwright install chromium` (downloads browsers) | 17.3s |
| #19–#24 | ❌ | Devcontainer feature normalization and docker-in-docker feature install | 1.0–48.7s |

All earlier loader stages (`#1`–`#8`) represent metadata fetches and are expected to complete quickly; they do not materially influence layer reuse.

## Registry Push vs Cache Usage

| Metric | Count | Notes |
|--------|-------|-------|
| Layers reused (`Layer already exists`) | 20 | e.g., `8ccc0040e417`, `4ba56795173a`, `acf20588eb21` |
| Layers pushed | 9 | `391a86ffd889`, `2afcea7e731b`, `9b627ecbe243`, `81be21e7f9db`, `8b8a89735a97`, etc. |

The pushed layers correspond to the steps that download Claude Code artifacts and Playwright browser binaries, which change whenever upstream releases a new version. Everything else was served from the cache.

## Follow-up Job (`resolve-issue`)

The second job in the workflow rebuilt the same Dockerfile immediately after the first job. The script shows **14 cached steps** (#11–#24) and **no new layers pushed**, demonstrating that the cache becomes fully hot once one run completes.

## Conclusion

- The devcontainer workflow is functioning as intended: once the base layers are built, repeated runs reuse the cached image and skip uploading unchanged layers.
- The nine pushed layers are attributable to nightly tool downloads (Claude CLI + Playwright). No action is required unless we decide to pin those assets or prebuild them into a separate layer.
- Future investigations can reuse `tools/devcontainer_cache_report.py` to produce the same breakdown for other runs without manual parsing.
