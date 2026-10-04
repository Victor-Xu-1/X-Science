package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"strings"
)

// Clone JSON-compatible inputs before redacting keys, so the actual call and
// its private approval authority keep their original bytes/number precision.
func kernelMCPApprovalPreview(resolution kernelMCPResolution, input map[string]any) (string, error) {
	raw, err := json.Marshal(map[string]any{
		"server": resolution.connector.Name, "method": resolution.tool.ToolName, "arguments": input,
	})
	if err != nil {
		return "", err
	}
	var cloned any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&cloned); err != nil {
		return "", err
	}
	encoded, err := json.MarshalIndent(redactKernelMCPPreviewValue(cloned, 0), "", "  ")
	if err != nil {
		return "", err
	}
	preview := []rune(string(encoded))
	if len(preview) > 4096 {
		preview = append(preview[:4096], []rune("\n[Preview shortened; actual arguments are unchanged.]")...)
	}
	return string(preview), nil
}

func redactKernelMCPPreviewValue(value any, depth int) any {
	if depth > 24 {
		return "[Nested content omitted]"
	}
	switch typed := value.(type) {
	case map[string]any:
		for _, label := range []string{"name", "key"} {
			if name, ok := typed[label].(string); ok && kernelMCPPreviewSensitiveKey(name) {
				if _, found := typed["value"]; found {
					typed["value"] = "[REDACTED]"
				}
			}
		}
		for key, child := range typed {
			if kernelMCPPreviewSensitiveKey(key) {
				typed[key] = "[REDACTED]"
			} else {
				typed[key] = redactKernelMCPPreviewValue(child, depth+1)
			}
		}
	case []any:
		for index, child := range typed {
			typed[index] = redactKernelMCPPreviewValue(child, depth+1)
		}
	case string:
		// Some tools accept JSON bodies as strings. Apply the same key policy
		// to those values, rather than relying on a quoted-key text regex.
		trimmed := strings.TrimSpace(typed)
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			var nested any
			decoder := json.NewDecoder(strings.NewReader(trimmed))
			decoder.UseNumber()
			var trailing any
			if decoder.Decode(&nested) == nil && decoder.Decode(&trailing) == io.EOF {
				if encoded, err := json.Marshal(redactKernelMCPPreviewValue(nested, depth+1)); err == nil {
					return string(encoded)
				}
			}
			return "[JSON-like content omitted from preview]"
		}
		if parsed, err := url.Parse(typed); err == nil && parsed.IsAbs() {
			changed := parsed.User != nil
			if parsed.User != nil {
				parsed.User = nil
			}
			query := parsed.Query()
			for key, values := range query {
				if kernelMCPPreviewSensitiveKey(key) {
					query.Set(key, "REDACTED")
					changed = true
				} else {
					for index, value := range values {
						clean := redactFeedbackString(value)
						changed = changed || clean != value
						values[index] = clean
					}
				}
			}
			if changed {
				parsed.RawQuery = query.Encode()
				return parsed.String()
			}
		}
		return redactFeedbackString(typed)
	}
	return value
}

func kernelMCPPreviewSensitiveKey(key string) bool {
	key = strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "", ".", "", ":", "").Replace(key))
	switch key {
	case "auth", "authentication", "authorization", "proxyauthorization", "cookie", "setcookie", "credentials", "credential", "passwd", "pwd", "passphrase":
		return true
	}
	return strings.Contains(key, "password") || strings.Contains(key, "secret") || strings.Contains(key, "apikey") ||
		strings.HasSuffix(key, "token") || strings.HasSuffix(key, "privatekey") || strings.HasSuffix(key, "accesskey") || strings.HasSuffix(key, "sessionkey")
}
