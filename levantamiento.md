# Deploy sin SSH — Diseño / Spec de implementación

> Documento de diseño para pasarle a Claude Code como contexto de build.
> Capta las decisiones de arquitectura y su porqué. **Las cuatro restricciones
> duras y las decisiones de seguridad no deben deshacerse sin entender el motivo.**

---

## 1. Resumen

Herramienta para hacer deploy a un servidor propio **sin SSH** y **sin exponer
puertos a internet**. Un **agente** corre en el servidor y recibe una señal de
deploy directa desde CI/CD, que viaja por una **red privada de Tailscale**. La
imagen Docker ya viene construida y **firmada** en un registry (GHCR); el agente
sólo la baja por digest y recrea el servicio. Un **CLI de init** configura ambos
lados.

Inspirado en cómo funciona Tailscale: nada escucha en una interfaz pública; las
conexiones salen hacia afuera.

---

## 2. Objetivos y restricciones (duras)

Cuatro propiedades no negociables. Toda decisión de implementación debe
preservarlas:

1. **Sin SSH.** El deploy no usa SSH en ningún momento.
2. **Sin puerto abierto a internet.** El agente escucha **sólo** en la interfaz
   de la tailnet (IP `100.x`), nunca en una pública.
3. **Sin broker/infra central que mantengamos nosotros.** Nada de levantar un
   message broker propio. Las piezas "centrales" (registry, control plane de
   Tailscale) las opera un tercero (GitHub, Tailscale).
4. **Sin polling.** El agente no consulta en loop; recibe un **push** directo en
   tiempo real.

Postura de seguridad adicional:

- La imagen se referencia por **digest inmutable** (`@sha256:...`), nunca por tag
  mutable.
- La autenticidad del artefacto se prueba con **firma (cosign / sigstore)**, no
  por confiar en el transporte.
- Los **pasos de deploy viven en el servidor**; el evento sólo lleva *datos* (qué
  digest, qué servicio). El canal **nunca** transporta scripts a ejecutar → no es
  un canal de ejecución remota de código.

---

## 3. Arquitectura (vista general)

```
  ┌─────────────────┐    (1) push + firma (HTTPS)   ┌──────────────────────┐
  │  CI/CD           │ - - - - - - - - - - - - - - ->│  GHCR (registry)     │
  │  GitHub Actions  │                               │  imagen @digest      │
  │                  │<- - - - - - - - - - - - - - - │  + firma cosign      │
  └────────┬─────────┘   (3) pull @digest (HTTPS,    │  (lo mantiene GitHub)│
           │                  lo hace el agente)     └──────────────────────┘
           │
           │  (2) POST /deploy {digest} — DIRECTO, por Tailscale (tiempo real)
           v
  ===================================================
  ||  TAILNET PRIVADA (Tailscale)                  ||
  ||  capa 1 -> ACL: solo tag:ci alcanza tag:agent ||
  ||                                               ||
  ||   +-------------------------------------+     ||
  ||   |  AGENTE (servidor)                  |     ||
  ||   |   - escucha SOLO en la tailnet      |     ||
  ||   |   - verifica firma cosign  <- capa 2|     ||
  ||   |   - pull @digest -> recrea servicio |     ||
  ||   |   - pasos de deploy LOCALES         |     ||
  ||   |   - corre bajo systemd              |     ||
  ||   +-------------------------------------+     ||
  ===================================================

  Control plane + DERP de Tailscale: coordina la red, NO toca los datos
  (tráfico P2P cifrado end-to-end). Lo corre Tailscale, no nosotros.
```

**Dos transportes distintos (clave):**

- La **señal** de deploy va por Tailscale (privado, instantáneo).
- La **imagen** va por **HTTPS normal** a/desde GHCR. El agente tiene salida a
  internet para el `pull`; GHCR **no** está en la tailnet.

---

## 4. Flujo de deploy

1. **CI construye** la imagen Docker.
2. **CI la pushea a GHCR** y la **firma con cosign** (keyless, usando la identidad
   OIDC del propio run de GitHub Actions). Queda referenciada por digest.
3. **CI se une a la tailnet** como **nodo efímero** (Action oficial de Tailscale +
   OAuth client). El nodo se autolimpia al terminar el job.
