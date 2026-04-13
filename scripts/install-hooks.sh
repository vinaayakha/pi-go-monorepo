#!/usr/bin/env bash
# Point git at the tracked hooks dir. Run once per clone.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
git config core.hooksPath .githooks
chmod +x .githooks/* scripts/*.sh
touch .setup-complete
echo "core.hooksPath → .githooks"
echo "Hooks active: $(ls .githooks | tr '\n' ' ')"
echo "Marker written: .setup-complete"
