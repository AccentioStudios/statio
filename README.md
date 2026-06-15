<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)"  srcset="statio-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="statio-light.svg">
    <img alt="Statio" src="statio-light.svg" width="96">
  </picture>
</p>

<p align="center">Despliega a tu propio servidor con un <code>git push</code> — sin SSH, sin abrir puertos.</p>

---

## Introducción

**Statio** lleva tu imagen Docker a tu servidor con un `git push`. Tu workflow de GitHub Actions
construye y firma la imagen; un agente liviano en tu servidor —conectado por una red privada
Tailscale— la recibe, verifica la firma y recrea el contenedor.

```
git push ─▶ CI: build + firma ─▶ statio/action ─▶ agente en tu server ─▶ desplegado
```

Lo que obtienes:

- **Sin SSH ni puertos abiertos.** El servidor no expone nada a internet.
- **Solo se despliega lo que tu CI firmó.** Verificación cosign keyless antes de tocar nada.
- **Dominio y DNS en el mismo deploy.** Reverse proxy (NPMplus) y Cloudflare, opcional.
- **Un step más de GitHub Actions.** Sin scripts de deploy frágiles.

> El porqué de cada decisión técnica y el modelo de seguridad están en
> [`docs/architecture.md`](docs/architecture.md).

---

## Instalación

En el servidor, un solo comando:

```sh
curl -fsSL https://raw.githubusercontent.com/accentiostudios/statio/main/install.sh | sudo sh
```

Detecta tu OS/arch, descarga el binario desde GitHub Releases, verifica el checksum y lo instala
en `/usr/local/bin/statio`.

**Actualizar:** vuelve a ejecutar ese mismo comando (actualiza solo si hay una versión más nueva)
o usa el self-update integrado:

```sh
statio upgrade            # descarga la última, verifica checksum y reemplaza el binario
statio upgrade --check    # solo avisa si hay una versión nueva, sin instalar
```

Tras actualizar en el servidor, reinicia el agente: `sudo systemctl restart statio-agent`. El CLI
también te avisa cuando hay una versión nueva. Para diagnosticar el entorno: `statio doctor`.

<details><summary>Otras formas de instalar</summary>

