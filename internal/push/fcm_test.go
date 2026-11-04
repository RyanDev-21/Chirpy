package push

import (
	"context"
	"strings"
	"testing"
)

// A wrong or partial key must give an error that doesn't echo the key. (A
// well-formed file with a broken private key is caught by Check at startup.)
func TestNewFCMRejectsBadKeysWithoutLeakingThem(t *testing.T) {
	secretish := "PRIVATE-KEY-MATERIAL-SHOULD-NOT-BE-LOGGED"
	for name, raw := range map[string]string{
		"empty":      "",
		"not json":   "hello " + secretish,
		"truncated":  `{"type":"service_account","private_key":"` + secretish,
		"wrong type": `{"type":"external_account","audience":"x","subject_token_type":"y","token_url":"https://example.com","credential_source":{"file":"/etc/passwd"}}`,
	} {
		_, err := NewFCM(context.Background(), []byte(raw))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if strings.Contains(err.Error(), secretish) {
			t.Errorf("%s: error leaks key material: %v", name, err)
		}
	}
}
