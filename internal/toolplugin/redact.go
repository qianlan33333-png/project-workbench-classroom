package toolplugin

import (
	"encoding/json"
	"net/url"
	"strings"
)

func (s *Server) redact(v any) any {
	switch x := v.(type) {
	case json.RawMessage:
		var out any
		if json.Unmarshal(x, &out) == nil {
			return s.redact(out)
		}
		return nil
	case map[string]any:
		out := map[string]any{}
		for k, value := range x {
			key := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(k))
			if contains([]string{"password", "apikey", "accesstoken", "refreshtoken", "authorization", "privatekey", "clientsecret", "appsecret", "secret"}, key) {
				out[k] = "[REDACTED_SECRET]"
			} else {
				out[k] = s.redact(value)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, value := range x {
			out[i] = s.redact(value)
		}
		return out
	case string:
		secrets := []string{s.Config.SourcePassword, s.Config.Model.Key}
		for _, g := range s.Config.Grants {
			secrets = append(secrets, g.Token)
		}
		for _, secret := range secrets {
			if len(secret) >= 8 {
				x = strings.ReplaceAll(x, secret, "[REDACTED_SECRET]")
			}
		}
		if u, e := url.Parse(x); e == nil && u.Host != "" && u.Scheme != "" {
			changed := false
			if u.User != nil {
				u.User = url.User("[REDACTED_SECRET]")
				changed = true
			}
			q := u.Query()
			for k := range q {
				lower := strings.ToLower(k)
				if strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "api_key") {
					q.Set(k, "[REDACTED_SECRET]")
					changed = true
				}
			}
			if changed {
				u.RawQuery = q.Encode()
				x = u.String()
			}
		}
		return x
	default:
		return v
	}
}
