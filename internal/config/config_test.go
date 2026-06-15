package config

func valid() *Config {
	c := &Config{
		Hostname: "statio",
		Cosign: CosignConfig{
			OIDCIssuer: "https://token.actions.githubusercontent.com",
			Identity:   "https://github.com/org/repo/.github/workflows/deploy.yml@refs/heads/main",
		},
	}
	c.applyDefaults()
	return c
}
