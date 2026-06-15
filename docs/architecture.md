# Cómo funciona `statio` por dentro

Documento técnico: arquitectura, pipeline, contrato de wire, modelo de seguridad y
referencias completas. Para la guía de uso, ver el [README](../README.md).

> **⚠️ Redesign v2 — config firmada desde el repo.** Varias secciones abajo describen el modelo
> original (manifest + compose server-side, env de dos archivos no firmado). El modelo actual:
> - La config del servicio vive en **`statio.yaml` (en el repo)**; el agente **genera** el compose
>   (allowlist, multi-servicio) — ya no hay compose escrito a mano ni `interp.env`.
> - El wire es un **envelope firmado** `{payload, bundle}`: CI firma el payload con cosign keyless
>   (misma identidad que la imagen) y el agente hace `verify-blob` **antes** de decodificar, con
>   binding de `audience`/`deploy_seq`/`expiry` (anti replay/cross-server).
> - El server mantiene un **`service.yaml` delgado** (anclas: repo permitido, registries, allowlists)
>   + `statio enable`. Los **env values** vienen de GitHub Secrets (**courier**), se escriben en
>   **tmpfs (`/run/statio`)** RAM-only, y el rollback usa snapshots en RAM.
> - Cada deploy deja un **audit log** redactado (`statio logs`).
>
> El diseño completo y los invariantes #14–#24 están en el plan:
> `~/.claude/plans/analiza-el-levantamento-md-para-cuddly-melody.md`.

## Índice

