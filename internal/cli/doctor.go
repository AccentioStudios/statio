package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/accentiostudios/statio/internal/config"
	"github.com/accentiostudios/statio/internal/selfupdate"
	"github.com/spf13/cobra"
)

// newDoctorCmd is a `flutter doctor`-style environment check: it reports what is
// installed/configured and flags the gaps, without changing anything.
func newDoctorCmd(version string) *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check your environment for common problems",
		RunE: func(c *cobra.Command, _ []string) error {
			banner("statio doctor", "diagnóstico del entorno")
			ok := true

			doctorVersion(version)
			doctorDocker(&ok)
			doctorTool("git", "auto-detect del repo en `statio init repo`", false, &ok)
			doctorTool("gh", "subir secrets con `gh secret set` (cliente)", true, &ok)
			doctorTool("cosign", "firmar imagen+payload en CI (no en el server)", true, &ok)
			doctorConfig(configPath, &ok)
			doctorService()
			doctorGitHub()

			fmt.Println()
			if ok {
				okLine("todo en orden")
			} else {
				warnLine("revisa los puntos marcados con ✗ arriba")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "/etc/statio/config.yaml", "ruta del config.yaml del agente")
	return cmd
}

func doctorVersion(version string) {
	line := fmt.Sprintf("statio %s · %s/%s", version, runtime.GOOS, runtime.GOARCH)
	if !selfupdate.IsVersion(version) {
		warnLine("%s (build de desarrollo — sin chequeo de versión)", line)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	latest, err := selfupdate.Latest(ctx)
	if err != nil {
		okLine("%s", line)
		return
	}
	if selfupdate.Outdated(version, latest) {
		warnLine("%s — hay una nueva: %s (ejecuta `statio upgrade`)", line, latest)
	} else {
		okLine("%s (última)", line)
	}
}

func doctorDocker(ok *bool) {
	if out, err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").Output(); err == nil {
		okLine("docker %s — daemon accesible", strings.TrimSpace(string(out)))
		return
	}
	if _, err := exec.LookPath("docker"); err == nil {
		failLine("docker instalado pero el daemon no responde (¿está corriendo? ¿permisos del socket?)")
	} else {
		failLine("docker no encontrado — requerido en el servidor")
	}
	*ok = false
}

func doctorTool(name, hint string, optional bool, ok *bool) {
	if _, err := exec.LookPath(name); err != nil {
		if optional {
			warnLine("%s no encontrado — %s", name, hint)
		} else {
			failLine("%s no encontrado — %s", name, hint)
			*ok = false
		}
		return
	}
	ver := ""
	if out, err := exec.Command(name, "--version").Output(); err == nil {
		ver = " " + strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	}
	okLine("%s%s — %s", name, ver, hint)
}

func doctorConfig(path string, ok *bool) {
	if _, err := os.Stat(path); err != nil {
		info("config del agente: %s no existe (normal si esta máquina no es el servidor)", path)
		return
	}
	if _, err := config.Load(path); err != nil {
		failLine("config %s inválida: %v", path, err)
		*ok = false
		return
	}
	okLine("config del agente: %s (válida)", path)
}

func doctorService() {
	if runtime.GOOS != "linux" {
		return
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return
	}
	out, _ := exec.Command("systemctl", "is-active", "statio-agent").Output()
	switch state := strings.TrimSpace(string(out)); state {
	case "active":
		okLine("servicio statio-agent: active")
	case "":
		info("servicio statio-agent: estado desconocido")
	default:
		info("servicio statio-agent: %s", state)
	}
}

func doctorGitHub() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := selfupdate.Latest(ctx); err != nil {
		warnLine("GitHub Releases no accesible: %v", err)
		return
	}
	okLine("GitHub Releases accesible")
}
