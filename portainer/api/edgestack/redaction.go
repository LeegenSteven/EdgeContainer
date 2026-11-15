package edgestack

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
)

const credentialMarker = "[已隐去凭据]"

var authorizationCredential = regexp.MustCompile(`(?i)(\bauthorization["']?\s*[:=]\s*["']?)(?:bearer|basic)\s+[^\s"',;}\]]+`)
var bearerCredential = regexp.MustCompile(`(?i)\b(?:bearer|basic)[ \t]+[A-Za-z0-9._~+/=-]+`)
var standaloneCredential = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]+|es_ingest_[A-Za-z0-9_-]+|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)\b`)
var assignedCredential = regexp.MustCompile(`(?i)(\b(?:[a-z0-9_]*api[_-]?key|[a-z0-9_]*token|edge[_-]?key|private[_-]?key|authorization|bearer|password|passwd|client[_-]?secret|secret)\b["']?\s*[:=]\s*["']?)[^\s"',;}]+`)

type redactionSecretProvider interface{ RedactionSecrets() ([]string, error) }

func redactText(value string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, credentialMarker)
		}
	}
	value = authorizationCredential.ReplaceAllString(value, "${1}"+credentialMarker)
	value = bearerCredential.ReplaceAllString(value, credentialMarker)
	value = standaloneCredential.ReplaceAllString(value, credentialMarker)
	return assignedCredential.ReplaceAllString(value, "${1}"+credentialMarker)
}
func sensitiveCredentialKey(key string) bool {
	key = strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
	return strings.HasSuffix(key, "apikey") || strings.HasSuffix(key, "token") || strings.HasSuffix(key, "password") || key == "passwd" || strings.HasSuffix(key, "secret") || key == "authorization" || key == "edgekey" || key == "privatekey" || key == "bearer" || key == "basic"
}
func redactJSON(value any, secrets []string) any {
	switch v := value.(type) {
	case string:
		return redactText(v, secrets)
	case []any:
		for i, item := range v {
			v[i] = redactJSON(item, secrets)
		}
		return v
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if sensitiveCredentialKey(key) {
				out[redactText(key, secrets)] = credentialMarker
			} else {
				out[redactText(key, secrets)] = redactJSON(item, secrets)
			}
		}
		return out
	}
	return value
}
func redactSummaryInput(request SummaryRequest, evidence []Evidence, secrets []string) (SummaryRequest, []Evidence) {
	request.Question = redactText(request.Question, secrets)
	snapshot := make([]Evidence, len(evidence))
	for i, event := range evidence {
		event.Text = redactText(event.Text, secrets)
		event.Phase = redactText(event.Phase, secrets)
		if redactText(event.Ref, secrets) != event.Ref || redactText(event.EventID, secrets) != event.EventID {
			hash := sha256.Sum256([]byte(event.EventID))
			event.Ref = "evt:" + hex.EncodeToString(hash[:])
		}
		if len(event.Provenance) > 0 {
			var value any
			if json.Unmarshal(event.Provenance, &value) == nil {
				event.Provenance, _ = json.Marshal(redactJSON(value, secrets))
			} else {
				event.Provenance = nil
			}
		}
		snapshot[i] = event
	}
	return request, snapshot
}
