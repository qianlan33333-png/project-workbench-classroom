package workbench

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	gw "projectworkbench/pkg/wecomgateway"
)

const crmSchema = `
CREATE TABLE IF NOT EXISTS gateway_crm_receipts (grant TEXT NOT NULL, request TEXT NOT NULL, hash TEXT NOT NULL, actor TEXT NOT NULL, session_id TEXT NOT NULL, result TEXT NOT NULL, created_at INTEGER NOT NULL, PRIMARY KEY(grant,request));
CREATE TABLE IF NOT EXISTS gateway_crm_routes (grant TEXT PRIMARY KEY, url TEXT NOT NULL);
-- Store only a stable monotonic index; transcripts remain in the existing inbox.
CREATE TABLE IF NOT EXISTS gateway_history_index(seq INTEGER PRIMARY KEY AUTOINCREMENT, message_id TEXT NOT NULL UNIQUE REFERENCES inbox(id) ON DELETE CASCADE, session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE);
CREATE INDEX IF NOT EXISTS gateway_session_history ON gateway_history_index(session_id,seq);
CREATE TRIGGER IF NOT EXISTS gateway_history_insert AFTER INSERT ON inbox WHEN NEW.source='wecom-gateway' AND json_extract(NEW.data,'$.gateway.historyRole') IS NOT NULL
BEGIN INSERT OR IGNORE INTO gateway_history_index(message_id,session_id) VALUES(NEW.id,NEW.session_id); END;
CREATE TRIGGER IF NOT EXISTS gateway_history_tag AFTER UPDATE OF data ON inbox WHEN NEW.source='wecom-gateway' AND json_extract(OLD.data,'$.gateway.historyRole') IS NULL AND json_extract(NEW.data,'$.gateway.historyRole') IS NOT NULL
BEGIN INSERT OR IGNORE INTO gateway_history_index(message_id,session_id) VALUES(NEW.id,NEW.session_id); END;
CREATE INDEX IF NOT EXISTS gateway_crm_lookup ON gateway_sessions(binding_id,json_extract(data,'$.bindingVersion'),json_extract(data,'$.externalUserId'));
`

type crmGrant struct {
	KeyID           string            `json:"keyId"`
	SecretFile      string            `json:"secretFile"`
	BindingID       string            `json:"bindingId"`
	BindingVersion  int64             `json:"bindingVersion"`
	StaffUserID     string            `json:"staffUserId"`
	CallbackOrigin  string            `json:"callbackOrigin"`
	CallbackURL     string            `json:"callbackUrl"`
	ContactMappings map[string]string `json:"contactMappings,omitempty"`
	secret          string
}

func (g crmGrant) scope() string {
	return encode([]any{g.KeyID, g.BindingID, g.BindingVersion, g.StaffUserID})
}

type crmRequest struct {
	V              int    `json:"v"`
	Op             string `json:"op"`
	StaffUserID    string `json:"staffUserId,omitempty"`
	ExternalUserID string `json:"externalUserId,omitempty"`
	Actor          string `json:"actor,omitempty"`
	RequestID      string `json:"requestId,omitempty"`
	SessionID      string `json:"sessionId,omitempty"`
	Version        int64  `json:"version,omitempty"`
	BindingVersion int64  `json:"bindingVersion,omitempty"`
	CallbackURL    string `json:"callbackUrl,omitempty"`
}
type crmState struct {
	Mode           string `json:"mode"`
	Version        int64  `json:"version"`
	BindingVersion int64  `json:"bindingVersion"`
	SessionID      string `json:"sessionId"`
}

func stateOf(g GatewaySession) crmState { return crmState{g.Mode, g.Version, g.BindingVersion, g.ID} }

type crmError string

