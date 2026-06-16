#!/usr/bin/env bash
# Assembles the deploy invocation from the action inputs (passed via env) and runs
# `statio deploy`, which parses statio.yaml, builds the payload, cosign-signs it, and sends the
# signed envelope. Secrets travel via env (STATIO_ENV_OVERRIDES), never argv. We do not use
# `set -x` (it would echo values); ::add-mask:: is belt-and-suspenders for masking.
set -euo pipefail

# The digest is either passed in (caller built the image) or resolved by build-sign.sh, which
# exports STATIO_RESOLVED_DIGEST into the job env after it builds + pushes.
digest="${STATIO_DIGEST:-}"
[[ -z "$digest" ]] && digest="${STATIO_RESOLVED_DIGEST:-}"
if [[ -z "$digest" ]]; then
  echo "statio: no image digest — pass 'digest' or let the action build the image" >&2
  exit 1
fi

args=(
  --target "$STATIO_TARGET"
  --service "$STATIO_SERVICE"
  --image "$STATIO_IMAGE"
  --digest "$digest"
  --deploy-seq "$STATIO_DEPLOY_SEQ"
  --timeout "$STATIO_TIMEOUT"
)

[[ -n "${STATIO_FILE:-}" ]] && args+=(--statio-file "$STATIO_FILE")
[[ -n "${STATIO_AUDIENCE:-}" ]] && args+=(--audience "$STATIO_AUDIENCE")
[[ "${STATIO_STRICT:-false}" == "true" ]] && args+=(--strict)

# Mask each override value in logs as defense in depth (real masking comes from sourcing
# the values from ${{ secrets.* }} in the workflow).
if [[ -n "${STATIO_ENV_OVERRIDES:-}" ]]; then
  while IFS= read -r line; do
    [[ -z "$line" || "$line" == \#* ]] && continue
    val="${line#*=}"
    [[ -n "$val" ]] && echo "::add-mask::$val"
  done <<< "$STATIO_ENV_OVERRIDES"
fi

# STATIO_ENV_OVERRIDES is already in the environment; `statio deploy` reads it.
exec statio deploy "${args[@]}"
