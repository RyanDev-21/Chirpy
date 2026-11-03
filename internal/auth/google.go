package auth

import (
	"context"
	"errors"

	"google.golang.org/api/idtoken"
)

// googleVerifier validates Google ID tokens against Google's public keys.
// audiences are the OAuth client IDs the token may be issued to: for
// Android "Sign in with Google" that is the *Web* client ID the app passes
// as serverClientId, not the Android client ID.
type googleVerifier struct {
	audiences []string
}

func NewGoogleVerifier(audiences []string) GoogleVerifier {
	if len(audiences) == 0 {
		return nil
	}
	return googleVerifier{audiences: audiences}
}

func (g googleVerifier) Verify(ctx context.Context, token string) (GoogleClaims, error) {
	var lastErr error = errors.New("no audience configured")
	for _, aud := range g.audiences {
		p, err := idtoken.Validate(ctx, token, aud)
		if err != nil {
			lastErr = err
			continue
		}
		email, _ := p.Claims["email"].(string)
		verified, _ := p.Claims["email_verified"].(bool)
		return GoogleClaims{Subject: p.Subject, Email: email, EmailVerified: verified}, nil
	}
	return GoogleClaims{}, lastErr
}
