#!/bin/bash
#
# Table test for classify-changes.sh. Run: make test-classify

cd "$(dirname "$0")" || exit 1

failures=0

# expect "<files, newline separated>" "<expected output with \n>"
expect() {
    local files=$1 want=$2 got
    got=$(printf '%b\n' "$files" | ./classify-changes.sh)
    if [ "$got" != "$(printf '%b' "$want")" ]; then
        echo "FAIL: [$files]"
        echo "  want: $(printf '%b' "$want" | tr '\n' '|')"
        echo "  got:  $(echo "$got" | tr '\n' '|')"
        failures=$((failures + 1))
    fi
}

expect ""                                              "tier=docs"
expect "docs/flows/PICO_BUTTONS.md"                    "tier=docs"
expect "README.md\nLICENSE"                            "tier=docs"
expect ".github/CODEOWNERS"                            "tier=docs"
expect ".github/workflows/pr-tests.yml"               "tier=full"
expect "configs/pico_config.yaml"                      "tier=scoped\npackages=./internal/plugins/picos/..."
expect "configs/pico_config.yaml\ndocs/flows/X.md"     "tier=scoped\npackages=./internal/plugins/picos/..."
expect "configs/pico_config.yaml\nconfigs/hue_config.yaml" \
    "tier=scoped\npackages=./internal/plugins/picos/... ./internal/plugins/lighting/..."
expect "configs/pico_config.yaml\nconfigs/pico_config.yaml" "tier=scoped\npackages=./internal/plugins/picos/..."
expect "configs/music_config.yaml"                     "tier=scoped\npackages=./internal/plugins/music/..."
expect "configs/energy_config.yaml"                    "tier=scoped\npackages=./internal/plugins/energy/..."
# schedule_config.yaml is read by scenario tests: it must run everything.
expect "configs/schedule_config.yaml"                  "tier=full"
# Configs with no known test mapping default to full.
expect "configs/sensor_config.yaml"                    "tier=full"
expect "configs/brand_new_config.yaml"                 "tier=full"
# Any code change wins over a scoped config change, in either order.
expect "configs/pico_config.yaml\nhomeautomation-go/internal/plugins/picos/manager.go" "tier=full"
expect "homeautomation-go/internal/plugins/picos/manager.go\nconfigs/pico_config.yaml" "tier=full"
expect "homeautomation-go/test/integration/scenario_x_test.go" "tier=full"
expect "homeautomation-go/go.mod"                      "tier=full"
expect "Makefile"                                      "tier=full"
expect ".githooks/pre-push"                            "tier=full"
expect "docs/a.md\nMakefile"                           "tier=full"

if [ "$failures" -gt 0 ]; then
    echo "$failures classify-changes case(s) failed"
    exit 1
fi
echo "✅ classify-changes: all cases passed"
