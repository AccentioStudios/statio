#!/usr/bin/env bash
# Runs `statio preflight` — a READ-ONLY dry-run against the agent — BEFORE the real deploy, so a
# misconfiguration (a host-port conflict with another app, or a reverse-proxy/DNS the statio.yaml
# asks for but the server isn't set up for) fails the job fast and cheap instead of crashing
# mid-deploy. It sends the SAME signed payload the deploy will, but to the agent's /preflight
# endpoint, which changes nothing on the server. Secrets travel via env, never argv.
set -euo pipefail

digest="${STATIO_DIGEST:-}"
[[ -z "$digest" ]] && digest="${STATIO_RESOLVED_DIGEST:-}"
if [[ -z "$digest" ]]; then
  echo "statio: no image digest: pass 'digest' or let the action build the image" >&2
  exit 1
fi

args=(
  --target "$STATIO_TARGET"
  --service "$STATIO_SERVICE"
  --image "$STATIO_IMAGE"
  --digest "$digest"
  --timeout "$STATIO_TIMEOUT"
)

[[ -n "${STATIO_FILE:-}" ]] && args+=(--statio-file "$STATIO_FILE")
[[ -n "${STATIO_AUDIENCE:-}" ]] && args+=(--audience "$STATIO_AUDIENCE")

# Mask each override value in logs (defense in depth; real masking comes from sourcing values
# from ${{ secrets.* }} in the workflow). STATIO_ENV_OVERRIDES is already in the environment.
if [[ -n "${STATIO_ENV_OVERRIDES:-}" ]]; then
  while IFS= read -r line; do
    [[ -z "$line" || "$line" == \#* ]] && continue
    val="${line#*=}"
    [[ -n "$val" ]] && echo "::add-mask::$val"
  done <<< "$STATIO_ENV_OVERRIDES"
fi

exec statio preflight "${args[@]}"