func (e crmError) Error() string { return string(e) }
func crmFailure(c *gin.Context, e error) {
	code := "unavailable"
	status := 503
	var typed crmError
	if errors.As(e, &typed) {
		code = string(typed)
		status = map[string]int{"invalid_request": 400, "invalid_signature": 401, "forbidden": 403, "not_bound": 404, "stale_version": 409, "ambiguous": 409, "request_conflict": 409}[code]
	}
	c.JSON(status, gin.H{"error": code})
}
func trustedCRM(origin, target string) bool {
	o, e := url.Parse(origin)
	if e != nil || o.Scheme != "https" || o.Host == "" || o.User != nil || o.String() != "https://"+o.Host {
		return false
	}
	t, e := url.Parse(target)
	return e == nil && t.Scheme == "https" && t.Host == o.Host && t.User == nil && t.Fragment == "" && t.RawQuery == "" && t.Path != "" && !strings.ContainsAny(target, "\\\r\n")
}
func loadCRMGrants() (map[string]crmGrant, error) {
	out := map[string]crmGrant{}
	path := os.Getenv("WORKBENCH_AR_GRANTS_FILE")
	if path == "" {
		return out, nil
	}
	st, e := os.Lstat(path)
	if !filepath.IsAbs(path) || e != nil || !st.Mode().IsRegular() || st.Mode().Perm()&077 != 0 || st.Size() > 1<<20 {
		return nil, errors.New("invalid CRM host grant file")
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	var gs []crmGrant
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&gs) != nil || d.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid CRM grants")
	}
	bindings := map[string]bool{}
	secrets := map[string]bool{}
	for _, g := range gs {
		if !toolIdentifier.MatchString(g.KeyID) || g.StaffUserID == "" || g.BindingID == "" || g.BindingVersion < 1 || !trustedCRM(g.CallbackOrigin, g.CallbackURL) || !filepath.IsAbs(g.SecretFile) || bindings[g.BindingID] || out[g.KeyID].KeyID != "" {
			return nil, errors.New("invalid or duplicate CRM grant")
		}
		g.secret, e = gw.ReadSecret("", g.SecretFile)
		if e != nil || len(g.secret) < 16 || secrets[g.secret] {
			return nil, errors.New("invalid CRM signing secret")
		}
		secrets[g.secret] = true
		for k, v := range g.ContactMappings {
			if k == "" || v == "" {
				return nil, errors.New("invalid trusted identity mapping")
			}
		}
		bindings[g.BindingID] = true
		out[g.KeyID] = g
	}
	return out, nil
}
func crmMAC(g crmGrant, dir, t string, b []byte) string {
	h := hmac.New(sha256.New, []byte(g.secret))
	fmt.Fprintf(h, "ar1\n%s\n%s\n%s\n", dir, g.KeyID, t)
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}
func crmValidTime(t string, at time.Time) bool {
	n, e := strconv.ParseInt(t, 10, 64)
	return e == nil && strconv.FormatInt(n, 10) == t && n >= at.Unix()-300 && n <= at.Unix()+300
}
func isCRM(r *http.Request) bool {
	for _, name := range []string{"X-AR-Key", "X-AR-Time", "X-AR-Sig"} {
		if len(r.Header.Values(name)) != 0 {
			return true
		}
	}
	return false
}
func (a *API) crmHook(c *gin.Context) {
	r := c.Request
	key, tm, sig := r.Header.Get("X-AR-Key"), r.Header.Get("X-AR-Time"), r.Header.Get("X-AR-Sig")
	g, ok := a.Store.crmGrants[key]
	if !ok || len(r.Header.Values("X-Gateway-Signature")) != 0 || len(r.Header.Values("X-Gateway-Event-Id")) != 0 || len(r.Header.Values("X-AR-Key")) != 1 || len(r.Header.Values("X-AR-Time")) != 1 || len(r.Header.Values("X-AR-Sig")) != 1 || !crmValidTime(tm, time.Now()) {
		crmFailure(c, crmError("invalid_signature"))
		return
	}
	typ, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || typ != "application/json" {
		crmFailure(c, crmError("invalid_request"))
		return
	}
	raw, e := io.ReadAll(http.MaxBytesReader(c.Writer, r.Body, 16<<10))
	if e != nil {
		crmFailure(c, crmError("invalid_request"))
		return
	}
	if !hmac.Equal([]byte(sig), []byte(crmMAC(g, "crm-to-wb", tm, raw))) {
		crmFailure(c, crmError("invalid_signature"))
		return
	}
	var q crmRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&q) != nil || dec.Decode(new(any)) != io.EOF || q.V != 1 {
		crmFailure(c, crmError("invalid_request"))
		return
	}
	if q.Op != "status" && q.Op != "resume" && q.Op != "configure" {
		crmFailure(c, crmError("invalid_request"))
		return
	}
	if !crmFields(raw, q.Op) {
		crmFailure(c, crmError("invalid_request"))
		return
	}
	if q.Op != "status" {
		id, e := uuid.Parse(q.RequestID)
		if e != nil || id.String() != q.RequestID || id == uuid.Nil {
			crmFailure(c, crmError("invalid_request"))
			return
		}
	}
	if q.Op != "configure" && (q.Actor == "" || len(q.Actor) > 256 || q.ExternalUserID == "" || len(q.ExternalUserID) > 256) {
		crmFailure(c, crmError("invalid_request"))
		return
	}
	if q.Op != "configure" && q.StaffUserID != g.StaffUserID {
		crmFailure(c, crmError("forbidden"))
		return
	}
	if q.Op == "resume" && (q.SessionID == "" || q.Version <= 0 || q.BindingVersion <= 0) {
		crmFailure(c, crmError("invalid_request"))
		return
	}
	s := a.Store
	connection := c.Param("id")
	// Keep a slow earlier probe from overwriting a later completed pairing.
	var observedRoute string
	var out json.RawMessage
	transact := func(probed bool) error {
		return s.Transaction(r.Context(), func(tx *sql.Tx) error {
			b, e := activeCRMGrant(tx, g, connection)
			if e != nil {
				return e
			}
			if q.RequestID != "" {
				var h, res string
				e = tx.QueryRow("SELECT hash,result FROM gateway_crm_receipts WHERE grant=? AND request=?", g.scope(), q.RequestID).Scan(&h, &res)
				if e == nil {
					if h != digest(raw) {
						return crmError("request_conflict")
					}
					out = json.RawMessage(res)
					return nil
				}
				if !errors.Is(e, sql.ErrNoRows) {
					return e
				}
			}
			var result any
			var sess string
			if q.Op == "configure" {
				if !trustedCRM(g.CallbackOrigin, q.CallbackURL) {
					return crmError("forbidden")
				}
				var current string
				re := tx.QueryRow("SELECT url FROM gateway_crm_routes WHERE grant=?", g.scope()).Scan(&current)
				if re != nil && !errors.Is(re, sql.ErrNoRows) {
					return re
				}
				if !probed {
					observedRoute = current
					return errCRMProbe
				}
				if observedRoute != current {
					return crmError("request_conflict")
				}
				if _, e = tx.Exec("INSERT INTO gateway_crm_routes VALUES(?,?) ON CONFLICT DO UPDATE SET url=excluded.url", g.scope(), q.CallbackURL); e != nil {
					return e
				}
				result = map[string]any{"ok": true, "callbackUrl": q.CallbackURL}
			} else {
				gs, e := resolveCRM(tx, g, b, q.ExternalUserID)
				if e != nil {
					return e
				}
				sess = gs.ID
				if q.Op == "resume" {
					if q.SessionID != gs.ID || gs.Mode != "human" || q.Version != gs.Version || q.BindingVersion != gs.BindingVersion {
						return crmError("stale_version")
					}
					if e = s.changeGatewayModeTx(tx, &gs, "auto", "CRM 客服恢复 AI"); e != nil {
						return e
					}
				}
				result = stateOf(gs)
			}
			out = json.RawMessage(encode(result))
			if q.RequestID != "" {
				_, e = tx.Exec("INSERT INTO gateway_crm_receipts VALUES(?,?,?,?,?,?,?)", g.scope(), q.RequestID, digest(raw), q.Actor, sess, string(out), now())
			}
			return e
		})
	}
	e = transact(false)
	if errors.Is(e, errCRMProbe) {
		nonce := newID()
		var answer struct {
			Nonce string `json:"nonce"`
		}
		e = s.crmPost(r.Context(), g, q.CallbackURL, map[string]any{"v": 1, "op": "probe", "nonce": nonce}, &answer)
		if e == nil && answer.Nonce == nonce {
			e = transact(true)
		} else {
			e = errors.New("probe failed")
		}
	}
	if e != nil {
		crmFailure(c, e)
		return
	}
	c.Data(200, "application/json", out)
}

