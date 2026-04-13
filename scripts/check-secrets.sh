#!/usr/bin/env bash
# Scan staged files for common secret patterns. Exit non-zero on match.
# Used by .git/hooks/pre-commit. Can also be run manually:
#   scripts/check-secrets.sh              # scan staged (default)
#   scripts/check-secrets.sh --all        # scan entire working tree
set -euo pipefail

MODE="${1:-staged}"

# Patterns: name|regex
PATTERNS=(
  'OpenAI API key|sk-[A-Za-z0-9]{20,}'
  'OpenAI project key|sk-proj-[A-Za-z0-9_-]{20,}'
  'Anthropic API key|sk-ant-api[0-9]+-[A-Za-z0-9_-]{20,}'
  'AWS access key|AKIA[0-9A-Z]{16}'
  'AWS secret key|aws_secret_access_key[[:space:]]*=[[:space:]]*[A-Za-z0-9/+=]{40}'
  'Google API key|AIza[0-9A-Za-z_-]{35}'
  'GitHub personal token|ghp_[A-Za-z0-9]{20,}'
  'GitHub OAuth token|gho_[A-Za-z0-9]{20,}'
  'GitHub app token|ghs_[A-Za-z0-9]{20,}'
  'Slack token|xox[baprs]-[A-Za-z0-9-]{20,}'
  'Stripe live key|sk_live_[A-Za-z0-9]{20,}'
  'Private key block|-----BEGIN [A-Z ]*PRIVATE KEY-----'
  'Generic hardcoded secret|(api[_-]?key|secret|token|password)[[:space:]]*[:=][[:space:]]*["'"'"'][A-Za-z0-9/_+=-]{24,}["'"'"']'
)

# Files to scan
if [[ "$MODE" == "--all" ]]; then
  FILES=$(git ls-files -co --exclude-standard)
else
  FILES=$(git diff --cached --name-only --diff-filter=ACM)
fi

if [[ -z "$FILES" ]]; then
  exit 0
fi

# Allowlist: example/doc files where placeholders are intentional
ALLOWLIST_REGEX='(\.env\.example$|check-secrets\.sh$|\.md$)'

FOUND=0
while IFS= read -r file; do
  [[ -z "$file" ]] && continue
  [[ ! -f "$file" ]] && continue
  # Skip binaries
  if file "$file" | grep -q 'binary'; then continue; fi
  # Skip allowlisted files
  if [[ "$file" =~ $ALLOWLIST_REGEX ]]; then continue; fi

  for entry in "${PATTERNS[@]}"; do
    name="${entry%%|*}"
    regex="${entry#*|}"
    if matches=$(grep -nEI "$regex" "$file" 2>/dev/null); then
      echo "✗ Secret detected: $name"
      echo "  file: $file"
      echo "$matches" | sed 's/^/    /'
      FOUND=1
    fi
  done
done <<< "$FILES"

if [[ $FOUND -ne 0 ]]; then
  echo ""
  echo "Commit blocked. Remove the secret, rotate it, and try again."
  echo "To bypass (DANGEROUS, only for false positives): git commit --no-verify"
  exit 1
fi

exit 0
