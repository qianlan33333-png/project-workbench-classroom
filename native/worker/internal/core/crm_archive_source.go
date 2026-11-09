package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const CRMArchiveSource = "crm-archive-cli"

type crmArchiveConfig struct {
	CLI         string `json:"cli"`
	ConfigDir   string `json:"configDir"`
	Origin      string `json:"origin"`
	TokenEnv    string `json:"tokenEnv"`
	RequestFile string `json:"requestFile"`
}

func exportCRMArchive(ctx context.Context, conn Connection) (*os.File, string, error) {
	var cfg crmArchiveConfig
	if json.Unmarshal([]byte(os.Getenv(conn.SecretEnv)), &cfg) != nil || !filepath.IsAbs(cfg.CLI) || !filepath.IsAbs(cfg.ConfigDir) || !filepath.IsAbs(cfg.RequestFile) || !validHTTPSource(cfg.Origin) || !ValidID(cfg.TokenEnv) || !strings.HasPrefix(cfg.TokenEnv, "WORKBENCH_SOURCE_") {
		return nil, "", fmt.Errorf("CRM archive CLI connection is not configured")
	}
	token := os.Getenv(cfg.TokenEnv)
	if token == "" {
		return nil, "", fmt.Errorf("CRM archive CLI machine token is not configured")
	}
	dir, err := os.MkdirTemp("", "workbench-crm-archive-*")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "facts.json")
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, cfg.CLI, "--timeout", "90s", "customers", "archive-facts", "export", "--input", cfg.RequestFile, "--output", path)
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "AICRM_CLI_") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	cmd.Env = append(cmd.Env, "AICRM_CLI_CONFIG_DIR="+cfg.ConfigDir, "AICRM_CLI_TOKEN="+token, "AICRM_CLI_CREDENTIAL_ORIGIN="+cfg.Origin, "AICRM_CLI_NO_UPDATE_NOTIFIER=1")
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", fmt.Errorf("CRM archive export failed; check the CLI credential and source authorization")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", fmt.Errorf("CRM archive export did not produce a file")
	}
	keep := false
	defer func() {
		if !keep {
			file.Close()
		}
	}()
	if err = os.Remove(path); err != nil {
		return nil, "", err
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, (256<<20)+1))
	if err != nil || n == 0 || n > 256<<20 {
		return nil, "", fmt.Errorf("CRM archive is empty, incomplete or over 256 MiB")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}
	keep = true
	return file, hex.EncodeToString(hash.Sum(nil)), nil
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
func streamCRMArchive(reader io.Reader, spec JSONSource, accept func(Record) error) error {
	if !validCRMArchiveCollection(spec.ArrayField) || spec.validate() != nil {
		return ErrInvalid
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
		return err
	}
	budget.reset()
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("%w: trailing CRM archive data", ErrInvalid)
	}
	hash.Write([]byte{']'})
	entry := manifest.Collections[spec.ArrayField]
	if !found || !complete || !readOnly || contract != "crm-production-read-facts.v1" || snapshot == "" || asOf == "" || !manifest.Complete || manifest.Version != "crm-read-facts-manifest.v1" || manifest.HashAlgorithm != "sha256" || manifest.Canonicalization != "go-json-number-preserving-v1" || manifest.Snapshot != snapshot || manifest.AsOf != asOf || entry.Count == nil || *entry.Count != count || entry.SHA256 != hex.EncodeToString(hash.Sum(nil)) {
		return fmt.Errorf("%w: CRM archive manifest or collection mismatch", ErrInvalid)
	}
	return nil
}
