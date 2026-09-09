package applog

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

const redacted = "[REDACTED]"

var (
	secretKeys = []string{
		"password", "passwd", "secret", "client_secret", "access_token", "refresh_token", "id_token", "token",
		"api_key", "apikey", "access_key", "authorization", "cookie", "devicecode", "loginuuid", "signature", "sign",
	}
	logURL      = regexp.MustCompile(`https?://[^\s<>"']+`)
	authHeader  = regexp.MustCompile(`(?im)(\b(?:authorization|proxy-authorization|cookie|set-cookie)\s*:\s*)[^\r\n]+`)
	authValue   = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[a-z0-9._~+/=-]+`)
	secretValue = regexp.MustCompile(`(?i)(["']?(?:` + strings.Join(secretKeys, "|") + `)["']?\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;&}]+)`)
)

func sensitiveKey(key string) bool {
	key = strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(key))
	return strings.Contains(key, "token") || strings.Contains(key, "password") || strings.Contains(key, "secret") ||
		strings.Contains(key, "credential") || strings.Contains(key, "cookie") ||
		strings.Contains(key, "signature") || strings.Contains(key, "accesskey") ||
		key == "passwd" || key == "authorization" || key == "apikey" || key == "accesskey" || key == "key" ||
		key == "authkey" || key == "auth" || key == "sessionid" || key == "session" ||
		key == "sign" || key == "signature" || key == "devicecode" || key == "loginuuid" || key == "code"
}

func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "[invalid URL]"
	}
	u.User = nil
	u.Fragment = ""
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		u.RawQuery = "redacted=invalid-query"
		return u.String()
	}
	changed := false
	for key := range values {
		if sensitiveKey(key) {
			values.Set(key, redacted)
			changed = true
		}
	}
	if changed {
		u.RawQuery = values.Encode()
	}
	return u.String()
}

func Redact(text string) string {
	if text == "" {
		return text
	}
	var value any
	if json.Valid([]byte(text)) {
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		if decoder.Decode(&value) == nil {
			encoded, err := json.Marshal(redactJSON(value))
			if err == nil {
				return string(encoded)
			}
		}
	}
	text = logURL.ReplaceAllStringFunc(text, func(raw string) string {
		urlPart := strings.TrimRight(raw, "),;.")
		return RedactURL(urlPart) + strings.TrimPrefix(raw, urlPart)
	})
	if !mayContainLogSecret(text) {
		return text
	}
	text = authHeader.ReplaceAllString(text, "${1}"+redacted)
	text = authValue.ReplaceAllString(text, "${1} "+redacted)
	return secretValue.ReplaceAllString(text, "${1}"+redacted)
}

func mayContainLogSecret(text string) bool {
	// Match regexp's ASCII-key case folding without scanning ordinary text with regexps.
	lower := strings.Map(func(r rune) rune {
		if r > unicode.MaxASCII {
			for folded := unicode.SimpleFold(r); folded != r; folded = unicode.SimpleFold(folded) {
				if folded <= unicode.MaxASCII {
					return unicode.ToLower(folded)
				}
			}
		}
		return unicode.ToLower(r)
	}, text)
	if strings.Contains(lower, "bearer") || strings.Contains(lower, "basic") {
		return true
	}
	for _, key := range secretKeys {
		if strings.Contains(lower, key) {
			return true
		}
	}
	return false
}

func redactJSON(value any) any {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if sensitiveKey(key) {
				// Numeric provider error codes are diagnostic, not OAuth codes.
				if strings.EqualFold(key, "code") {
					if _, ok := child.(json.Number); ok {
						continue
					}
				}
				value[key] = redacted
			} else {
				value[key] = redactJSON(child)
			}
		}
	case []any:
		for i, child := range value {
			value[i] = redactJSON(child)
		}
	case string:
		return Redact(value)
	}
	return value
}

func redactEntry(entry *Entry) {
	entry.Message = Redact(entry.Message)
	entry.Error = Redact(entry.Error)
	entry.Stack = Redact(entry.Stack)
	if entry.Path != "" {
		entry.Path = RedactURL(entry.Path)
	}
	entry.Fields.Component = Redact(entry.Component)
	entry.Fields.RequestID = Redact(entry.RequestID)
	entry.Fields.TaskID = Redact(entry.TaskID)
	entry.Fields.DriveID = Redact(entry.DriveID)
	entry.Fields.VideoID = Redact(entry.VideoID)
	entry.Fields.FileID = Redact(entry.FileID)
	entry.Fields.Stage = Redact(entry.Stage)
}
