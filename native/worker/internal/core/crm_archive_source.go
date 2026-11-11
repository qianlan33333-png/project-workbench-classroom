package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const CRMArchiveSource = "crm-archive-cli"

type crmArchiveConfig struct {
	CLI             string `json:"cli"`
	Origin          string `json:"origin"`
	ClientID        string `json:"clientId"`
	ClientSecretEnv string `json:"clientSecretEnv"`
	RequestFile     string `json:"requestFile"`
}

func exportCRMArchive(ctx context.Context, conn Connection) (*os.File, error) {
	return exportCRMArchiveRequest(ctx, conn, "")
}
func exportCRMArchiveRequest(ctx context.Context, conn Connection, request string) (*os.File, error) {
	var cfg crmArchiveConfig
	if json.Unmarshal([]byte(os.Getenv(conn.SecretEnv)), &cfg) != nil || !filepath.IsAbs(cfg.CLI) || !filepath.IsAbs(cfg.RequestFile) || !validHTTPSource(cfg.Origin) || cfg.ClientID == "" || !ValidID(cfg.ClientSecretEnv) || !strings.HasPrefix(cfg.ClientSecretEnv, "WORKBENCH_SOURCE_") {
		return nil, fmt.Errorf("CRM archive CLI connection is not configured")
	}
	if request != "" {
		if !filepath.IsAbs(request) {
			return nil, ErrInvalid
		}
		cfg.RequestFile = request
	}
	origin, err := url.Parse(cfg.Origin)
	if err != nil || origin.Path != "" || origin.RawQuery != "" || origin.ForceQuery {
		return nil, ErrInvalid
	}
	secret := os.Getenv(cfg.ClientSecretEnv)
	if secret == "" {
		return nil, fmt.Errorf("CRM archive CLI machine secret is not configured")
	}
	dir, err := os.MkdirTemp("", "workbench-crm-archive-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	profile := fmt.Sprintf(`{"current":"workbench","profiles":{"workbench":{"origin":%q}}}`, cfg.Origin)
	if err = os.WriteFile(filepath.Join(dir, "profiles.json"), []byte(profile), 0600); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "facts.json")
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	token, err := crmArchiveToken(ctx, cfg, secret)
	if err != nil {
		return nil, err
	}
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(name, "AICRM_CLI_") && name != cfg.ClientSecretEnv && name != conn.SecretEnv {
			env = append(env, entry)
		}
	}
	env = append(env, "AICRM_CLI_CONFIG_DIR="+dir, "AICRM_CLI_NO_UPDATE_NOTIFIER=1")
	cmd := exec.CommandContext(ctx, cfg.CLI, "--timeout", "90s", "customers", "archive-facts", "export", "--input", cfg.RequestFile, "--output", path)
	cmd.Env = append(env, "AICRM_CLI_TOKEN="+token, "AICRM_CLI_CREDENTIAL_ORIGIN="+cfg.Origin)
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("CRM archive export failed; check the CLI credential and source authorization")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("CRM archive export did not produce a file")
	}
	keep := false
	defer func() {
		if !keep {
			file.Close()
		}
	}()
	if err = os.Remove(path); err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || info.Size() == 0 || info.Size() > 256<<20 {
		return nil, fmt.Errorf("CRM archive is empty, incomplete or over 256 MiB")
	}
	keep = true
	return file, nil
}