4. **CI hace `POST /deploy`** directo a la IP de tailnet del agente, con un payload
   mínimo: el digest y el servicio.
5. **El agente verifica la firma** cosign del digest contra la identidad esperada
   (capa 2). Si no valida, rechaza.
6. **El agente hace `pull` por digest** desde GHCR (HTTPS) y **corre los pasos de
   deploy locales** del servicio (recrear contenedor, etc.).
7. **El agente responde** el resultado por la misma conexión HTTP → CI sabe si
   salió bien o mal. **Feedback channel gratis**, sin componente extra.

---

## 5. Componentes

### 5.1 Agente (en el servidor)

- Binario único. **Sugerencia:** usar **`tsnet`** (librería de Tailscale para Go)
  para que el binario embeba Tailscale y exponga su listener **sólo en la
  tailnet**, sin un `tailscaled` aparte que mantener.
- Corre como servicio **systemd** (sobrevive reboots, se reinicia si crashea).
- Expone un endpoint HTTP (p. ej. `POST /deploy`) **bindeado a la IP de tailnet
  únicamente**.
- Al recibir un deploy:
  - Verifica la firma cosign del digest contra la identidad OIDC esperada.
  - `docker pull` por digest desde GHCR.
  - Ejecuta el procedimiento de deploy local del servicio (recrear contenedor /
    `docker compose up -d` / lo que aplique).
  - Devuelve estado (started / success / failure).
- Guarda en disco, con permisos restrictivos (`0600`, root):
  - Credenciales de `pull` del registry (GHCR), si el repo es privado.
  - La identidad esperada del firmante (issuer + subject de cosign).
  - Las definiciones de los servicios y sus pasos de deploy (versionados local).

### 5.2 CI/CD (GitHub Actions)

- Workflow generado por el init. Hace: `build` -> `push + cosign sign` ->
  `join tailnet (efímero)` -> `POST /deploy`.
- Permisos del workflow:
  - `id-token: write` (para el OIDC de cosign keyless).
  - `packages: write` (para pushear a GHCR).
- Secrets necesarios:
  - **Credencial OAuth de Tailscale** (para levantar el nodo efímero).
    **Único secreto de larga vida.** No es una llave de firma.

### 5.3 GHCR (registry)

- Almacena la imagen por digest y la firma cosign.
- Lo mantiene GitHub. El agente necesita credenciales de `pull` si el repo es
  privado.

### 5.4 Tailscale (transporte)

- La **tailnet** es la red privada donde viven el agente y, transitoriamente, el
  nodo efímero de CI.
- **ACLs (capa 1):** sólo los nodos taggeados `tag:ci` alcanzan el puerto de
  deploy del agente (`tag:agent`).
- **Control plane + DERP** los corre Tailscale; el tráfico va P2P cifrado
  end-to-end; el control plane no ve los datos. No es algo que mantengamos.
- *Alternativa de la misma familia, no elegida:* **Cloudflare Tunnel** — el agente
  expone un endpoint HTTPS por el edge de Cloudflare, CI llama con un token de
  Access. Misma propiedad de "sin puerto abierto, sin broker propio".

### 5.5 Init / CLI — *la pieza que falta diseñar en detalle*

Responsabilidades:

- **En el servidor:** instalar el binario del agente, registrar el servicio
  systemd, unir el nodo a la tailnet con su tag, escribir la config (identidad
  esperada del firmante, creds del registry, definiciones de servicios + pasos de
  deploy), setear permisos.
- **Para el repo:** generar `.github/workflows/deploy.yml` ya cableado, y la lista
  de Secrets que CI necesita (o setearlos vía la API de GitHub).
- **Idempotente:** re-correrlo reconfigura y rota credenciales sin romper.

---

## 6. Modelo de autenticación (dos capas)

| Capa | Pregunta | Mecanismo |
|------|----------|-----------|
| **Capa 1 — transporte** | ¿Quién puede siquiera hablarle al agente? | ACL de Tailscale (`tag:ci -> tag:agent`) |
| **Capa 2 — artefacto** | ¿Qué tiene permitido deployar el agente? | Firma cosign verificada al momento del deploy |

Son independientes y complementarias. Tailscale evita que cualquiera de internet
toque el agente; cosign evita que se deploye un artefacto no firmado por nuestro
CI, **incluso si** algo logró llegar por la red. El agente **no confía en la
posición de red sola**.