1. [Arquitectura (2 partes)](#1-arquitectura-2-partes)
2. [Las 4 restricciones duras](#2-las-4-restricciones-duras)
3. [El pipeline de deploy](#3-el-pipeline-de-deploy)
4. [El contrato de wire (statio/v1)](#4-el-contrato-de-wire-statiov1)
5. [Modelo de env de dos archivos](#5-modelo-de-env-de-dos-archivos)
6. [Modelo de seguridad](#6-modelo-de-seguridad)
7. [Referencia de archivos](#7-referencia-de-archivos)
8. [Referencia de comandos](#8-referencia-de-comandos)
9. [Referencia de la Action](#9-referencia-de-la-action)
10. [Layout del código](#10-layout-del-código)

## 1. Arquitectura (2 partes)

Son 2 partes en un monorepo, atadas por el contrato `statio/v1` (`internal/spec`):

| Parte | Dónde corre | Qué es |
|-------|-------------|--------|
| **Servidor** | tu server | el binario `statio` como `statio agent run` bajo systemd + `statio init`/`statio env` |
| **Action** | runner de GitHub Actions | `action/` — composite que baja el **mismo** binario y corre `statio deploy` |

La Action baja y corre el mismo binario → la validación/schema viven en **un solo código
Go**, usado por el agente (autoritativo) y por `statio deploy` (fail-fast). El contrato no
puede divergir.

**Tailscale embebido (`tsnet`).** El agente importa `tailscale.com/tsnet`: el binario `statio`
embebe un nodo Tailscale completo (WireGuard userspace + cliente). En el servidor **no se
instala `tailscaled` ni el CLI** — `ts.Up()` une el nodo a la tailnet y `ts.ListenTLS()`
expone el endpoint solo ahí. El estado persiste en `/var/lib/statio/tsnet`. tsnet corre en
netstack userspace; los contenedores del host no están en la tailnet (no hace falta: el
agente habla con NPMplus por localhost y baja la imagen por HTTPS normal). Lo único que opera
un tercero (Tailscale) es el control plane + DERP — restricción #3.

**Dos transportes distintos:**
- La **señal + config** va por la tailnet (privado, cifrado E2E por WireGuard).
- La **imagen** va por HTTPS normal a/desde GHCR (GHCR no está en la tailnet).
- Las llamadas nuevas (Cloudflare HTTPS, NPMplus localhost) son **salientes** — no agregan
  superficie inbound.

## 2. Las 4 restricciones duras

No se negocian. Toda decisión las preserva:

1. **Sin SSH** en ningún momento.
2. El agente **escucha solo en la tailnet** (100.x), nunca en una interfaz pública.
3. **Sin broker/infra central propia.** Las piezas centrales (registry, control plane de
   Tailscale, Sigstore TUF) las opera un tercero.
4. **Sin polling.** El agente recibe un push directo en tiempo real.

Postura adicional: imagen por **digest inmutable**; autenticidad por **firma cosign** (no por
el transporte); el evento lleva **datos**, nunca scripts (no es ejecución remota de código).

## 3. El pipeline de deploy

El agente corre, por servicio y bajo `flock`, un pipeline ordenado:

```
1. admit     decode + schema cerrado + allowlists + repo-equality + protected-keys
2. verify    firma cosign del digest              ← GATE DURO (antes de cualquier efecto)
3. (env)     merge base+overrides → hash → short-circuit no-op si digest+env sin cambios y sano
4. pull      docker pull @digest + re-check (digest resuelto == pedido)
5. env       escribe interp.env + app.env (0600, atómico)
6. recreate  docker compose up -d (argv, nunca shell string)
7. health    probe en loopback                     ← GATE DE EXPOSICIÓN
8. proxy     NPMplus upsert proxy-host             best-effort
9. dns       Cloudflare upsert registro A          best-effort
10. persist  avanza last_good (digest+env+edge) → responde estado por-etapa
```

**Estados terminales** (en la respuesta HTTP, sin secretos):

| Estado | Significado |
|--------|-------------|
| `success` | verificado, pulled, sano, edge OK o no pedido |
| `no_op` | mismo digest + mismo env, ya sano |
| `success_degraded` | contenedor sano, pero proxy/dns falló (convergente) |
| `failure_rolled_back` | health falló → revirtió imagen+env al último bueno |
| `failure` | gate duro falló (admit/verify/pull) → nada cambió |

`verify`/`pull` son gates duros; `proxy`/`dns` son **best-effort** (un blip de
Cloudflare/NPMplus no tira un contenedor sano). El **health corre antes del edge**: un deploy
roto nunca se expone públicamente, y el rollback queda edge-neutral.

## 4. El contrato de wire (statio/v1)

Lo que `statio deploy` (CI) postea al agente:

```jsonc
{
  "apiVersion": "statio/v1",
  "kind": "DeployRequest",
  "service": "api",
  "image": { "repository": "ghcr.io/accentiostudios/api", "digest": "sha256:<64hex>" },
  "env_overrides": { "RELEASE_SHA": "abc123" },
  "proxy": {
    "enabled": true, "domain": "api.example.com",
    "upstream_host": "api", "upstream_port": 3000, "scheme": "http",
    "ssl": true, "force_https": true, "http2": true, "hsts": true, "websockets": true
  },
  "dns": { "enabled": true, "domain": "api.example.com" },
  "deploy_id": "<uuid-v4 opcional>"
}
```

Decodificado con `DisallowUnknownFields` + cap de 256 KiB. Cada campo es escalar/bool/enum/
map-de-literales. **Ningún** campo llega a una posición de comando/shell/template/URL/host/
ruta. El agente mapea los campos tipados a llamadas NPMplus/Cloudflare hard-coded.

**Event-carried vs server-config:**

| Lo lleva el evento (datos) | Lo tiene el server (config/secrets) |
|---|---|
| `service`, `digest`, `env_overrides`, campos de `proxy`/`dns` | repo de imagen (igualdad), identidad cosign, env base + protected, allowlists, creds NPMplus, token+zone Cloudflare, **IP pública**, record-type (forzado `A`), `advanced_config` nginx |

Validación antes de cualquier efecto: digest regex + repo-equality; `service` → dir existente
(nunca auto-crea); dominio RFC-1123 + miembro de la zona allowlist; `upstream_host` ∈
allowlist de contenedores locales; caps de env (sin NUL/newline/control chars).

## 5. Modelo de env de dos archivos

Es la pieza que hace la no-inyección **estructural**, no disciplina de escaping:

- **`interp.env`** → solo `STATIO_IMAGE_DIGEST`. Lo consume `docker compose --env-file` para la
  **interpolación** `${...}`.
- **`app.env`** → el env de la app mergeada. Lo consume el compose **solo** vía `env_file:`
  (lector literal KEY=VALUE, **sin** expansión `${}`).

Un secreto con `$`, `${...}` o `:?` es solo un byte en `app.env`; nunca llega al parser de
interpolación de compose. Los dos archivos nunca se cruzan.

**Merge híbrido** (`internal/env`): base en `env.base.yaml` (con `protected`/`required`/
`secretRef`) + overrides del evento. Orden fail-closed: valida overrides → rechaza colisión
con `protected` (422) → resuelve base → aplica overrides (override gana) → exige `required` →
valida que ningún valor final tenga control chars. Rollback restaura el `app.env` renderizado
junto con el digest (una unidad).

## 6. Modelo de seguridad

Invariantes que **no se deshacen** (varias verificadas por test):

1. **Canal data-only / no-RCE.** Schema cerrado + cap; nada del evento en posición de comando.
   Cualquier feature que deje al evento llevar repo, comando, hook, `advanced_config`,
   record-type o IP → RCE root → rechazar.
2. **`ListenFunnel` prohibido + lint.** `tsnet.Server` lo expone en el mismo objeto =
   exposición pública. `go test` falla si aparece `.ListenFunnel(` o `net.Listen(`. Más el
   self-check `100.64.0.0/10` al arrancar.
3. **OAuth client, no auth key.** `Ephemeral=false`; clients separados agente/CI.
4. **Identity cosign exacta por default.** Regex solo si está anclada `^...$` y sin wildcard
   sobre owner/repo; fail-closed si falta issuer/identity.
5. **Re-check de digest post-pull** + **repo-equality** (el evento elige cuál digest firmado de
   una imagen permitida, no una imagen arbitraria).
6. **Asimetría de autenticidad (residual aceptado).** env/proxy/dns no están firmados por
   cosign; se confían por la ACL de la tailnet (`tag:ci` + WhoIs fail-closed). cosign protege
   *qué código* corre, no *qué env/routing*. → El **OAuth de CI es joya de la corona**:
   rotación + alerta. Acotado por protected-keys, DNS fijado a la IP propia, y allowlists.
7. **Protected-keys fail-closed** antes de resolver secretos; toda key `secretRef` debe ser
   `protected`.
8. **Sin newline/NUL/control-chars** en valores de env (anti forja de líneas en `app.env`).
9. **Allowlist de `upstream_host`** (anti-SSRF) y de dominios (sufijo de zona), antes de
   construir cualquier body de API.
10. **Tokens Cloudflare separados y mínimos**; usuario NPMplus dedicado no-admin;
    `advanced_config` siempre server-side.
11. **Secretos fuera de logs/argv/responses.** Masking de GitHub + `::add-mask::`; errores de
    clientes API stripeados de `Authorization`; nunca se loguea el spec; la respuesta es estado
    typed por-etapa.
12. **WhoIs fail-closed**; tlog/SCT > 0; perms de secrets validadas al arranque; reconciliación
    proxy/DNS event-driven + best-effort (preserva #4 y #3).
13. **`docker.sock` = root-equivalente** (inherente): un exploit del agente antes del gate
    cosign es root del host. El gate cosign + el sandbox systemd son los controles
    compensatorios. Rootless Docker = trabajo futuro.

## 7. Referencia de archivos

### `/etc/statio/config.yaml`

```yaml
hostname: statio                     # MagicDNS hostname del agente
listen_port: 443
tailscale:
  oauth_file: /etc/statio/secrets/oauth
  tags: [tag:agent]
  state_dir: /var/lib/statio/tsnet
cosign:
  oidc_issuer: https://token.actions.githubusercontent.com
  identity: https://github.com/ORG/REPO/.github/workflows/deploy.yml@refs/heads/main
  # identity_regexp: '^...$'        # alternativa anclada (sin wildcard sobre owner/repo)
  require_tlog: true
  require_sct: true
  trusted_root_file: ''            # vacío = TUF live; o un trusted_root.json offline
registry:
  ghcr_auth_file: /etc/statio/secrets/ghcr.json
npmplus:                           # opcional
  base_url: http://npmplus:81
  credentials_file: /etc/statio/secrets/npmplus.json
cloudflare:                        # opcional
  credentials_file: /etc/statio/secrets/cloudflare.json
  zone_apex: example.com
dns:
  public_ip: 203.0.113.10
  ttl: 1
  proxied: false
services_dir: /etc/statio/services
state_dir: /var/lib/statio
log_level: info
```

### Layout en el servidor

```
/usr/local/bin/statio                          0755
/etc/statio/config.yaml                         0600   config global
/etc/statio/services/<svc>/manifest.yaml        0600   política (la escribes tú)
/etc/statio/services/<svc>/compose.yaml         0600   sustrato (lo escribes tú)
/etc/statio/services/<svc>/env.base.yaml        0600   base env (via `statio env`)
/etc/statio/services/<svc>/interp.env           0600   solo STATIO_IMAGE_DIGEST (lo escribe el agente)
/etc/statio/services/<svc>/app.env              0600   env mergeada (lo escribe el agente)
/etc/statio/services/<svc>/secrets/<key>        0600   secretRef de `statio env --secret-stdin`
/etc/statio/secrets/{oauth,ghcr.json,npmplus.json,cloudflare.json}   0600
/var/lib/statio/tsnet/                           0700   estado tsnet (persistente)
/var/lib/statio/services/<svc>/state.json        0600   last_good + history
/var/lib/statio/services/<svc>/history/          0600   snapshots de app.env (N=5)
```

### `manifest.yaml` (referencia completa)

```yaml
apiVersion: statio/v1
kind: ServiceDeploy
name: api                          # == nombre del directorio
signer:                            # opcional: override de la identidad cosign global
  oidc_issuer: ...
  identity: ...
  identity_regexp: ...
image:
  repository: ghcr.io/accentiostudios/api   # el evento debe traer EXACTAMENTE este repo
deploy:
  compose_file: compose.yaml
  project: api
  services: [api]
  image_env: STATIO_IMAGE_DIGEST     # default
health:
  type: http                       # http | tcp | none
  url: http://127.0.0.1:3000/health   # http: DEBE ser loopback
  addr: 127.0.0.1:3000             # tcp: DEBE ser loopback
  expect_status: 200
  start_period: 5s
  interval: 2s
  timeout: 3s
  retries: 10
proxy:
  allowed_domain_suffixes: [example.com]
  allowed_upstream_hosts: [api]
dns:
  allowed_domain_suffixes: [example.com]
rollback:
  enabled: true
  env_policy: with-digest          # with-digest | event-wins
```

## 8. Referencia de comandos

```
statio agent run --config /etc/statio/config.yaml

statio deploy --target HOST --service S --image REPO --digest sha256:...
            [--env KEY=VALUE]... [--proxy-domain D --proxy-upstream-host H
             --proxy-upstream-port P --proxy-ssl] [--dns-domain D]
            [--spec-stdin] [--strict] [--timeout 5m]

statio status --target HOST
statio env set|rm|list <svc> [...]   [--services-dir /etc/statio/services]

statio init server         # interactivo; flags no-interactivos:
                         #   --hostname --identity --issuer --config
                         #   (--ts-oauth-secret-stdin | --ts-oauth-secret-file F)
statio init integrations   # interactivo
statio init repo           # interactivo; flags: --target --service --image --action-ref --out
statio version
```

## 9. Referencia de la Action

```yaml
- uses: accentiostudios/statio/action@v1
  with:
    target: statio.<tailnet>.ts.net        # requerido
    service: api                         # requerido
    image: ghcr.io/accentiostudios/api   # requerido
    digest: ${{ steps.build.outputs.digest }}   # requerido
    env: |                               # opcional — usar ${{ secrets.* }}
      RELEASE_SHA=${{ github.sha }}
    proxy-domain: api.example.com        # opcional
    proxy-upstream-host: api
    proxy-upstream-port: "3000"
    proxy-ssl: "true"
    dns-domain: api.example.com          # opcional
    ts-oauth-client-id: ${{ secrets.TS_OAUTH_CLIENT_ID }}       # requerido
    ts-oauth-secret: ${{ secrets.TS_OAUTH_CLIENT_SECRET }}      # requerido
    statio-version: v1
    timeout: 5m
    strict: "false"
```

Composite: baja el binario `statio` pinneado, se une a la tailnet (efímero `tag:ci`) y corre
`statio deploy`. Los secretos viajan por env, nunca por argv.

## 10. Layout del código

| Paquete | Responsabilidad |
|---------|-----------------|
| `internal/spec` | contrato `statio/v1`: tipos + validación cerrada (compartido por ambas partes) |
| `internal/agent` | tsnet server, handler `POST /deploy`, self-check 100.x, WhoIs guard, lint |
| `internal/verify` | cosign keyless (cosign/v3 + sigstore-go), verify-before-act |
| `internal/deploy` | pipeline (10 etapas), manifest, state/rollback, health, compose, puller |
| `internal/env` | merge híbrido (protected/required, two-file, sin newline/NUL) |
| `internal/proxy` | cliente NPMplus typed (upsert idempotente) |
| `internal/dns` | cliente Cloudflare (upsert A idempotente) |
| `internal/config` | config global + validación fail-closed + perms |
| `internal/client` | `statio deploy` (arma y postea el spec; usa `internal/spec`) |
| `internal/cli` | árbol cobra + asistentes `huh` |
| `internal/fsutil` | escritura atómica + check de perms (linux) |

**Build / test:**

```sh
go build ./...
go test ./...     # spec, env, config, proxy, dns, deploy (pipeline), agent (lint anti-Funnel)
go vet ./...
```

**Notas de implementación v1:** el cliente Cloudflare es un cliente REST delgado (no
`cloudflare-go`) detrás de la interfaz `DNSProvider`; el `pull` usa el CLI `docker` (no el
SDK); `init server`/`integrations` hacen el bootstrap esencial (la rotación idempotente
completa es trabajo futuro). Todo está detrás de interfaces para swapearlo sin tocar el agente.
