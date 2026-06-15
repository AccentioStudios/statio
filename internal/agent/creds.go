package agent

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/accentiostudios/push/internal/fsutil"
)

type npmplusCreds struct {
	BaseURL  string `json:"base_url"`
	Identity string `json:"identity"`
	Secret   string `json:"secret"`
}

type cloudflareCreds struct {
	APIToken string `json:"api_token"`
	ZoneID   string `json:"zone_id"`
}

func loadNPMplusCreds(path string) (*npmplusCreds, error) {
	var c npmplusCreds
	if err := loadSecretJSON(path, &c); err != nil {
		return nil, err
	}
	if c.Identity == "" || c.Secret == "" {
		return nil, fmt.Errorf("npmplus creds: identity/secret missing")
	}
	return &c, nil
}

func loadCloudflareCreds(path string) (*cloudflareCreds, error) {
	var c cloudflareCreds
	if err := loadSecretJSON(path, &c); err != nil {
		return nil, err
	}
	if c.APIToken == "" || c.ZoneID == "" {
		return nil, fmt.Errorf("cloudflare creds: api_token/zone_id missing")
	}
	return &c, nil
}

// loadSecretJSON validates 0600 root perms, then parses the JSON. Errors never echo
// the file contents.
func loadSecretJSON(path string, v any) error {
	if err := fsutil.CheckPerm(path); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read secret %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parse secret %s: invalid json", path)
	}
	return nil
}