- **Con Go:** `go install github.com/accentiostudios/statio/cmd/statio@latest`
- **deb / rpm:** descarga el paquete del [release](https://github.com/accentiostudios/statio/releases) → `sudo dpkg -i statio_*.deb`
- **Desde el código:** `git clone https://github.com/accentiostudios/statio && cd statio && go build -o statio ./cmd/statio`

</details>

Necesitas **Docker** en el servidor y una **cuenta de Tailscale** (el plan gratis alcanza). En CI
no instalas nada: la Action descarga el binario.

---

## Quick Start

El setup toca **dos lugares**. Cada comando lleva su marca:

- 🖥️ **En tu servidor** — el VPS Linux, por SSH, como root.
- 💻 **En tu máquina** — dentro del repo de tu proyecto.

Vamos del cero a un `git push` que despliega.

### Paso 0 · Tailscale (una vez, en la web)

En el [admin console de Tailscale](https://login.tailscale.com/admin/settings/oauth) crea **dos
OAuth clients**: uno con el tag `tag:agent` (para el server) y otro con `tag:ci` (para CI). Pega
esta ACL en *Access controls*:

```json
{
  "tagOwners": { "tag:agent": ["autogroup:admin"], "tag:ci": ["autogroup:admin"] },
  "acls": [ { "action": "accept", "src": ["tag:ci"], "dst": ["tag:agent:443"] } ],
  "ssh": []
}
```

Esto es lo único manual: solo `tag:ci` podrá hablarle al agente, y solo por un puerto.

---

## Parte A — En tu servidor 🖥️

Dos pasos: `init server` levanta el agente (la base del servidor) y `enable` acepta cada servicio
que vas a desplegar.

### A1 · Configura el agente 🖥️

```sh
sudo statio init server
```

El asistente te pregunta lo esencial. El campo clave es la **identidad de firma**: define qué
workflow de GitHub puede desplegar a este servidor.

```
  Nombre de este servidor   › statio
  Repositorio de GitHub     › accentiostudios/api      # owner/repo o la URL
  Archivo del workflow      › deploy.yml
  Branch                    › main
  OAuth client secret       › ••••••••••••••••
```

Con esos campos arma la identidad
`https://github.com/<owner>/<repo>/.github/workflows/<archivo>@refs/heads/<branch>`. El **owner** es
tu usuario u organización (en GitHub es el mismo campo): una cuenta personal usa su usuario,
`tu-usuario/mi-api`.

> **Nota** La identidad se compara exacta — mayúsculas, branch y nombre del workflow deben coincidir,
> o el deploy falla en `verify`. Detalle en [arquitectura §6.2](docs/architecture.md#62-footguns-de-la-identidad-de-firma).
> Si ya tienes el repo a mano, `statio init repo` (Parte B) te imprime la identidad exacta lista para pegar.

### A2 · Habilita el servicio 🖥️

```sh
sudo statio enable
```

`enable` acepta un servicio y fija sus anclas de seguridad: el repo de imagen permitido, los
registries de dependencias y los dominios. El asistente te pregunta:

```
  Nombre del servicio            › api
  Repositorio de la imagen       › ghcr.io/accentiostudios/api   # el repo EXACTO (repo-equality)
  Registries permitidos (deps)   › docker.io, ghcr.io
  ¿Exponer un dominio público?   › no
```

Va separado de `init server` a propósito: un deploy firmado **solo puede desplegar a un servicio que
ya aceptaste con `enable`**, nunca crear uno nuevo. Si tu CI se compromete, el atacante queda acotado
a lo que habilitaste. (Razonamiento completo en
[arquitectura §6.1](docs/architecture.md#61-por-qué-enable-está-separado-de-init-server).)

> **Nota** ¿Imagen en un repo privado? Una vez, en el servidor: `docker login ghcr.io` (el agente baja
> la imagen con el login de Docker del host).

<details><summary>Equivalente no-interactivo (scripts/CI)</summary>

```sh
sudo statio enable api --image ghcr.io/accentiostudios/api \
  --proxy-domain-suffix example.com --dns-domain-suffix example.com

# secretos que solo ops debe ver (opcional — la mayoría vienen de GitHub Secrets):
sudo statio env set api SOME_OPS_SECRET --secret-stdin --protected
```

</details>

### A3 · Inicia el agente 🖥️

```sh
sudo systemctl daemon-reload && sudo systemctl enable --now statio-agent
```

---

## Parte B — En tu repo 💻

Esta parte corre en tu máquina, dentro del repo de tu proyecto — no en el servidor.

### B1 · Prepara el repo 💻

```sh
statio init repo
```

En tu repo, esto:

- crea `statio.yaml` si no existe (la config de tu app),
- detecta tu identidad de firma y la imprime, lista para pegar en la Parte A,
- mira si ya tienes CI: si **tienes un workflow**, te da un *snippet* para pegar (no toca tu archivo);
  si **no tienes CI**, te ofrece generar un `.github/workflows/deploy.yml` listo.

El `statio.yaml` describe tu app — es la fuente de verdad del deploy:

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

El step de tu workflow va donde construyes y firmas tu imagen. Es lo que imprime `statio init repo`
si ya tienes CI:

```yaml
permissions:
  id-token: write        # OBLIGATORIO: cosign firma imagen + payload (keyless OIDC)
  packages: write
  contents: read

# ...tu build + push de la imagen, dejando el digest en steps.build.outputs.digest...
- uses: sigstore/cosign-installer@v3
- run: cosign sign --yes ghcr.io/accentiostudios/api@${{ steps.build.outputs.digest }}

- uses: accentiostudios/statio/action@v1
  with:
    target:  statio.tu-tailnet.ts.net          # hostname del agente (= audience firmado)
    service: api                               # debe estar habilitado en el server
    image:   ghcr.io/accentiostudios/api       # debe coincidir con `statio enable`
    digest:  ${{ steps.build.outputs.digest }}
    env: |                                     # un KEY=${{ secrets.KEY }} por cada env de tu statio.yaml
      DATABASE_URL=${{ secrets.DATABASE_URL }}
    ts-oauth-client-id: ${{ secrets.TS_OAUTH_CLIENT_ID }}
    ts-oauth-secret:    ${{ secrets.TS_OAUTH_CLIENT_SECRET }}
```

statio nunca modifica tu workflow: si ya tienes uno, pegas el step; si no, te genera el `deploy.yml`
completo. Los inputs del Action están en la [referencia](#referencia-del-action) más abajo.

### B2 · Configura los secrets 💻

Desde tu máquina, hacia GitHub:

```sh
gh secret set TS_OAUTH_CLIENT_ID     --body '<tailscale tag:ci oauth client id>'
gh secret set TS_OAUTH_CLIENT_SECRET --body '<tailscale tag:ci oauth client secret>'
gh secret set DATABASE_URL           --body 'postgresql://app:...@db:5432/appdb'
```

La regla: `statio.yaml` declara los **nombres** de las keys; el bloque `env:` del Action les da el
**valor** desde `${{ secrets.* }}`. Lo que no es secreto va en `env_inline`.

### B3 · Despliega 💻

```sh
git push
```

CI construye y firma la imagen, firma el payload (la misma identidad keyless) y manda el envelope. El
agente lo verifica, baja la imagen, genera el compose desde tu `statio.yaml` y recrea los contenedores.
El estado por etapa sale en los logs de la Action; el historial, en `statio logs api`.

> **Nota** El auto-detect de `init repo` lee tu git remote local, así que funciona con repos privados.
> La imagen y el código siguen privados, pero la firma keyless registra la *identidad* del repo en un
> log público (Rekor): el **nombre** del repo queda público.
> Ver [arquitectura §6.5](docs/architecture.md#65-repos-privados-y-rekor).

---

## Referencia del Action

`accentiostudios/statio/action@v1`. El workflow **debe** declarar `permissions: id-token: write`
(cosign keyless firma imagen + payload). El Action instala el binario y cosign, se une a la tailnet
como nodo efímero `tag:ci`, y corre `statio deploy`.

| Input | Requerido | Qué es |
|---|---|---|
| `target` | sí | hostname MagicDNS del agente (ej. `statio.tu-tailnet.ts.net`). Es el **audience firmado** — debe ser ESE server. |
| `service` | sí | slot del servicio; debe estar habilitado en el server (`statio enable`). |
| `image` | sí | repo de tu imagen; debe coincidir con `statio enable` (repo-equality). |
| `digest` | sí | digest a desplegar (`steps.build.outputs.digest`, o uno viejo para rollback). |
| `env` | no | overrides por deploy, líneas `KEY=${{ secrets.KEY }}`. GitHub las enmascara. |
| `statio-file` | no | ruta del `statio.yaml` (default `statio.yaml`). |
| `ts-oauth-client-id` / `ts-oauth-secret` | sí | OAuth client de Tailscale **`tag:ci`** (distinto del `tag:agent` del server). |
| `timeout` | no | timeout del deploy (default `5m`). |
| `strict` | no | tratar `success_degraded` como fallo (default `false`). |

`deploy-seq` (anti-replay) lo pone el Action solo, desde `github.run_number` — no lo configures.

---

## Guías

### Agregar un dominio (reverse proxy + DNS)

1. En el servidor, ejecuta el asistente de integraciones y pega en `/etc/statio/config.yaml` las
   líneas que imprime:

   ```sh
   sudo statio init integrations    # te pregunta por NPMplus y Cloudflare, paso a paso
   ```

2. Permite el dominio al habilitar el servicio. En `sudo statio enable`, responde **sí** a "¿Exponer
   un dominio público?" e ingresa el sufijo (o con flags: `--proxy-domain-suffix example.com
   --proxy-upstream api --dns-domain-suffix example.com`).

3. Declara el dominio en tu `statio.yaml`:

   ```yaml
   proxy: { domain: api.example.com, upstream: api, upstream_port: 3000 }
   dns:   { domain: api.example.com }
   ```

En el próximo deploy, el agente crea o actualiza el proxy host en NPMplus y el registro DNS apuntando
a tu servidor. Si NPMplus o Cloudflare fallan, el deploy igual queda sano (estado `success_degraded`) y
converge al reintentar. El dominio solo se acepta si cae bajo un sufijo permitido al habilitar
(anti-hijack). El cert TLS lo emites en NPMplus (Let's Encrypt).

### Variables de entorno

Los valores viven en GitHub Secrets; tu `statio.yaml` solo **rutea** qué key va a qué servicio. El
agente las escribe en `/run/statio/<svc>/<servicio>.env` en tmpfs (RAM) y las pasa al contenedor —
nunca a disco persistente.

```yaml
# statio.yaml (repo): declara las keys por servicio
services:
  - name: api
    env: [DATABASE_URL, JWT_SECRET]        # valores desde GitHub Secrets
    env_inline: { NODE_ENV: production }   # config NO secreta, literal
```

```yaml
# el workflow mapea cada key a su secret (GitHub las enmascara en logs)
with:
  env: |
    DATABASE_URL=${{ secrets.DATABASE_URL }}
    JWT_SECRET=${{ secrets.JWT_SECRET }}
```

Existe una base **server-side** solo para secretos de ops que CI no debe ver:

```sh
sudo statio env set api OPS_ONLY --secret-stdin --protected   # CI no puede sobreescribirla
sudo statio env set api MUST_HAVE --required                  # CI debe proveerla, o 422
```

> **Nota** Los valores de CI son RAM-only y no salen en logs ni en la respuesta, pero no hay cifrado
> at-rest: `docker inspect` se los muestra a root local. Detalle en
> [arquitectura §6.4](docs/architecture.md#64-secretos-at-rest-claim-honesto).

### Hacer rollback

Cada deploy lleva un digest explícito, así que volver atrás es desplegar el digest viejo: desde
**Actions → Run workflow** con el input `digest` (el workflow generado lo soporta). CI firma un payload
nuevo con el digest viejo — válido y fresco.

Y si un deploy nuevo no pasa el health check, el rollback es **automático**: vuelve a la última versión
sana (imagen + env juntos). El snapshot vive en RAM (`/run`), así que el auto-rollback funciona en el
mismo arranque; tras un reboot no hay rollback offline → se re-deploya desde CI.

### Ver el historial / auditar

```sh
statio logs api                                    # timeline por deploy (en el server)
statio logs api --target statio.<tailnet>.ts.net   # remoto, por la tailnet (redactado)
```

### Varios servicios o servidores

- **Varios servicios**: un `statio enable <svc>` por cada uno; un `statio.yaml` por repo describe los
  contenedores (tu app + dependencias como postgres/redis).
- **Varios servidores**: `statio` es por-servidor. Ejecuta `statio init server` en cada uno; cada agente
  tiene su propio hostname (= su `audience` firmado) y CI elige el target. No se coordinan.

---

## Comandos

```sh
statio init server          # asistente: configura el agente
statio init integrations    # asistente: NPMplus + Cloudflare + IP pública
statio init repo            # asistente: statio.yaml + cómo llamar al Action
statio enable [svc]         # asistente: acepta un servicio y fija sus anclas

statio env set <svc> KEY=VALUE [--protected] [--required]
statio env set <svc> KEY --secret-stdin          # secreto de ops por stdin
statio env list <svc>
statio env rm  <svc> KEY

statio deploy ...           # lo usa la Action (no a mano)
statio logs <svc> [--target HOST]                # audit log (local o remoto)
statio status --target HOST                      # estado del agente
statio upgrade [--check]                         # self-update (verifica checksum)
statio doctor                                    # diagnóstico del entorno
statio version                                   # o: statio --version
```

Los asistentes (`init server`, `init integrations`, `init repo`, `enable`) son interactivos: ejecútalos
sin flags y te van guiando. En CI/scripts aceptan flags y secretos por `--*-stdin`; el Action usa la
forma con flags automáticamente.

---

## Solución de problemas

| Síntoma | Qué revisar |
|---------|-------------|
| El agente no levanta (`no tailnet address`) | El OAuth client `tag:agent` y que el nodo esté aprobado en Tailscale. |
| Deploy `403` `[audience]` | El payload apunta a otro server: revisa el `target` de la Action. |
| Deploy `403` `[no_signature]` / `[identity_mismatch]` | Falta el bundle, o la identidad firmante no coincide con la configurada (owner/repo/workflow/branch). |
| Deploy `409` `[replay_seq]` o `[expired]` | Payload viejo/reusado: re-ejecuta el deploy desde CI. |
| Deploy `422` `[protected]`/`[required]` | Intentaste sobreescribir una key `--protected`, o falta una `--required`. |
| `[registry_denied]` | Una dependencia usa un registry fuera del allowlist (`statio enable --registries`). |
| `success_degraded` que no se va | NPMplus o Cloudflare inalcanzable. Revisa `statio init integrations` y reintenta. |
| `[timeout]` y revierte | La app no responde en el health path (loopback). Revisa el contenedor. |

Cada falla trae un `code` estable + `hint`; el detalle crudo (output de compose) queda solo en
`journalctl -u statio-agent`, nunca en la respuesta a CI.

---

## Seguridad y arquitectura

El modelo de seguridad, los invariantes, el runbook de compromiso de identidad, el pipeline de deploy,
el contrato de wire y el layout del código están en
**[`docs/architecture.md`](docs/architecture.md)**.

---

<p align="center"><sub>Hecho por accentiostudios.</sub></p>
