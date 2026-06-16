#!/usr/bin/env bash
# Builds, pushes and cosign-signs the app image so the calling workflow needs only the statio
# action — no separate docker/build-push or cosign steps. Backward compatible: when the caller
# already passed a digest (they built the image themselves) we skip building and just sign it.
#
# Signing keyless here uses the run's OIDC identity — the SAME identity the agent verifies — so
# moving the build/sign inside the action does not change the security model.
set -euo pipefail

image="$STATIO_IMAGE"
digest="${STATIO_DIGEST:-}"

if [[ -z "$digest" ]]; then
  dockerfile="${STATIO_DOCKERFILE:-Dockerfile}"
  context="${STATIO_CONTEXT:-.}"
  tag="${STATIO_TAG:-latest}"
  registry="${image%%/*}"   # ghcr.io from ghcr.io/org/app

  echo "::group::statio: build & push ${image}:${tag}"
  if [[ -n "${STATIO_REGISTRY_PASSWORD:-}" ]]; then
    echo "$STATIO_REGISTRY_PASSWORD" | docker login "$registry" -u "${STATIO_REGISTRY_USERNAME:-}" --password-stdin
  fi
  docker build -t "${image}:${tag}" -f "$dockerfile" "$context"
  docker push "${image}:${tag}"
  # RepoDigests is populated after a successful push: "ghcr.io/org/app@sha256:…".
  repodigest="$(docker inspect --format='{{index .RepoDigests 0}}' "${image}:${tag}")"
  digest="${repodigest##*@}"
  echo "Resolved digest: $digest"
  # Hand the digest to the deploy step (which reads STATIO_RESOLVED_DIGEST when no digest input).
  echo "STATIO_RESOLVED_DIGEST=$digest" >> "$GITHUB_ENV"
  echo "::endgroup::"
fi

if [[ -z "$digest" ]]; then
  echo "statio: could not determine the image digest" >&2
  exit 1
fi

if [[ "${STATIO_SIGN:-true}" == "true" ]]; then
  echo "::group::statio: cosign sign ${image}@${digest}"
  cosign sign --yes "${image}@${digest}"
  echo "::endgroup::"
fi
