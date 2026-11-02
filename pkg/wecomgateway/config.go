package wecomgateway

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Credential references, never inline secrets. Config files can be templated.
type Config struct {
	BaseURL           string `json:"baseUrl"`
	APIKeyEnv         string `json:"apiKeyEnv,omitempty"`
	APIKeyFile        string `json:"apiKeyFile,omitempty"`
	WebhookSecretFile string `json:"webhookSecretFile"`
	WorkbenchURL      string `json:"workbenchUrl,omitempty"`
	PasswordEnv       string `json:"passwordEnv,omitempty"`
	PasswordFile      string `json:"passwordFile,omitempty"`
}

func ReadConfig(path string) (Config, error) {
	var c Config
	b, e := os.ReadFile(path)
	if e != nil {
		return c, errors.New("cannot read config")
	}
	e = json.Unmarshal(b, &c)
	if e != nil {
		return c, errors.New("invalid config JSON")
	}
	for _, p := range []*string{&c.APIKeyFile, &c.WebhookSecretFile, &c.PasswordFile} {
		if *p != "" && !filepath.IsAbs(*p) {
			*p = filepath.Join(filepath.Dir(path), *p)
		}
	}
	return c, ValidateURL(c.BaseURL)
}
func ReadSecret(env, file string) (string, error) {
	if env != "" && file != "" {
		return "", errors.New("choose one credential reference")
	}
	if env != "" {
		v := strings.TrimSpace(os.Getenv(env))
		if v != "" {
			return v, nil
		}
		return "", errors.New("credential environment variable missing")
	}
	if file != "" {
		st, e := os.Lstat(file)
		if e != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
			return "", errors.New("credential file must be a regular private file (0600)")
		}
		b, e := os.ReadFile(file)
		if e == nil && len(b) < 16384 && strings.TrimSpace(string(b)) != "" {
			return strings.TrimSpace(string(b)), nil
		}
	}
	return "", errors.New("credential reference unavailable")
}
func WritePrivate(path string, b []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".gateway-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e == nil {
		e = os.Rename(name, path)
	}
	return e
}
