#!/usr/bin/env bash
# Assembles the deploy invocation from the action inputs (passed via env) and runs
# `push deploy`, which parses push.yaml, builds the payload, cosign-signs it, and sends the
# signed envelope. Secrets travel via env (PUSH_ENV_OVERRIDES), never argv. We do not use
# `set -x` (it would echo values); ::add-mask:: is belt-and-suspenders for masking.
set -euo pipefail

args=(
  --target "$PUSH_TARGET"
  --service "$PUSH_SERVICE"
  --image "$PUSH_IMAGE"
  --digest "$PUSH_DIGEST"
  --deploy-seq "$PUSH_DEPLOY_SEQ"
  --timeout "$PUSH_TIMEOUT"
)

[[ -n "${PUSH_FILE:-}" ]] && args+=(--push-file "$PUSH_FILE")
[[ -n "${PUSH_AUDIENCE:-}" ]] && args+=(--audience "$PUSH_AUDIENCE")
[[ "${PUSH_STRICT:-false}" == "true" ]] && args+=(--strict)

# Mask each override value in logs as defense in depth (real masking comes from sourcing
# the values from ${{ secrets.* }} in the workflow).
if [[ -n "${PUSH_ENV_OVERRIDES:-}" ]]; then
  while IFS= read -r line; do
    [[ -z "$line" || "$line" == \#* ]] && continue
    val="${line#*=}"
    [[ -n "$val" ]] && echo "::add-mask::$val"
  done <<< "$PUSH_ENV_OVERRIDES"
fi

# PUSH_ENV_OVERRIDES is already in the environment; `push deploy` reads it.
exec push deploy "${args[@]}"
