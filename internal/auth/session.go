package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	sessionSecretKey = []byte("default-dev-secret-key-change-me-in-production")
	CookieName       = "rms_session"
)

func InitSession() {
	secret := os.Getenv("SESSION_SECRET")
	if secret != "" {
		sessionSecretKey = []byte(secret)
	}
}

// GenerateSessionToken creates a signed session token containing the userID and expiry time
func GenerateSessionToken(userID string, duration time.Duration) string {
	expiry := time.Now().Add(duration).Unix()
	payload := fmt.Sprintf("%s:%d", userID, expiry)

	mac := hmac.New(sha256.New, sessionSecretKey)
	mac.Write([]byte(payload))
	signature := mac.Sum(nil)

	token := fmt.Sprintf("%s:%s", payload, base64.RawURLEncoding.EncodeToString(signature))
	return base64.RawURLEncoding.EncodeToString([]byte(token))
}

// VerifySessionToken parses a token, verifies its HMAC signature, and returns the userID if valid
func VerifySessionToken(tokenStr string) (string, error) {
	decodedBytes, err := base64.RawURLEncoding.DecodeString(tokenStr)
	if err != nil {
		return "", errors.New("invalid session encoding")
	}

	parts := strings.Split(string(decodedBytes), ":")
	if len(parts) != 3 {
		return "", errors.New("invalid session format")
	}

	userID := parts[0]
	expiryStr := parts[1]
	sigBase64 := parts[2]

	payload := fmt.Sprintf("%s:%s", userID, expiryStr)

	// Verify HMAC signature
	mac := hmac.New(sha256.New, sessionSecretKey)
	mac.Write([]byte(payload))
	expectedSig := mac.Sum(nil)

	sigBytes, err := base64.RawURLEncoding.DecodeString(sigBase64)
	if err != nil || !hmac.Equal(sigBytes, expectedSig) {
		return "", errors.New("invalid session signature")
	}

	// Verify expiration
	expiry, err := strconv.ParseInt(expiryStr, 10, 64)
	if err != nil {
		return "", errors.New("invalid session expiry")
	}

	if time.Now().Unix() > expiry {
		return "", errors.New("session expired")
	}

	return userID, nil
}

// SetSessionCookie sets the session cookie in the HTTP response
func SetSessionCookie(w http.ResponseWriter, userID string) {
	// Standard 24h sessions
	token := GenerateSessionToken(userID, 24*time.Hour)
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   false, // Set to true in production if TLS is terminated at the app (Caddy terminates TLS, Go app runs on HTTP locally)
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400, // 24 hours
	})
}

// ClearSessionCookie clears the session cookie in the HTTP response
func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}
