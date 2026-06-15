<h1 align="center">Statio</h1>

<p align="center">Despliega a tu propio servidor con un <code>git push</code> — sin SSH, sin abrir puertos.</p>

---

## Introducción

**Statio** lleva tu imagen Docker a tu servidor de forma segura, sin las partes molestas del
deploy self-hosted:

- 🔌 **Sin SSH** y **sin puertos abiertos** a internet. El servidor no expone nada.
- ✍️ **Imágenes firmadas.** Solo se despliega lo que tu CI firmó. Nadie más.
- 🌐 **Dominio automático.** Configura el reverse proxy (NPMplus) y el registro DNS
  (Cloudflare) en el mismo deploy.
- 🧩 **Se integra como un step más** de GitHub Actions. Sin scripts frágiles.

Funciona así: tu workflow construye y firma la imagen, y un pequeño **agente** en tu
servidor (conectado por una red privada Tailscale) la recibe, la verifica y recrea el
contenedor.

```
git push ─▶ CI: build + firma ─▶ statio/action ─▶ 🛰️ agente en tu server ─▶ ✅ desplegado
```

> ¿Quieres el porqué de cada decisión técnica y el modelo de seguridad? Está en
> [`docs/architecture.md`](docs/architecture.md).

---

## Instalación

En el servidor, un solo comando:

```sh
curl -fsSL https://raw.githubusercontent.com/accentiostudios/statio/main/install.sh | sudo sh
```

Detecta tu OS/arch, descarga el binario firmado desde GitHub Releases, verifica el checksum
y lo instala en `/usr/local/bin/statio`.

<details><summary>Otras formas de instalar</summary>

