package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/accentiostudios/statio/internal/selfupdate"
	"github.com/spf13/cobra"
)

func newUpgradeCmd(current string) *cobra.Command {
	var target string
	var checkOnly, assumeYes bool
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
			warnLine("si el agente corre como servicio, reinícialo para usar el binario nuevo:")
			codeBlock("sudo systemctl restart statio-agent")
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "version", "", "versión específica a instalar (ej. v1.2.3)")
	cmd.Flags().BoolVar(&checkOnly, "check", false, "solo verificar si hay una versión nueva, sin instalar")
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "no preguntar confirmación")
	return cmd
}
