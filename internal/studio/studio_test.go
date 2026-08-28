package studio

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleRecipes(t *testing.T) {
	w := httptest.NewRecorder()
	handleRecipes(w, httptest.NewRequest("GET", "http://127.0.0.1/api/recipes", nil))
	if body := w.Body.String(); !strings.Contains(body, `"languages"`) || !strings.Contains(body, "laravel") {
		t.Fatalf("recipes response missing data")
	}
}

func TestHandleResolve(t *testing.T) {
	w := httptest.NewRecorder()
	handleResolve(w, httptest.NewRequest("POST", "http://127.0.0.1/api/resolve", strings.NewReader(`{"ids":["laravel","ddev","mysql"]}`)))
	if body := w.Body.String(); !strings.Contains(body, `"framework":"laravel"`) || !strings.Contains(body, "ddev config") {
		t.Fatalf("resolve response wrong: %s", body)
	}
}

func TestResolveInvalidReturnsError(t *testing.T) {
	w := httptest.NewRecorder()
	handleResolve(w, httptest.NewRequest("POST", "http://127.0.0.1/api/resolve", strings.NewReader(`{"ids":["laravel","magento"]}`)))
	if !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("invalid combo should return an error field")
	}
}

func TestLoopbackGuard(t *testing.T) {
	req := httptest.NewRequest("GET", "http://evil.com/api/recipes", nil)
	req.Host = "evil.com"
	w := httptest.NewRecorder()
	loopbackOnly(handleRecipes)(w, req)
	if w.Code != 403 {
		t.Fatalf("guard should 403 a non-loopback host, got %d", w.Code)
	}
}
