package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-openapi/jsonpointer"
)

// HTTP credentials and optional standard form login live only in the owner's
// environment. There are no model-controlled URLs, headers or login parameters.
type HTTPSourceSecret struct {
	Headers  map[string]string `json:"headers"`
	Username string            `json:"username"`
	Password string            `json:"password"`
	Login    *struct {
		URL           string            `json:"url"`
		Fields        map[string]string `json:"fields"`
		CSRFCookie    string            `json:"csrfCookie"`
		CSRFField     string            `json:"csrfField"`
		SuccessStatus int               `json:"successStatus"`
	} `json:"login"`
}

func validHTTPSource(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == ""
}
func sourceHeaders(req *http.Request, secret HTTPSourceSecret) {
	for k, v := range secret.Headers {
		req.Header.Set(k, v)
	}
	if secret.Username != "" {
		req.SetBasicAuth(secret.Username, secret.Password)
	}
}
func httpSourceClient(ctx context.Context, source string, secret HTTPSourceSecret) (*http.Client, error) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 10 * time.Minute, Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if secret.Login == nil {
		return client, nil
	}
	login, e := url.Parse(secret.Login.URL)
	if e != nil {
		return nil, ErrInvalid
	}
	origin, e := url.Parse(source)
	if e != nil || login.Scheme != origin.Scheme || login.Host != origin.Host || login.User != nil || login.Fragment != "" {
		return nil, ErrInvalid
	}
	req, e := http.NewRequestWithContext(ctx, "GET", login.String(), nil)
	if e != nil {
		return nil, ErrInvalid
	}
	sourceHeaders(req, secret)
	response, e := client.Do(req)
	if e != nil {
		return nil, fmt.Errorf("source login unavailable")
	}
	io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("source login returned HTTP %d", response.StatusCode)
	}
	values := url.Values{}
	for k, v := range secret.Login.Fields {
		values.Set(k, v)
	}
	if secret.Login.CSRFCookie != "" {
		token := ""
		for _, cookie := range jar.Cookies(login) {
			if cookie.Name == secret.Login.CSRFCookie {
				token = cookie.Value
			}
		}
		if token == "" || secret.Login.CSRFField == "" {
			return nil, fmt.Errorf("source login CSRF missing")
		}
		values.Set(secret.Login.CSRFField, token)
	}
	req, e = http.NewRequestWithContext(ctx, "POST", login.String(), strings.NewReader(values.Encode()))
	if e != nil {
		return nil, ErrInvalid
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", origin.Scheme+"://"+origin.Host)
	sourceHeaders(req, secret)
	response, e = client.Do(req)
	if e != nil {
		return nil, fmt.Errorf("source login unavailable")
	}
	io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	response.Body.Close()
	expected := secret.Login.SuccessStatus
	if expected == 0 {
		expected = 200
	}
	if response.StatusCode != expected {
		return nil, fmt.Errorf("source credentials rejected (HTTP %d)", response.StatusCode)
	}
	return client, nil
}
func downloadJSON(ctx context.Context, conn Connection) (*os.File, string, error) {
	if !validHTTPSource(conn.Location) {
		return nil, "", ErrInvalid
	}
	var secret HTTPSourceSecret
	if conn.SecretEnv != "" {
		if e := json.Unmarshal([]byte(os.Getenv(conn.SecretEnv)), &secret); e != nil {
			return nil, "", fmt.Errorf("source credentials are not configured")
		}
	}
	client, e := httpSourceClient(ctx, conn.Location, secret)
	if e != nil {
		return nil, "", e
	}
	req, e := http.NewRequestWithContext(ctx, "GET", conn.Location, nil)
	if e != nil {
		return nil, "", ErrInvalid
	}
	sourceHeaders(req, secret)
	response, e := client.Do(req)
	if e != nil {
		return nil, "", fmt.Errorf("source download unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, "", fmt.Errorf("source download returned HTTP %d", response.StatusCode)
	}
	file, e := os.CreateTemp("", "workbench-source-*.json")
	if e != nil {
		return nil, "", e
	}
	// Unlink while open: an interrupted worker cannot leave raw snapshots in /tmp.
	if e = os.Remove(file.Name()); e != nil {
		file.Close()
		return nil, "", e
	}
	keep := false
	defer func() {
		if !keep {
			file.Close()
			os.Remove(file.Name())
		}
	}()
	digest := sha256.New()
	n, e := io.Copy(io.MultiWriter(file, digest), io.LimitReader(response.Body, (256<<20)+1))
	if e != nil || n > 256<<20 {
		return nil, "", fmt.Errorf("source download incomplete or over 256 MiB")
	}
	if _, e = file.Seek(0, io.SeekStart); e != nil {
		return nil, "", e
	}
	keep = true
	return file, hex.EncodeToString(digest.Sum(nil)), nil
}

// ArrayField is either empty for a root array or an exact top-level field name.
// Per-record mapping uses the already adopted RFC6901 library, not a new DSL.
type JSONSource struct {
	ArrayField  string            `json:"arrayField,omitempty"`
	KeyPointer  string            `json:"keyPointer,omitempty"`
	KeyPointers []string          `json:"keyPointers,omitempty"`
	RowKey      string            `json:"rowKey,omitempty"`
	Fields      map[string]string `json:"fields"`
}

func (s JSONSource) validate() error {
	if len(s.Fields) == 0 || len(s.Fields) > 128 {
		return ErrInvalid
	}
	keys := s.KeyPointers
	if s.RowKey != "" {
		if s.RowKey != "content-occurrence" || s.KeyPointer != "" || len(keys) != 0 {
			return ErrInvalid
		}
	} else if s.KeyPointer != "" {
		if len(keys) != 0 {
			return ErrInvalid
		}
		keys = []string{s.KeyPointer}
	} else if len(keys) < 2 || len(keys) > 8 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, key := range keys {
		if key == "" || seen[key] {
			return ErrInvalid
		}
		if _, e := jsonpointer.New(key); e != nil {
			return ErrInvalid
		}
		seen[key] = true
	}
	return validateSelection(Section{Select: s.Fields})
}

func (s JSONSource) recordID(row map[string]any) (string, error) {
	if s.RowKey == "content-occurrence" {
		raw, err := json.Marshal(row)
		if err != nil {
			return "", ErrInvalid
		}
		digest := sha256.Sum256(raw)
		return "row_" + hex.EncodeToString(digest[:]), nil
	}
	keys := s.KeyPointers
	if s.KeyPointer != "" {
		keys = []string{s.KeyPointer}
	}
	values := make([]any, 0, len(keys))
	for _, path := range keys {
		pointer, err := jsonpointer.New(path)
		if err != nil {
			return "", ErrInvalid
		}
		value, _, err := pointer.Get(row)
		if err != nil {
			return "", fmt.Errorf("%w: source identity missing", ErrInvalid)
		}
		switch v := value.(type) {
		case string:
			if v == "" {
				return "", fmt.Errorf("%w: source identity empty", ErrInvalid)
			}
		case json.Number:
		default:
			return "", fmt.Errorf("%w: source identity must be string or number", ErrInvalid)
		}
		values = append(values, value)
	}
	if s.KeyPointer != "" {
		return fmt.Sprint(values[0]), nil
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", ErrInvalid
	}
	digest := sha256.Sum256(raw)
	return "ck_" + hex.EncodeToString(digest[:]), nil
}
func skipJSON(dec *json.Decoder, resetBudget func()) error {
	token, e := dec.Token()
	if e != nil {
		return e
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delim != '[' && delim != '{' {
		return ErrInvalid
	}
	depth := 1
	for depth > 0 {
		if resetBudget != nil {
			resetBudget()
		}
		token, e = dec.Token()
		if e != nil {
			return e
		}
		if d, ok := token.(json.Delim); ok {
			if d == '[' || d == '{' {
				depth++
			} else {
				depth--
			}
		}
	}
	return nil
}

type sourceBudgetReader struct {
	reader    io.Reader
	remaining int64
}

func (r *sourceBudgetReader) reset() { r.remaining = 512 << 10 }
func (r *sourceBudgetReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, fmt.Errorf("%w: source JSON value exceeds read budget", ErrInvalid)
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, e := r.reader.Read(p)
	r.remaining -= int64(n)
	return n, e
}

// The opening array delimiter has already been consumed by the source reader.
func streamSourceRows(dec *json.Decoder, bounded *sourceBudgetReader, spec JSONSource, accept func(Record) error, inspect func(map[string]any) error) error {
	occurrences := map[string]int{}
	for {
		bounded.reset()
		if !dec.More() {
			break
		}
		var row map[string]any
		before := dec.InputOffset()
		if e := dec.Decode(&row); e != nil {
			return fmt.Errorf("%w: malformed source record", ErrInvalid)
		}
		if row == nil || dec.InputOffset()-before > 512<<10 {
			return fmt.Errorf("%w: invalid or oversized source record", ErrInvalid)
		}
		if inspect != nil {
			if e := inspect(row); e != nil {
				return e
			}
		}
		if accept == nil {
			continue
		}
		id, e := spec.recordID(row)
		if e != nil {
			return e
		}
		if spec.RowKey == "content-occurrence" {
			occurrences[id]++
			id += "_" + strconv.Itoa(occurrences[id])
		}
		record := Record{ID: id, Fields: map[string]any{}}
		for alias, path := range spec.Fields {
			pointer, _ := jsonpointer.New(path)
			value, _, e := pointer.Get(row)
			if e != nil {
				record.Fields[alias] = nil
			} else {
				record.Fields[alias] = value
			}
		}
		if e := accept(record); e != nil {
			return e
		}
	}
	end, e := dec.Token()
	if e != nil || end != json.Delim(']') {
		return ErrInvalid
	}
	return nil
}

func streamJSON(reader io.Reader, spec JSONSource, accept func(Record) error) error {
	if e := spec.validate(); e != nil {
		return e
	}
	bounded := &sourceBudgetReader{reader: reader}
	bounded.reset()
	dec := json.NewDecoder(bounded)
	dec.UseNumber()
	first, e := dec.Token()
	if e != nil {
		return ErrInvalid
	}
	found := false
	array := func() error {
		if found {
			return fmt.Errorf("%w: duplicate source array", ErrInvalid)
		}
		found = true
		return streamSourceRows(dec, bounded, spec, accept, nil)
	}
	if spec.ArrayField == "" {
		if first != json.Delim('[') {
			return ErrInvalid
		}
		if e = array(); e != nil {
			return e
		}
	} else {
		if first != json.Delim('{') {
			return ErrInvalid
		}
		for dec.More() {
			bounded.reset()
			key, e := dec.Token()
			if e != nil {
				return ErrInvalid
			}
			if key == spec.ArrayField {
				token, e := dec.Token()
				if e != nil || token != json.Delim('[') {
					return ErrInvalid
				}
				if e = array(); e != nil {
					return e
				}
			} else if e = skipJSON(dec, nil); e != nil {
				return ErrInvalid
			}
		}
		end, e := dec.Token()
		if e != nil || end != json.Delim('}') {
			return ErrInvalid
		}
	}
	bounded.reset()
	var extra any
	if !found || dec.Decode(&extra) != io.EOF {
		return fmt.Errorf("%w: missing array or trailing source data", ErrInvalid)
	}
	return nil
}
