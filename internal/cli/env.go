package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/accentiostudios/statio/internal/env"
	"github.com/accentiostudios/statio/internal/fsutil"
	"github.com/spf13/cobra"
)

func newEnvCmd() *cobra.Command {
	var servicesDir string
	cmd := &cobra.Command{Use: "env", Short: "Manage a service's server-side base env"}
	cmd.PersistentFlags().StringVar(&servicesDir, "services-dir", "/etc/statio/services", "services directory")

	basePath := func(svc string) string { return filepath.Join(servicesDir, svc, "env.base.yaml") }

	var protected, required, secretStdin bool
	set := &cobra.Command{
		Use:   "set <service> KEY[=VALUE]",
		Short: "Set a base env key (value, or --secret-stdin for a secretRef)",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			svc := args[0]
			b, err := env.LoadBaseEnv(basePath(svc))
			if err != nil {
				return err
			}
			e := env.Entry{Protected: protected, Required: required}
			if secretStdin {
				key := args[1]
				val, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
				if err != nil {
					return err
				}
				refPath := filepath.Join(servicesDir, svc, "secrets", strings.ToLower(key))
				if err := os.MkdirAll(filepath.Dir(refPath), 0o700); err != nil {
					return err
				}
				if err := fsutil.SecureWrite(refPath, val, 0o600); err != nil {
					return err
				}
				e.Key = key
				e.SecretRef = "file://" + refPath
				e.Protected = true // secretRef must be protected (invariant)
			} else {
				k, v, ok := strings.Cut(args[1], "=")
				if !ok {
					return fmt.Errorf("expected KEY=VALUE (or use --secret-stdin)")
				}
				e.Key = k
				e.Value = &v
			}
			b.Set(e)
			if err := b.Save(basePath(svc)); err != nil {
				return err
			}
			fmt.Printf("set %s on %s\n", e.Key, svc)
			return nil
		},
	}
	set.Flags().BoolVar(&protected, "protected", false, "forbid CI from overriding this key")
	set.Flags().BoolVar(&required, "required", false, "require the deploy to supply this key")
	set.Flags().BoolVar(&secretStdin, "secret-stdin", false, "read the secret value from stdin (stored as secretRef)")

	rm := &cobra.Command{
		Use:   "rm <service> KEY",
		Short: "Remove a base env key",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			b, err := env.LoadBaseEnv(basePath(args[0]))
			if err != nil {
				return err
			}
			if !b.Remove(args[1]) {
				return fmt.Errorf("key %q not found", args[1])
			}
			return b.Save(basePath(args[0]))
		},
	}

	list := &cobra.Command{
		Use:   "list <service>",
		Short: "List base env keys (secret/protected values are redacted)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			b, err := env.LoadBaseEnv(basePath(args[0]))
			if err != nil {
				return err
			}
			for _, e := range b.Entries {
				val := "<required>"
				switch {
				case e.SecretRef != "" || e.Protected:
					val = "<redacted>"
				case e.Value != nil:
					val = *e.Value
				}
				flags := ""
				if e.Protected {
					flags += " protected"
				}
				if e.Required {
					flags += " required"
				}
				fmt.Printf("%s=%s%s\n", e.Key, val, flags)
			}
			return nil
		},
	}

	cmd.AddCommand(set, rm, list)
	return cmd
}
