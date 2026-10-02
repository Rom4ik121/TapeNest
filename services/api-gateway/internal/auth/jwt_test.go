package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/api-gateway/internal/domain"
)

const testSecret = "0123456789abcdef0123456789abcdef-test"

func TestJWTRoundTrip(t *testing.T) {
	j := NewJWTIssuer(testSecret, 15*time.Minute, fixedNow)
	p := Principal{UserID: uuid.New(), TelegramID: 42, Role: domain.RoleAdmin, SessionID: "fam-1"}
	tok, exp, err := j.Issue(p)
	if err != nil {
		t.Fatal(err)
	}
	if !exp.Equal(testNow.Add(15 * time.Minute)) {
		t.Fatalf("exp %v", exp)
	}
	got, err := j.Parse(tok)
	if err != nil {
		t.Fatal(err)
	}
	if got != p {
		t.Fatalf("got %+v want %+v", got, p)
	}
	if j.TTL() != 15*time.Minute {
		t.Fatal("ttl")
	}
}

func TestJWTRejects(t *testing.T) {
	j := NewJWTIssuer(testSecret, 15*time.Minute, fixedNow)
	p := Principal{UserID: uuid.New(), TelegramID: 42, Role: domain.RoleUser, SessionID: "fam"}
	tok, _, _ := j.Issue(p)

	later := NewJWTIssuer(testSecret, 15*time.Minute, func() time.Time { return testNow.Add(16 * time.Minute) })
	if _, err := later.Parse(tok); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired: %v", err)
	}
	if _, err := NewJWTIssuer("another-secret-another-secret-12345", time.Minute, fixedNow).Parse(tok); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("wrong secret: %v", err)
	}
	sign := func(m jwt.SigningMethod, key any, c jwt.Claims) string {
		s, err := jwt.NewWithClaims(m, c).SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	base := func() Claims {
		return Claims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer: issuer, Subject: p.UserID.String(), Audience: jwt.ClaimStrings{audience},
				IssuedAt: jwt.NewNumericDate(testNow), ExpiresAt: jwt.NewNumericDate(testNow.Add(time.Minute)),
			},
			TelegramID: 42, SessionID: "fam",
		}
	}
	wrongIss, wrongAud, badSub, noSid, noExp := base(), base(), base(), base(), base()
	wrongIss.Issuer = "evil"
	wrongAud.Audience = jwt.ClaimStrings{"other"}
	badSub.Subject = "42"
	noSid.SessionID = ""
	noExp.ExpiresAt = nil
	for name, tok := range map[string]string{
		"alg none":     sign(jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, base()),
		"HS512":        sign(jwt.SigningMethodHS512, []byte(testSecret), base()),
		"wrong issuer": sign(jwt.SigningMethodHS256, []byte(testSecret), wrongIss),
		"wrong aud":    sign(jwt.SigningMethodHS256, []byte(testSecret), wrongAud),
		"bad subject":  sign(jwt.SigningMethodHS256, []byte(testSecret), badSub),
		"no sid":       sign(jwt.SigningMethodHS256, []byte(testSecret), noSid),
		"no exp":       sign(jwt.SigningMethodHS256, []byte(testSecret), noExp),
		"garbage":      "not.a.jwt",
	} {
		if _, err := j.Parse(tok); !errors.Is(err, ErrTokenInvalid) {
			t.Errorf("%s: want ErrTokenInvalid, got %v", name, err)
		}
	}
}
