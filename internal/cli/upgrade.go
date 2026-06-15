package cli

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/accentiostudios/statio/internal/selfupdate"
	"github.com/spf13/cobra"
)

// agentUnit is the systemd unit `statio init server` installs on a server.
const agentUnit = "statio-agent"

// agentUnitActive reports whether the agent unit exists and is currently running.
// It is false on non-systemd hosts (e.g. a dev machine running only the CLI), so the
// upgrade never tries to touch a service that isn't there.
func agentUnitActive() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	// `is-active --quiet` exits 0 only when the unit is loaded and running.
	return exec.Command("systemctl", "is-active", "--quiet", agentUnit).Run() == nil
}

func newUpgradeCmd(current string) *cobra.Command {
	var target string
	var checkOnly, assumeYes, noRestart bool
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Update statio to the latest release (self-update)",
		Long: "Descarga la última release de statio, verifica su checksum y reemplaza\n" +
			"el binario actual. Re-ejecutable: no hace nada si ya estás al día.",
		RunE: func(c *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			tag := target
			if tag == "" {
				latest, err := selfupdate.Latest(ctx)
				if err != nil {
					return fmt.Errorf("no pude consultar la última versión: %w", err)
				}
				tag = latest
			}

			// Already current (only meaningful when comparing against latest).
			if target == "" && selfupdate.IsVersion(current) && !selfupdate.Outdated(current, tag) {
				okLine("statio ya está al día (%s, última %s)", current, tag)
				return nil
			}

			info("Instalada: %s", current)
			info("Objetivo:  %s", tag)
			if checkOnly {
				if selfupdate.Outdated(current, tag) {
					warnLine("hay una versión nueva: %s → %s", current, tag)
					codeBlock("statio upgrade")
				} else {
					okLine("estás al día")
				}
				return nil
			}
			if !assumeYes && interactive() {
				ok, err := confirm(fmt.Sprintf("¿Actualizar statio a %s?", tag))
				if err != nil {
					return err
				}
				if !ok {
					info("cancelado.")
					return nil
				}
			}
			if err := selfupdate.Apply(ctx, tag, info); err != nil {
				return err
			}
			okLine("statio actualizado a %s", tag)

			// On a server the agent runs as systemd; restart it so it loads the new
			// binary. We only touch it when the unit is actually active — on a dev
			// machine (CLI-only) there's nothing to restart.
			switch {
			case noRestart:
				warnLine("si el agente corre como servicio, reinícialo para usar el binario nuevo:")
				codeBlock("sudo systemctl restart statio-agent")
			case agentUnitActive():
				if err := exec.Command("systemctl", "restart", agentUnit).Run(); err != nil {
					warnLine("no pude reiniciar el agente automáticamente (¿corres como root?): %v", err)
					codeBlock("sudo systemctl restart statio-agent")
				} else {
					okLine("agente reiniciado (%s) con el binario nuevo", agentUnit)
				}
			default:
				info("(no hay un agente %s activo en esta máquina; nada que reiniciar)", agentUnit)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "version", "", "versión específica a instalar (ej. v1.2.3)")
	cmd.Flags().BoolVar(&checkOnly, "check", false, "solo verificar si hay una versión nueva, sin instalar")
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "no preguntar confirmación")
	cmd.Flags().BoolVar(&noRestart, "no-restart", false, "no reiniciar el agente statio-agent tras actualizar")
	return cmd
}
