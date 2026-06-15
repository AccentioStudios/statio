# Contribuir a statio

Gracias por querer aportar. Esta guía cubre cómo levantar el proyecto, el estilo que seguimos y cómo
proponer cambios.

## Requisitos

- **Go** — la versión está en [`go.mod`](go.mod); usa esa o una más nueva.
- **Docker** — para probar el agente localmente.
- **git**, y opcionalmente una cuenta de **Tailscale** para pruebas end-to-end.

## Levantar el proyecto

```sh
git clone https://github.com/accentiostudios/statio
cd statio
go build ./...                    # compila todo
go build -o statio ./cmd/statio   # el binario único
```

## Antes de abrir un PR

Ejecuta lo mismo que valida CI; todo debe quedar en verde:

```sh
go build ./...
go vet ./...
go test ./...
gofmt -l .        # no debe listar ningún archivo
```

## Estilo

- Go estándar, formateado con `gofmt`. Evita dependencias nuevas salvo que sean necesarias;
  justifícalas en el PR.
- Comentarios y documentación en **español neutral**.
- Mensajes de commit en **inglés**, formato [Conventional Commits](https://www.conventionalcommits.org)
  (`feat:`, `fix:`, `docs:`, `refactor:`, `chore:`…, con scope opcional).

## Estructura del código

- `cmd/statio` — entrypoint del binario.
- `internal/` — todo el código: `cli`, `agent`, `deploy`, `compose`, `verify`, `spec`, `selfupdate`…
- `action/` — la composite Action de GitHub que corre en CI.
- `docs/architecture.md` — arquitectura, pipeline de deploy, contrato de wire y modelo de seguridad.
  **Léelo antes de tocar** el agente, el verificador o el generador de compose.

## Seguridad

statio tiene un modelo de seguridad explícito (firma cosign, anclas server-side, los invariantes
documentados en [`docs/architecture.md`](docs/architecture.md) §6). Si tu cambio toca verificación,
parsing del payload, generación de compose o manejo de secretos, **explica en el PR cómo preserva esos
invariantes**.

¿Encontraste una vulnerabilidad? No abras un issue público: usa el reporte privado de GitHub
(**Security → Report a vulnerability**) o contacta a los maintainers en privado.

## Proponer un cambio

1. Haz fork y crea una rama desde `main`.
2. Commits en formato Conventional Commits.
3. Abre un PR con una descripción clara: **qué** cambia, **por qué**, y **cómo lo probaste**.
4. CI debe pasar.

## Releases (maintainers)

Los releases se cortan con un tag — un push a `main` no publica nada:

```sh
git tag vX.Y.Z
git push origin vX.Y.Z
```

Eso dispara GoReleaser ([`.github/workflows/release.yml`](.github/workflows/release.yml)): compila los
binarios linux/darwin × amd64/arm64, los firma con cosign keyless y publica el GitHub Release con
`checksums.txt`. `install.sh` y `statio upgrade` consumen ese release.
