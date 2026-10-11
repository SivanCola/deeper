package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

func openCodeGoDigest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// Identity follows the credential reference and user transport settings, never
// a resolved secret or the mutable display name/model list/protocol default.
func openCodeGoIdentity(p ProviderEntry) string {
	b, _ := json.Marshal(struct {
		Key           string
		Headers       map[string]string
		Body          map[string]any
		Auth, NoProxy bool
	}{strings.TrimSpace(p.APIKeyEnv), normalizedProviderHeaders(p.Headers), p.ExtraBody, p.AuthHeader, p.NoProxy})
	return openCodeGoDigest(b)
}