- **Con Go:** `go install github.com/accentiostudios/statio/cmd/statio@latest`
- **Sin instalar nada (Go):** `go run github.com/accentiostudios/statio/cmd/statio@latest version`
- **deb / rpm:** descarga el paquete del [release](https://github.com/accentiostudios/statio/releases) → `sudo dpkg -i statio_*.deb`
- **Desde el código:** `git clone https://github.com/accentiostudios/statio && cd statio && go build -o statio ./cmd/statio`

</details>

Además necesitas **Docker** en el servidor y una **cuenta de Tailscale** (con el plan gratis
es suficiente), que usas una sola vez en el [Quick Start](#quick-start). En CI no instalas
nada: la Action descarga el binario.

---

## Quick Start

Vas a tocar **dos lugares**. Cada comando indica dónde corre:

- 🖥️ **En tu servidor** — el VPS Linux, por SSH/consola, como root.
- 💻 **En tu máquina** — dentro del repo de tu proyecto (donde está tu código).

(GitHub corre el Action solo; ahí no entrás, salvo para `gh secret set` desde tu máquina.)

### 0 · Prerequisito (una vez) — en Tailscale (web)

En el [admin console de Tailscale](https://login.tailscale.com/admin/settings/oauth) crea
**dos OAuth clients**: uno con el tag `tag:agent` (para el server) y otro con `tag:ci`
(para CI). Y pega esta ACL (Access controls):

```json
{
  "tagOwners": { "tag:agent": ["autogroup:admin"], "tag:ci": ["autogroup:admin"] },
  "acls": [ { "action": "accept", "src": ["tag:ci"], "dst": ["tag:agent:443"] } ],
  "ssh": []
}
```

> 💡 Esto es lo único manual. Solo `tag:ci` podrá hablarle al agente, y solo por un puerto.

---

## Parte A — En tu servidor 🖥️

> Todo en esta parte corre **en el servidor** (por SSH), una sola vez.

### A1 · Configura el agente — 🖥️

```sh
sudo statio init server
```

El wizard te pregunta (lo importante: la **identidad de firma**):

```
  Nombre de este servidor   › statio
  Repositorio de GitHub      › accentiostudios/api      # owner/repo o la URL (podés pegarla)
  Archivo del workflow       › deploy.yml
  Branch                     › main
  OAuth client secret        › ••••••••••••••••
```

> 🔑 **La identidad de firma** = *qué workflow de GitHub puede deployar*. Con esos campos arma:
> `https://github.com/<owner>/<repo>/.github/workflows/<archivo>@refs/heads/<branch>`
> - **owner** = tu **usuario U organización** — en GitHub es el mismo campo. ¿Cuenta personal,
>   sin organización? Usás tu **usuario**: `tu-usuario/mi-api`.
> - Son **nombres** (o pegá la URL del repo), no hace falta organización: `accentiostudios/api`.
> - **Tip:** corré `statio init repo` en tu repo (Parte B) y te imprime la identidad exacta lista
>   para pegar acá.
> - **Footguns** (todos dan `verify falla`): mayúsculas exactas como en GitHub; solo deploya desde
>   esa rama; el nombre del archivo del workflow debe coincidir.

### A2 · Habilita el servicio — 🖥️

Ops acepta el servicio y fija sus anclas (qué repo de imagen, qué registries de dependencias,
qué dominios):

```sh
sudo statio enable api --image ghcr.io/accentiostudios/api \
  --proxy-domain-suffix example.com --dns-domain-suffix example.com
```

Secretos que solo ops debe ver (opcional — la mayoría vienen de GitHub Secrets):

```sh
sudo statio env set api SOME_OPS_SECRET --secret-stdin --protected
```

> 🔒 ¿Imagen en un repo **privado**? Una vez, en el servidor: `docker login ghcr.io` (el agente
> baja la imagen con el login de Docker del host).

### A3 · Inicia el agente — 🖥️

```sh
sudo systemctl daemon-reload && sudo systemctl enable --now statio-agent
```

---

## Parte B — En tu repo 💻

> Todo en esta parte corre **en tu máquina, dentro del repo de tu proyecto** — NO en el servidor.

### B1 · Prepara el repo — 💻

```sh
statio init repo      # se ejecuta dentro del repo de tu proyecto
```

Esto, en tu repo:
- crea `statio.yaml` si no existe (la config de tu app),
- detecta tu **identidad de firma** y te la imprime para pegar en la Parte A,
- mira si ya tenés CI:
  - **ya tenés un workflow** → te da un *snippet* para pegar en él (no toca tu archivo),
  - **no tenés CI** → te ofrece generar un `.github/workflows/deploy.yml` listo.

**`statio.yaml`** — describe tu app (la fuente de verdad):

```yaml
services:
  - name: api                    # tu app: sin `image:` → se inyecta tu imagen firmada
    ports: [3000]                # → publicado solo en 127.0.0.1:3000
    env: [DATABASE_URL]          # solo el NOMBRE; el valor llega desde CI
    env_inline: { NODE_ENV: production }
    health: { path: /health }
proxy: { domain: api.example.com, upstream: api, upstream_port: 3000 }
dns:   { domain: api.example.com }
```

**El step en tu workflow** — donde buildeás y firmás tu imagen, agregá esto (es lo que imprime
`statio init repo` si ya tenés CI):

```yaml
permissions:
  id-token: write        # ← OBLIGATORIO: cosign firma imagen + payload (keyless OIDC)
  packages: write
  contents: read

# ...tu build + push de la imagen, dejando el digest en steps.build.outputs.digest...
- uses: sigstore/cosign-installer@v3
- run: cosign sign --yes ghcr.io/accentiostudios/api@${{ steps.build.outputs.digest }}

- uses: accentiostudios/statio/action@v1
  with:
    target:  statio.tu-tailnet.ts.net          # hostname del agente (= audience firmado)
    service: api                               # debe estar habilitado en el server
    image:   ghcr.io/accentiostudios/api       # debe coincidir con `statio enable --image`
    digest:  ${{ steps.build.outputs.digest }}
    env: |                                     # un KEY=${{ secrets.KEY }} por cada env de tu statio.yaml
      DATABASE_URL=${{ secrets.DATABASE_URL }}
    ts-oauth-client-id: ${{ secrets.TS_OAUTH_CLIENT_ID }}
    ts-oauth-secret:    ${{ secrets.TS_OAUTH_CLIENT_SECRET }}
```

> ¿Sin CI? `statio init repo` te genera el `deploy.yml` completo. ¿Ya tenés CI? statio **no toca**
> tu workflow — pegás el step de arriba donde corresponda. Ver la
> [Referencia del Action](#referencia-del-action-accentiostudiosstatioactionv1).

### B2 · Configura los secrets — 💻 (desde tu máquina, hacia GitHub)

```sh
gh secret set TS_OAUTH_CLIENT_ID     --body '<tailscale tag:ci oauth client id>'
gh secret set TS_OAUTH_CLIENT_SECRET --body '<tailscale tag:ci oauth client secret>'
gh secret set DATABASE_URL           --body 'postgresql://app:...@db:5432/appdb'
```

> 💡 Regla: `statio.yaml` declara los **nombres** de keys; el bloque `env:` del Action les da el
> **valor** desde `${{ secrets.* }}`. Lo que no es secreto va en `env_inline`.

### B3 · Despliega 🚀 — 💻

```sh
git push
```

CI buildea y firma la imagen, **firma el payload** (misma identidad keyless) y manda el envelope.
El agente lo verifica, baja la imagen, **genera el compose** desde tu `statio.yaml` y recrea los
contenedores. El estado por etapa sale en los logs de la Action; el historial en `statio logs api`.

> 🔒 **Repos privados.** El auto-detect de `init repo` lee tu git remote **local** → funciona
> igual con repos privados (no necesita auth ni API). La imagen y el código siguen privados; solo
> tené en cuenta que la firma keyless registra la *identidad* (owner/repo/workflow) en el log
> público de transparencia (Rekor) — el **nombre** del repo queda público aunque el repo sea privado.

---

## Guías

### Referencia del Action (`accentiostudios/statio/action@v1`)

El workflow **debe** declarar `permissions: id-token: write` (cosign keyless firma imagen +
payload). El Action instala el binario, instala cosign, se une a la tailnet como nodo efímero
`tag:ci`, y corre `statio deploy`.

| Input | Requerido | Qué es |
|---|---|---|
| `target` | sí | hostname MagicDNS del agente (ej. `statio.tu-tailnet.ts.net`). Es el **audience firmado** — debe ser ESE server. |
| `service` | sí | slot del servicio; debe estar habilitado en el server (`statio enable`). |
| `image` | sí | repo de tu imagen; debe coincidir con `statio enable --image` (repo-equality). |
| `digest` | sí | digest a desplegar (`steps.build.outputs.digest`, o un viejo para rollback). |
| `env` | no | overrides por deploy, líneas `KEY=${{ secrets.KEY }}`. GitHub las enmascara. |
| `statio-file` | no | ruta del `statio.yaml` (default `statio.yaml`). |
| `ts-oauth-client-id` / `ts-oauth-secret` | sí | OAuth client de Tailscale **`tag:ci`** (distinto del `tag:agent` del server). |
| `timeout` | no | timeout del deploy (default `5m`). |
| `strict` | no | tratar `success_degraded` como fallo (default `false`). |

`deploy-seq` (anti-replay) lo pone el Action solo, desde `github.run_number` — no lo configures.

### Agregar un dominio (reverse proxy + DNS)

1. En el servidor, ejecuta el asistente de integraciones y pega en `/etc/statio/config.yaml`
   las líneas que imprime:

   ```sh
   sudo statio init integrations    # te pregunta por NPMplus y Cloudflare, paso a paso
   ```

2. Permite el dominio al habilitar el servicio (ancla server-side):

   ```sh
   sudo statio enable api --image ghcr.io/accentiostudios/api \
     --proxy-domain-suffix example.com --proxy-upstream api \
     --dns-domain-suffix example.com
   ```

3. Declara el dominio en tu `statio.yaml` (en el repo):

   ```yaml
   proxy: { domain: api.example.com, upstream: api, upstream_port: 3000 }
   dns:   { domain: api.example.com }
   ```

En el próximo deploy, el agente crea o actualiza el proxy host en NPMplus y el registro DNS
apuntando a tu servidor. Si NPMplus o Cloudflare fallan, el deploy igual queda sano (estado
`success_degraded`) y converge al reintentar. El dominio solo se acepta si cae bajo un sufijo
permitido al habilitar (anti-hijack).

> El cert TLS lo emites en NPMplus (Let's Encrypt) — la emisión automática desde el agente
> es trabajo futuro. El registro DNS y el proxy host sí los maneja `statio`.

### Variables de entorno

Las **values** viven en GitHub Secrets; tu `statio.yaml` solo **rutea** qué key va a qué
servicio. El agente las escribe en `/run/statio/<svc>/<servicio>.env` en **tmpfs (RAM)** y las
pasa al contenedor — nunca a disco persistente.

```yaml
# statio.yaml (repo): declara las keys por servicio
services:
  - name: api
    env: [DATABASE_URL, JWT_SECRET]   # values desde GitHub Secrets
    env_inline: { NODE_ENV: production }   # config NO secreta, literal
```

```yaml
# el workflow mapea cada key a su secret (GitHub las enmascara en logs)
with:
  env: |
    DATABASE_URL=${{ secrets.DATABASE_URL }}
    JWT_SECRET=${{ secrets.JWT_SECRET }}
```

Una base **server-side** sigue existiendo solo para secretos de ops que CI no debe ver:

```sh
sudo statio env set api OPS_ONLY --secret-stdin --protected   # CI no puede sobreescribirla
sudo statio env set api MUST_HAVE --required                  # CI debe proveerla, o 422
```

> **Sobre los secretos at-rest (honesto):** el agente corre como root con `docker.sock`, así
> que es root-equivalente. Las values de CI son **RAM-only** y no salen en logs/respuesta, pero
> no hay cifrado at-rest mágico: `docker inspect` se las muestra a root local (inherente). La
> protección real es que GitHub Secrets es el store, el canal va firmado, y nada toca el disco
> persistente.

### Hacer rollback

Como cada deploy lleva un digest explícito, volver atrás es desplegar el digest viejo. Desde
la pestaña **Actions → Run workflow** con el input `digest` (el workflow generado lo soporta).
CI firma un payload nuevo con el digest viejo — válido y fresco.

Y si un deploy nuevo no pasa el health check, **el rollback es automático**: vuelve a la última
versión sana (imagen + env juntos). El snapshot vive en RAM (`/run`), así que el auto-rollback
funciona en el mismo arranque; **tras un reboot** no hay rollback offline → se re-deploya desde
CI (es el modelo courier).

### Ver el historial / auditar

```sh
statio logs api                       # timeline por deploy (en el server)
statio logs api --target statio.<tailnet>.ts.net   # remoto, por la tailnet (redactado)
```

### Varios servicios o servidores

- **Varios servicios**: `statio enable <svc>` por cada uno; un `statio.yaml` por repo describe los
  contenedores (tu app + dependencias como postgres/redis).
- **Varios servidores**: `statio` es por-servidor. Ejecuta `statio init server` en cada uno; cada
  agente tiene su propio hostname (= su `audience` firmado) y CI elige el target. No se coordinan.

---

## Comandos

```sh
statio init server          # asistente: configura el agente (interactivo)
statio init integrations    # asistente: NPMplus + Cloudflare + IP (interactivo)
statio init repo            # asistente: genera el workflow + statio.yaml starter
statio enable <svc> --image REPO [--proxy-domain-suffix ...] [--dns-domain-suffix ...]

statio env set <svc> KEY=VALUE [--protected] [--required]
statio env set <svc> KEY --secret-stdin          # secreto de ops por stdin
statio env list <svc>
statio env rm  <svc> KEY

statio deploy --target HOST --service S --image REPO --digest D --deploy-seq N   # lo usa la Action
statio logs <svc> [--target HOST]   # audit log (local o remoto)
statio status --target HOST         # estado del agente
statio version
```

Los `init` se ejecutan interactivos en una terminal; en CI/scripts aceptan flags y secretos
por `--*-stdin`.

---

## Solución de problemas

| Síntoma | Qué revisar |
|---------|-------------|
| El agente no levanta (`no tailnet address`) | El OAuth client `tag:agent` y que el nodo esté aprobado en Tailscale. |
| Deploy `403` `[audience]` | El payload apunta a otro server: revisa el `target`/`--audience` de la Action. |
| Deploy `403` `[no_signature]` / `[identity_mismatch]` | Falta el bundle, o la identidad firmante no coincide con la configurada (org/repo/workflow/branch). |
| Deploy `409` `[replay_seq]` o `[expired]` | Payload viejo/reusado: re-corre el deploy desde CI (mint fresco). |
| Deploy `422` `[protected]`/`[required]` | Intentaste sobreescribir una key `--protected`, o falta una `--required`. |
| `[registry_denied]` | Una dependencia usa un registry fuera del allowlist (`statio enable --registries`). |
| `success_degraded` que no se va | NPMplus o Cloudflare inalcanzable. Revisa `statio init integrations`. Reintenta. |
| `[timeout]` y revierte | La app no responde en el health path (loopback). Revisa el contenedor. |

Cada falla trae un `code` estable + `hint`; el detalle crudo (output de compose) queda solo en
`journalctl -u statio-agent`, nunca en la respuesta a CI.

---

## Seguridad: runbook de compromiso de identidad

Una sola identidad cosign firma **imagen y payload**. Si ese repo/workflow se compromete, un
atacante puede firmar code + config. Mitigaciones y respuesta:

- **Prevención:** branch protection + required reviews en el repo/ref firmante; alertá en cada
  deploy (`statio logs`).
- **Rotación de un secreto filtrado** (sin esperar a CI): `statio env set <svc> KEY --secret-stdin`
  en el server (la base de ops es el camino break-glass).
- **Rotar la identidad confiable:** es un cambio de `cosign.identity` en `/etc/statio/config.yaml`,
  distribuible a todos los agentes; reiniciá `statio-agent`. No hay edición por-servicio.
- **Acotado por diseño:** aunque el payload esté firmado, el DNS apunta solo a tu IP pinneada, el
  upstream y los dominios están en allowlist, y el compose generado no puede escalar a root del
  host (sin privileged/mounts/sock). El daño se limita a "tu propio repo desplegó algo".

---

## Cómo funciona por dentro

La arquitectura, el pipeline de deploy, el contrato de wire, el modelo de env de dos
archivos, el modelo de seguridad y el layout del código están en
**[`docs/architecture.md`](docs/architecture.md)**.

---

<p align="center"><sub>Hecho por accentiostudios · sin SSH, sin puertos, sin drama.</sub></p>
