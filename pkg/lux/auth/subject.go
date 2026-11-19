package auth

import (
	"fmt"
	"time"
)

// SignForSubject creates access credentials for one authentication model.
func SignForSubject(cfg *Config, subject string, data ClaimsWriter) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("JWT config is required")
	}
	return SignForSubjectWithExpiry(cfg, subject, data, cfg.Expires)
}

// SignForSubjectWithExpiry uses the supplied access lifetime without changing its subject.
func SignForSubjectWithExpiry(cfg *Config, subject string, data ClaimsWriter, expires time.Duration) (string, error) {
	if subject == "" {
		return "", fmt.Errorf("JWT subject is required")
	}
	return signWithSubject(cfg, data, expires, accessPurpose, subject)
}

// SignRefreshForSubject creates refresh credentials for one authentication model.
func SignRefreshForSubject(cfg *Config, subject string, data ClaimsWriter) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("JWT config is required")
	}
	if !cfg.RefreshEnabled {
		return "", fmt.Errorf("refresh tokens are not enabled")
	}
	if subject == "" {
		return "", fmt.Errorf("JWT subject is required")
	}
	return signWithSubject(cfg, data, cfg.RefreshExpires, refreshPurpose, subject)
}

// RefreshForSubject preserves the verified identity and rejects another model's token.
func RefreshForSubject(cfg *Config, subject, token string) (string, error) {
	if subject == "" {
		return "", fmt.Errorf("JWT subject is required")
	}
	claims, err := VerifyRefresh(cfg, token)
	if err != nil {
		return "", err
	}
	if claims.Subject() != subject {
		return "", fmt.Errorf("unexpected JWT subject")
	}
	return SignForSubject(cfg, subject, claims.AppendJSON)
}
