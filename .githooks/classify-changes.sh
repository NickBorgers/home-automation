#!/bin/bash
#
# Decide how much validation a set of changed files needs.
#
# Input:  changed file paths on stdin, one per line (repo-relative).
# Output: two lines, safe to append to $GITHUB_OUTPUT:
#           tier=docs|scoped|full
#           packages=<space-separated Go package patterns>   (only for tier=scoped)
#
# Tiers:
#   docs    Only documentation changed. Run diagram validation only.
#   scoped  Only config files changed, and no integration test reads them from disk.
#           Run yamllint, diagram validation, and the unit tests of the listed packages.
#   full    Anything else, including anything this script does not know. Run everything.
#
# A config file is "scoped" only if BOTH are true (verify with grep before adding one):
#   1. test/integration reads no real copy of it. Integration tests use testdata/ copies,
#      except configs/schedule_config.yaml, which they load from configs/.
#   2. The unit tests that read it are in the listed packages.
#
# The result is the highest tier across all files: full > scoped > docs.

tier=docs
packages=""

add_package() {
    case " $packages " in
        *" $1 "*) ;;
        *) packages="${packages:+$packages }$1" ;;
    esac
}

while IFS= read -r file; do
    [ -z "$file" ] && continue
    case "$file" in
        .github/workflows/*)
            tier=full
            ;;
        *.md | docs/* | LICENSE | LICENSE.* | .github/* | *.txt)
            ;;
        configs/pico_config.yaml)
            [ "$tier" = docs ] && tier=scoped
            add_package ./internal/plugins/picos/...
            ;;
        configs/hue_config.yaml)
            [ "$tier" = docs ] && tier=scoped
            add_package ./internal/plugins/lighting/...
            ;;
        configs/music_config.yaml)
            [ "$tier" = docs ] && tier=scoped
            add_package ./internal/plugins/music/...
            ;;
        configs/energy_config.yaml)
            [ "$tier" = docs ] && tier=scoped
            add_package ./internal/plugins/energy/...
            ;;
        *)
            tier=full
            ;;
    esac
done

echo "tier=$tier"
if [ "$tier" = scoped ]; then
    echo "packages=$packages"
fi
