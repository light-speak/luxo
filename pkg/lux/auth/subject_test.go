package auth

import (
	"strings"
	"testing"
	"time"
)

func TestSubjectTokenLifecycle(t *testing.T) {
	cfg := testConfig()
	for _, subject := range []string{"User", "Admin", "租户/用户", "quoted\"subject"} {
		t.Run(subject, func(t *testing.T) {
			token, err := SignForSubject(cfg, subject, testClaims(9007199254740993, ""))
			if err != nil {
				t.Fatal(err)
			}
			claims, err := Verify(cfg, token)
			if err != nil || claims.Subject() != subject || claims.ID() != 9007199254740993 {
				t.Fatalf("claims=%+v err=%v", claims, err)
			}
			cached, err := VerifyCached(cfg, token)
			if err != nil || cached.Subject() != subject {
				t.Fatalf("cache lost subject: %v", err)
			}
			refresh, err := SignRefreshForSubject(cfg, subject, claims.AppendJSON)
			if err != nil {
				t.Fatal(err)
			}
			renewed, err := RefreshForSubject(cfg, subject, refresh)
			if err != nil {
				t.Fatal(err)
			}
			result, err := Verify(cfg, renewed)
			if err != nil || result.Subject() != subject || result.Raw() != claims.Raw() {
				t.Fatalf("renewal changed identity: %v", err)
			}
			if _, err := RefreshForSubject(cfg, subject+"Other", refresh); err == nil {
				t.Fatal("cross-subject refresh accepted")
			}
			if _, err := RefreshForSubject(cfg, subject, token); err == nil {
				t.Fatal("access used as refresh")
			}
		})
	}
}

func TestSubjectSigningRejectsInvalidInput(t *testing.T) {
	cfg := testConfig()
	for _, subject := range []string{"", string([]byte{0xff})} {
		if _, err := SignForSubject(cfg, subject, testClaims(1, "")); err == nil {
			t.Fatal("invalid subject accepted")
		}
		if _, err := SignRefreshForSubject(cfg, subject, testClaims(1, "")); err == nil {
			t.Fatal("invalid refresh subject accepted")
		}
	}
	if _, err := SignForSubject(nil, "User", testClaims(1, "")); err == nil {
		t.Fatal("nil config accepted")
	}
	if _, err := SignRefreshForSubject(nil, "User", testClaims(1, "")); err == nil {
		t.Fatal("nil refresh config accepted")
	}
	cfg.RefreshEnabled = false
	if _, err := SignRefreshForSubject(cfg, "User", testClaims(1, "")); err == nil {
		t.Fatal("disabled refresh accepted")
	}
	cfg.RefreshEnabled = true
	expired, err := SignForSubjectWithExpiry(cfg, "User", testClaims(1, ""), -time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(cfg, expired); err == nil {
		t.Fatal("expired access accepted")
	}
	unbound, err := SignRefresh(cfg, testClaims(1, ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RefreshForSubject(cfg, "User", unbound); err == nil || !strings.Contains(err.Error(), "subject") {
		t.Fatalf("unbound refresh: %v", err)
	}
	if _, err := RefreshForSubject(cfg, "", unbound); err == nil {
		t.Fatal("empty subject renewal accepted")
	}
}

func TestSubjectEnvelopeValidation(t *testing.T) {
	cfg := testConfig()
	for _, payload := range []string{
		`{"sub":"User","data":{"id":42},"exp":4102444800,"token_use":"access"}`,
		`{"data":{"id":42,"sub":"Admin"},"sub":"User","exp":4102444800,"token_use":"access"}`,
	} {
		claims, err := Verify(cfg, signedPurposePayload(cfg, payload))
		if err != nil || claims.Subject() != "User" {
			t.Fatalf("subject lost or replaced: %v", err)
		}
	}
	for _, field := range []string{`"sub":null`, `"sub":42`, `"sub":""`, `"sub":"User","sub":"Admin"`, `"sub":"\uD800"`} {
		payload := `{"data":{"id":42},"exp":4102444800,"token_use":"access",` + field + `}`
		if claims, err := Verify(cfg, signedPurposePayload(cfg, payload)); err == nil || claims.Subject() != "" || claims.Raw() != "" {
			t.Fatalf("accepted malformed subject %s: %v", field, err)
		}
	}
	expired := signedPurposePayload(cfg, `{"data":{"id":42},"sub":"User","exp":1,"token_use":"refresh"}`)
	if _, err := RefreshForSubject(cfg, "User", expired); err == nil {
		t.Fatal("expired refresh renewed")
	}
}

func TestSubjectOwnsDecodedText(t *testing.T) {
	for _, subject := range []string{`"User"`, `"\u7528\u6237"`, `"quoted\"subject"`} {
		payload := []byte(`{"sub":` + subject + `,"data":{"id":42},"exp":4102444800,"token_use":"access"}`)
		claims, _, err := readTokenClaims(payload, accessPurpose)
		if err != nil {
			t.Fatal(err)
		}
		want := claims.Subject()
		for i := range payload {
			payload[i] = 'x'
		}
		for range 16 {
			_, _, err := readTokenClaims([]byte(`{"sub":"Replacement","data":{},"exp":4102444800,"token_use":"access"}`), accessPurpose)
			if err != nil {
				t.Fatal(err)
			}
		}
		if claims.Subject() != want {
			t.Fatalf("subject changed after input mutation and reader reuse: got %q want %q", claims.Subject(), want)
		}
	}
}
