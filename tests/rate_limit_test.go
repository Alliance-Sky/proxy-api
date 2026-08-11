package tests

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httprate"
)

func setupTestRouterWithRateLimit(adminToken string) chi.Router {
	r := chi.NewRouter()

	rateLimiter := httprate.Limit(
		2, // Very low limit for testing (2 requests per minute)
		1*time.Minute,
		httprate.WithKeyFuncs(httprate.KeyByIP),
		httprate.WithLimitHandler(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"statusCode":429,"error":"Too Many Requests"}`, http.StatusTooManyRequests)
		}),
	)

	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			isLocalhost := strings.HasPrefix(r.RemoteAddr, "127.0.0.1:") || strings.HasPrefix(r.RemoteAddr, "[::1]:")
			isDirectConnection := r.Header.Get("X-Forwarded-For") == "" && r.Header.Get("X-Real-IP") == ""

			if (isLocalhost && isDirectConnection) || (adminToken != "" && r.Header.Get("X-Admin-Token") == adminToken) {
				next.ServeHTTP(w, r)
				return
			}
			rateLimiter(next).ServeHTTP(w, r)
		})
	})

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	return r
}

func TestRateLimiter(t *testing.T) {
	r := setupTestRouterWithRateLimit("secret-token")

	// Helper to make requests
	makeReq := func(remoteAddr, token string) int {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = remoteAddr
		if token != "" {
			req.Header.Set("X-Admin-Token", token)
		}
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		return rr.Code
	}

	// 1. Test regular external IP gets blocked after 2 requests
	if status := makeReq("192.168.1.1:1234", ""); status != http.StatusOK {
		t.Errorf("Expected 200, got %v", status)
	}
	if status := makeReq("192.168.1.1:1234", ""); status != http.StatusOK {
		t.Errorf("Expected 200, got %v", status)
	}
	if status := makeReq("192.168.1.1:1234", ""); status != http.StatusTooManyRequests {
		t.Errorf("Expected 429 Too Many Requests, got %v", status)
	}

	// 2. Test localhost bypasses rate limit
	for i := 0; i < 5; i++ {
		if status := makeReq("127.0.0.1:5000", ""); status != http.StatusOK {
			t.Errorf("Localhost should bypass limit, but got %v", status)
		}
	}

	// 3. Test external IP with correct admin token bypasses rate limit
	for i := 0; i < 5; i++ {
		if status := makeReq("10.0.0.1:4000", "secret-token"); status != http.StatusOK {
			t.Errorf("Admin token should bypass limit, but got %v", status)
		}
	}

	// 4. Test external IP with INCORRECT admin token gets rate limited
	if status := makeReq("10.0.0.2:4000", "wrong-token"); status != http.StatusOK {
		t.Errorf("Expected 200, got %v", status)
	}
	if status := makeReq("10.0.0.2:4000", "wrong-token"); status != http.StatusOK {
		t.Errorf("Expected 200, got %v", status)
	}
	if status := makeReq("10.0.0.2:4000", "wrong-token"); status != http.StatusTooManyRequests {
		t.Errorf("Wrong token should be rate limited, got %v", status)
	}
}