// CRM's headless machine login uses the same client_credentials contract as
// the CLI. The CLI exports human credential files only.
func crmArchiveToken(ctx context.Context, cfg crmArchiveConfig, secret string) (string, error) {
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {cfg.ClientID}, "client_secret": {secret}, "audience": {"external_integration"}, "scope": {"read"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Origin+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", ErrInvalid
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := http.Client{Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("CRM machine authorization unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("CRM machine authorization returned HTTP %d", resp.StatusCode)
	}
	var issued struct {
		Token string `json:"access_token"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&issued); err != nil || issued.Token == "" {
		return "", fmt.Errorf("CRM machine token missing")
	}
	return issued.Token, nil
}

func validCRMArchiveCollection(name string) bool {
	switch name {
	case "customers", "users", "identities", "orders", "messages", "surveys", "answers", "activation_receipts":
		return true
	}
	return false
}

func readArchiveObject(dec *json.Decoder, budget *sourceBudgetReader, visit func(string) error) error {
	start, err := dec.Token()
	if err != nil || start != json.Delim('{') {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for {
		budget.reset()
		if !dec.More() {
			break
		}
		token, err := dec.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] || len(seen) >= 64 {
			return ErrInvalid
		}
		seen[key] = true
		if err = visit(key); err != nil {
			return err
		}
	}
	end, err := dec.Token()
	if err != nil || end != json.Delim('}') {
		return ErrInvalid
	}
	return nil
}

// Validate the selected collection against the CRM export manifest before the
// existing synchronization task can publish any of its staged records.
func streamCRMArchive(reader io.Reader, spec JSONSource, accept func(Record) error) (string, error) {
	if !validCRMArchiveCollection(spec.ArrayField) || spec.validate() != nil {
		return "", ErrInvalid
	}
	budget := &sourceBudgetReader{reader: reader}
	budget.reset()
	dec := json.NewDecoder(budget)
	dec.UseNumber()
	var contract, snapshot, asOf string
	var complete, readOnly, found bool
	var manifest struct {
		Version          string `json:"version"`
		Complete         bool   `json:"complete"`
		HashAlgorithm    string `json:"hash_algorithm"`
		Canonicalization string `json:"canonicalization"`
		Snapshot         string `json:"source_snapshot_id"`
		AsOf             string `json:"source_as_of"`
		Collections      map[string]struct {
			Count  *int   `json:"row_count"`
			SHA256 string `json:"sha256"`
		} `json:"collections"`
	}
	hash := sha256.New()
	hash.Write([]byte{'['})
	count := 0
	inspect := func(row map[string]any) error {
		raw, err := json.Marshal(row)
		if err != nil {
			return ErrInvalid
		}
		if count > 0 {
			hash.Write([]byte{','})
		}
		hash.Write(raw)
		count++
		return nil
	}
	facts := func(key string) error {
		switch key {
		case "contract":
			return dec.Decode(&contract)
		case "complete":
			return dec.Decode(&complete)
		case "read_only":
			return dec.Decode(&readOnly)
		case "source_snapshot_id":
			return dec.Decode(&snapshot)
		case "source_as_of":
			return dec.Decode(&asOf)
		case "manifest":
			return dec.Decode(&manifest)
		case spec.ArrayField:
			start, err := dec.Token()
			if err != nil || start != json.Delim('[') {
				return ErrInvalid
			}
			found = true
			return streamSourceRows(dec, budget, spec, accept, inspect)
		default:
			return skipJSON(dec, budget.reset)
		}
	}
	if err := readArchiveObject(dec, budget, func(key string) error {
		switch key {
		case "data":
			return readArchiveObject(dec, budget, facts)
		case "error":
			var value json.RawMessage
			if dec.Decode(&value) != nil || string(value) != "null" {
				return ErrInvalid
			}
			return nil
		default:
			return skipJSON(dec, budget.reset)
		}
	}); err != nil {
		return "", err
	}
	budget.reset()
	if _, err := dec.Token(); err != io.EOF {
		return "", fmt.Errorf("%w: trailing CRM archive data", ErrInvalid)
	}
	hash.Write([]byte{']'})
	entry := manifest.Collections[spec.ArrayField]
	if !found || !complete || !readOnly || contract != "crm-production-read-facts.v1" || snapshot == "" || asOf == "" || !manifest.Complete || manifest.Version != "crm-read-facts-manifest.v1" || manifest.HashAlgorithm != "sha256" || manifest.Canonicalization != "go-json-number-preserving-v1" || manifest.Snapshot != snapshot || manifest.AsOf != asOf || entry.Count == nil || *entry.Count != count || entry.SHA256 != hex.EncodeToString(hash.Sum(nil)) {
		return "", fmt.Errorf("%w: CRM archive manifest or collection mismatch", ErrInvalid)
	}
	return entry.SHA256, nil
}

// ExportOperationsCRMArchive is the private source adapter entry. It reuses
// exactly the installed CLI machine authentication/export implementation.
func ExportOperationsCRMArchive(ctx context.Context, request, output string) error {
	if !filepath.IsAbs(request) || !filepath.IsAbs(output) {
		return ErrInvalid
	}
	source, e := exportCRMArchiveRequest(ctx, Connection{SecretEnv: "WORKBENCH_SOURCE_CRM_ARCHIVE"}, request)
	if e != nil {
		return e
	}
	defer source.Close()
	f, e := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = io.Copy(f, source)
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		os.Remove(output)
	}
	return e
}