// Reject duplicate, case-folded or extraneous fields before interpreting the command.
func crmFields(raw []byte, op string) bool {
	names := []string{"v", "op"}
	if op == "configure" {
		names = append(names, "callbackUrl", "requestId")
	} else {
		names = append(names, "staffUserId", "externalUserId", "actor")
		if op == "resume" {
			names = append(names, "sessionId", "version", "bindingVersion", "requestId")
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	tok, e := d.Token()
	if e != nil || tok != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		k, e := d.Token()
		key, ok := k.(string)
		if e != nil || !ok || seen[key] || !contains(names, key) {
			return false
		}
		seen[key] = true
		var v json.RawMessage
		if d.Decode(&v) != nil {
			return false
		}
	}
	return len(seen) == len(names)
}

var errCRMProbe = errors.New("probe required")

func activeCRMGrant(tx *sql.Tx, g crmGrant, connection string) (GatewayBinding, error) {
	b, e := gatewayBindingTx(tx, g.BindingID)
	if errors.Is(e, sql.ErrNoRows) {
		return b, crmError("not_bound")
	}
	if e != nil {
		return b, e
	}
	var active int
	e = tx.QueryRow("SELECT count(*) FROM connections c JOIN projects p ON p.id=? WHERE c.id=? AND c.kind=? AND json_extract(c.data,'$.enabled')=1 AND p.archived=0", b.ProjectID, b.ConnectionID, gatewaySource).Scan(&active)
	if e != nil {
		return b, e
	}
	if b.ConnectionID != connection || b.Version != g.BindingVersion || !b.Enabled || active != 1 {
		return b, crmError("not_bound")
	}
	return b, nil
}
func resolveCRM(tx *sql.Tx, grant crmGrant, b GatewayBinding, external string) (GatewaySession, error) {
	// Legacy overrides can only fill missing identity. Signed observations conflicting with a
	// pinned migration record make the relationship unresolvable, never an automatic remap.
	rows, e := tx.Query(`SELECT gs.data FROM gateway_sessions gs JOIN sessions s ON s.id=gs.id WHERE gs.binding_id=? AND json_extract(gs.data,'$.bindingVersion')=? AND s.project_id=? AND coalesce(json_extract(s.data,'$.recycledAt'),0)=0 AND (json_extract(gs.data,'$.externalUserId')=? OR json_extract(gs.data,'$.contactId') IN (SELECT key FROM json_each(?) WHERE value=?))`, b.ID, b.Version, b.ProjectID, external, encode(grant.ContactMappings), external)
	if e != nil {
		return GatewaySession{}, e
	}
	defer rows.Close()
	var result GatewaySession
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			return result, e
		}
		var g GatewaySession
		if e = json.Unmarshal([]byte(raw), &g); e != nil {
			return result, e
		}
		if result.ID != "" || g.IdentityConflict || grant.ContactMappings[g.ContactID] != "" && g.ExternalUserID != "" && grant.ContactMappings[g.ContactID] != g.ExternalUserID {
			return result, crmError("ambiguous")
		}
		result = g
	}
	if e = rows.Err(); e != nil {
		return result, e
	}
	if result.ID == "" {
		return result, crmError("not_bound")
	}
	return result, nil
}
func (s *Store) crmPost(ctx context.Context, g crmGrant, target string, v, out any) error {
	if !trustedCRM(g.CallbackOrigin, target) {
		return errors.New("invalid CRM target")
	}
	raw := []byte(encode(v))
	r, e := http.NewRequestWithContext(ctx, "POST", target, bytes.NewReader(raw))
	if e != nil {
		return e
	}
	tm := strconv.FormatInt(time.Now().Unix(), 10)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-AR-Time", tm)
	r.Header.Set("X-AR-Key", g.KeyID)
	r.Header.Set("X-AR-Sig", crmMAC(g, "wb-to-crm", tm, raw))
	cl := http.Client{Timeout: 4 * time.Second, Transport: s.crmTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, e := cl.Do(r)
	if e != nil {
		return errors.New("CRM request unavailable")
	}
	defer res.Body.Close()
	body, e := io.ReadAll(io.LimitReader(res.Body, (16<<10)+1))
	if e != nil || len(body) > 16<<10 || res.StatusCode != 200 || json.Unmarshal(body, out) != nil {
		return errors.New("CRM response not confirmed")
	}
	return nil
}