Verificación cosign (keyless): el agente valida que el digest tenga una firma con:

- `--certificate-oidc-issuer` = el issuer de tokens de GitHub Actions.
- `--certificate-identity` (o regexp) = el subject esperado (repo / workflow
  autorizado).

---

## 7. Decisiones clave y por qué (no deshacer sin entender)

- **Imagen por digest, no por tag.** Inmutable y reproducible; deployar dos veces
  el mismo digest es idempotente.
- **Pasos de deploy en el servidor, evento sólo con datos.** Si los scripts
  viajaran con el evento, el canal sería **ejecución remota de código root**:
  cualquiera que forje un evento correría comandos arbitrarios. Manteniéndolos
  locales, un evento forjado, en el peor caso, dispara el deploy de un artefacto
  conocido a un servicio conocido — y aun eso lo frena la firma cosign.
- **Push (no pull).** Requisito explícito: sin polling. El push directo da tiempo
  real y, de yapa, un feedback channel (la respuesta HTTP).
- **Sin broker propio.** Un broker self-hosted es un punto único de falla para
  todos los deploys y un pasivo de mantenimiento. La señal viaja por Tailscale,
  que opera un tercero.
- **Firma cosign keyless en vez de secreto compartido.** No hay llave de firma de
  larga vida que rotar; la confianza ancla en la identidad OIDC de GitHub.

---

## 8. Rollback

El modelo de push lo soporta naturalmente: como el evento lleva un **digest
explícito**, para hacer rollback CI manda el digest viejo. (Mejor que el modelo de
polling-del-registry, que sólo va siempre al más nuevo.)

---

## 9. Alternativas consideradas y descartadas

- **Broker central self-hosted (NATS / Redis / MQTT propio).** Descartado:
  mantenimiento + punto único de falla.
- **Pull / polling del registry (estilo Watchtower).** Viable y de cero infra,
  pero descartado por el requisito de "sin poll" y por rollback más débil.
- **Webhook con puerto público.** Descartado: superficie de ataque en internet.
- **Cloudflare Tunnel.** No descartado del todo — misma familia que Tailscale,
  válido como alternativa.

---

## 10. Preguntas abiertas (para la fase de init/CLI)

- ¿Cómo se une el agente a la tailnet en el init? (auth key / OAuth client; se
  necesita una credencial de Tailscale al instalar).
- Entrega de secrets a GitHub: ¿el CLI los setea vía API (PAT con acceso al repo)
  o imprime un bloque para pegar a mano? *(Sugerencia: imprimir-para-pegar por
  default, API opcional.)*
- Ruteo multi-servidor (staging / prod): cada agente con su tag/identidad; ¿cómo
  elige el target el workflow? (por hostname de tailnet / MagicDNS).
- Formato de los "pasos de deploy" locales: ¿script `.sh`, `docker-compose.yml`, o
  un manifiesto chico por servicio?
- Health check y rollback automático ante fallo de deploy.
- Almacenamiento de secrets en el server: archivos `0600` root vs. un secrets
  manager.

---

## 11. Stack sugerido (no prescriptivo)

- **Lenguaje:** Go — encaja con `tsnet` (Tailscale embebido), binario único, fácil
  bajo systemd.
- **Tailscale:** `tsnet` en el agente; Action `tailscale/github-action` con OAuth
  en CI.
- **Firma / verificación:** sigstore / cosign (keyless con OIDC).
- **Docker:** SDK de Docker para Go, o shellear a `docker` / `docker compose`.
- **Config del agente:** un archivo (YAML / TOML) en un dir root-only.

---

## 12. Primeros pasos sugeridos para la implementación

1. Scaffolding del **agente**: servidor HTTP mínimo con `tsnet`, endpoint
   `POST /deploy`, bindeado sólo a la tailnet.
2. Verificación cosign del digest recibido antes de cualquier acción.
3. `pull` por digest + ejecución del procedimiento de deploy local de un servicio.
4. Unit systemd + manejo de config.
5. **Workflow de ejemplo** de GitHub Actions (build, sign, join tailnet, POST).
6. Recién entonces, el **CLI de init** (ver preguntas abiertas, sección 10).