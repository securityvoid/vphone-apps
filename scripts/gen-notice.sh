#!/usr/bin/env bash
# Generates NOTICE (repo root) and its embed copy internal/notice/NOTICE for every
# module linked into the binary (`go list -deps ./cmd/vphone-apps`), reading license
# files from the module cache. Needs a populated module cache; changes no repo file
# other than the two outputs.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
export LC_ALL=C

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# path<TAB>version<TAB>dir<TAB>replace-path for every linked non-main module (replace target's dir).
go list -deps -json ./cmd/vphone-apps | jq -r 'select(.Module != null and ((.Module.Main // false) | not)) | .Module | [.Path, (.Replace.Version // .Version), (.Replace.Dir // .Dir // ""), (.Replace.Path // "")] | @tsv' | sort -u > "$work/mods.tsv"

license_file() { # dir -> first LICENSE*/LICENCE*/COPYING* file, case-insensitive
  find "$1" -maxdepth 1 -type f \( -iname 'LICENSE*' -o -iname 'LICENCE*' -o -iname 'COPYING*' -o -iname 'UNLICENSE*' \) | sort | head -n 1
}

license_name() { # file -> known title, else first line
  local n
  n="$(grep -m1 -E 'MIT License|Apache License|BSD|ISC|Mozilla Public License' "$1" || true)"
  if [ -z "$n" ]; then n="$(head -n 1 "$1")"; fi
  printf '%s' "$n" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//'
}

emit_file() { cat "$1"; printf '\n'; }

out="$work/NOTICE"
{
  cat <<'HDR'
NOTICE

vphone-apps is an assembly of other people's open-source work: it uses ipatool
as a library to talk to the App Store, unfairplay as a library to remove
FairPlay encryption, and vphone-cli (through the vphoned guest daemon's
documented control protocol) to install apps on a virtual iPhone. It also
depends on the modules listed below. All of their licenses are reproduced here.

vphone-apps itself is released under the MIT License (reproduced first below).

Inspired by the darkwirelabs.com article at https://www.darkwirelabs.com/articles/from-app-store-to-virtual-iphone-with-vphone/; it is not a dependency.

unfairplay is built from the fork github.com/securityvoid/unfairplay because
DrWhax/unfairplay PR #28 (https://github.com/DrWhax/unfairplay/pull/28), which
exposes its decrypt and setup entry points as importable packages, is not yet
merged.

================================================================================
vphone-apps
================================================================================

--- vphone-apps ---
License: MIT License

HDR
  emit_file LICENSE
  cat <<'HDR'

================================================================================
Direct upstreams
================================================================================
HDR

  # ipatool
  d="$(awk -F'\t' '$1=="github.com/majd/ipatool/v2"{print $3}' "$work/mods.tsv")"
  f="$(license_file "$d")"; [ -n "$f" ] || { echo "no license file: github.com/majd/ipatool/v2" >&2; exit 1; }
  printf '\n--- ipatool (github.com/majd/ipatool/v2) ---\nRepository: https://github.com/majd/ipatool\nLicense: %s\n\n' "$(license_name "$f")"
  emit_file "$f"

  # unfairplay (the replace target dir; both module paths are named)
  line="$(awk -F'\t' '$1=="github.com/DrWhax/unfairplay"{print}' "$work/mods.tsv")"
  d="$(printf '%s' "$line" | cut -f3)"; rp="$(printf '%s' "$line" | cut -f4)"
  printf '\n--- unfairplay (github.com/DrWhax/unfairplay'
  if [ -n "$rp" ]; then printf ' => %s' "$rp"; fi
  printf ') ---\nRepository: https://github.com/DrWhax/unfairplay\nLicense: MIT, with Apache-2.0 (DarkPhoenix) and MIT (macOSFairFree) components\n\n'
  for u in NOTICE LICENSE LICENSE-DARKPHOENIX.txt LICENSE-MACOSFAIRFREE.txt; do
    [ -f "$d/$u" ] || { echo "missing $u in unfairplay module $d" >&2; exit 1; }
    printf '[%s]\n\n' "$u"
    emit_file "$d/$u"
  done

  # vphone-cli
  printf '\n--- vphone-cli / vphoned (github.com/Lakr233/vphone-cli) ---\nRepository: https://github.com/Lakr233/vphone-cli\nLicense: MIT (not a Go dependency; used through its documented vphone.sock protocol)\n\n'
  tail -n +2 scripts/licenses/vphone-cli.LICENSE
  printf '\n'

  cat <<'HDR'

================================================================================
Dependencies (every module linked into the binary)
================================================================================
HDR
  while IFS=$'\t' read -r path version dir _; do
    case "$path" in github.com/majd/ipatool/v2|github.com/DrWhax/unfairplay) continue ;; esac
    f=""
    if [ -n "$dir" ]; then f="$(license_file "$dir")"; fi
    [ -n "$f" ] || { echo "no license file: $path" >&2; exit 1; }
    printf '\n--- %s %s ---\nLicense: %s\n\n' "$path" "$version" "$(license_name "$f")"
    emit_file "$f"
  done < "$work/mods.tsv"
} > "$out"

mkdir -p internal/notice
cp "$out" "$work/NOTICE.root" && cp "$out" "$work/NOTICE.embed"
mv "$work/NOTICE.root" NOTICE
mv "$work/NOTICE.embed" internal/notice/NOTICE
