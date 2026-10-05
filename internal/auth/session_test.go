package auth

import (
	"net/http/httptest"
	"testing"
)

func TestPlatformSessionCookieIsolatedFromCustomerCookie(t *testing.T) {
	platformResponse := httptest.NewRecorder()
	SetPlatformSessionCookie(platformResponse, "platform-user")
	platformCookies := platformResponse.Result().Cookies()
	if len(platformCookies) != 1 {
		t.Fatalf("expected one platform cookie, got %d", len(platformCookies))
	}
	platformCookie := platformCookies[0]
	if platformCookie.Name != PlatformCookieName || platformCookie.Path != "/platform" || !platformCookie.HttpOnly {
		t.Fatalf("unexpected platform cookie attributes: %#v", platformCookie)
	}
	if userID, err := VerifySessionToken(platformCookie.Value); err != nil || userID != "platform-user" {
		t.Fatalf("platform cookie token verifies as %q, %v", userID, err)
	}

	customerResponse := httptest.NewRecorder()
	SetSessionCookie(customerResponse, "customer-user")
	customerCookie := customerResponse.Result().Cookies()[0]
	if customerCookie.Name != CookieName || customerCookie.Path != "/" || customerCookie.Name == platformCookie.Name {
		t.Fatalf("unexpected customer cookie attributes: %#v", customerCookie)
	}
}
