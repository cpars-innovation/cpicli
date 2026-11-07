#!/usr/bin/env bash
# Lists the modules linked into cpictl with their licenses and copies the
# license texts to licenses/third-party/ (for binary distributions).
set -euo pipefail
cd "$(dirname "$0")/.."
out=licenses/THIRD_PARTY_NOTICES.md
notes="$(sed -n '/^## Notes/,$p' "$out" 2>/dev/null || true)"
rm -rf licenses/third-party && mkdir -p licenses/third-party
{
  echo "# Third-party software in cpictl"
  echo
  echo "The cpictl binary links the following modules. Each module's license text, and its NOTICE file where it has one (Apache-2.0), is in [third-party/](third-party/)."
  echo
  echo "| Module | Version | License |"
  echo "|---|---|---|"
} > "$out"
go list -deps -f '{{if not .Standard}}{{with .Module}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}' ./cmd/cpictl |
  sort -u | grep -v '^github.com/cpars-innovation/cpicli ' |
  while read -r path ver dir; do
    kind=""
    for f in $(ls "$dir" | grep -i -E '^(licen[cs]e|copying|unlicense|notice)' || true); do
      t="$(head -c 3000 "$dir/$f")"
      case "$f" in [Nn][Oo][Tt][Ii][Cc][Ee]*)
        # Apache-2.0: a module's NOTICE file is passed on, not a license
        cp "$dir/$f" "licenses/third-party/$(echo "$path" | tr '/' '_')__$f"
        continue ;;
      esac
      case "$t" in
        *"Apache License"*) k=Apache-2.0 ;;
        *"Mozilla Public License"*) k=MPL-2.0 ;;
        *"Permission is hereby granted"*) k=MIT ;;
        *"Redistribution and use"*) k=BSD ;;
        *) k=other ;;
      esac
      kind="${kind:+$kind + }$k"
      cp "$dir/$f" "licenses/third-party/$(echo "$path" | tr '/' '_')__$f"
    done
    echo "| \`$path\` | $ver | ${kind:-?} |" >> "$out"
  done
[ -n "$notes" ] && { echo; echo "$notes"; } >> "$out"
