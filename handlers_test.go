package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── Testes de key.go ─────────────────────────────────────────────────────────

func TestGenerateAPIKey(t *testing.T) {
	key, err := generateAPIKey()
	if err != nil {
		t.Fatalf("generateAPIKey() retornou erro: %v", err)
	}
	if !strings.HasPrefix(key, "tm_key_") {
		t.Errorf("esperava prefixo 'tm_key_', obteve: %s", key)
	}
	// 7 (prefixo) + 64 (hex de 32 bytes) = 71 chars
	if len(key) != 71 {
		t.Errorf("esperava chave com 71 chars, obteve %d", len(key))
	}
}

func TestGenerateAPIKeyIsRandom(t *testing.T) {
	key1, _ := generateAPIKey()
	key2, _ := generateAPIKey()
	if key1 == key2 {
		t.Error("duas chamadas a generateAPIKey() retornaram o mesmo valor — devem ser únicas")
	}
}

func TestHashAPIKey(t *testing.T) {
	hash := hashAPIKey("minha-chave")
	if len(hash) != 64 {
		t.Errorf("hash SHA-256 deve ter 64 chars hex, obteve %d", len(hash))
	}
	// Determinístico: mesma entrada → mesmo hash
	hash2 := hashAPIKey("minha-chave")
	if hash != hash2 {
		t.Error("hashAPIKey deve ser determinístico")
	}
}

func TestHashAPIKeyDifferentInputs(t *testing.T) {
	h1 := hashAPIKey("chave-a")
	h2 := hashAPIKey("chave-b")
	if h1 == h2 {
		t.Error("inputs diferentes devem produzir hashes diferentes")
	}
}

// ── Helpers de teste ─────────────────────────────────────────────────────────

// newTestApp cria uma instância de App com DB nulo e uma MasterKey de teste.
// Os handlers que precisam de DB usam mocks via httptest.
func newTestApp(masterKey string) *App {
	return &App{
		DB:        nil, // Substituído por mock nos testes que precisam
		MasterKey: masterKey,
	}
}

// ── healthHandler ─────────────────────────────────────────────────────────────

func TestHealthHandler(t *testing.T) {
	app := newTestApp("master-secret")
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()

	app.healthHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("esperava 200, obteve %d", rr.Code)
	}
	var body map[string]string
	json.NewDecoder(rr.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("esperava status 'ok', obteve %q", body["status"])
	}
}

// ── validateKeyHandler ────────────────────────────────────────────────────────

func TestValidateKeyHandler_MissingHeader(t *testing.T) {
	app := newTestApp("master-secret")
	req := httptest.NewRequest(http.MethodGet, "/validate", nil)
	rr := httptest.NewRecorder()

	app.validateKeyHandler(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("esperava 401 sem Authorization, obteve %d", rr.Code)
	}
}

func TestValidateKeyHandler_EmptyBearerToken(t *testing.T) {
	app := newTestApp("master-secret")
	req := httptest.NewRequest(http.MethodGet, "/validate", nil)
	req.Header.Set("Authorization", "Bearer ")
	rr := httptest.NewRecorder()

	app.validateKeyHandler(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("esperava 401 com Bearer vazio, obteve %d", rr.Code)
	}
}

// TestValidateKeyHandler_InvalidKey usa um banco fake para simular chave não encontrada.
// Como *sql.DB não tem interface, apontamos para um host inválido que fará o QueryRow
// falhar com erro de conexão → handler retorna 401.
func TestValidateKeyHandler_InvalidKey(t *testing.T) {
	// Registra um driver DSN falso usando o pgx driver já registrado
	// mas apontando para host que recusa conexão → Scan retorna erro → 401
	db, err := sql.Open("pgx", "postgres://fake:fake@127.0.0.1:1/fakedb?connect_timeout=1")
	if err != nil {
		t.Fatalf("sql.Open falhou: %v", err)
	}
	defer db.Close()

	app := &App{DB: db, MasterKey: "master-secret"}
	req := httptest.NewRequest(http.MethodGet, "/validate", nil)
	req.Header.Set("Authorization", "Bearer tm_key_invalida")
	rr := httptest.NewRecorder()

	app.validateKeyHandler(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("esperava 401 para chave inválida, obteve %d", rr.Code)
	}
}

// ── createKeyHandler ──────────────────────────────────────────────────────────

func TestCreateKeyHandler_MethodNotAllowed(t *testing.T) {
	app := newTestApp("master-secret")
	req := httptest.NewRequest(http.MethodGet, "/admin/keys", nil)
	rr := httptest.NewRecorder()

	app.createKeyHandler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("esperava 405 para GET, obteve %d", rr.Code)
	}
}

func TestCreateKeyHandler_InvalidBody(t *testing.T) {
	app := newTestApp("master-secret")
	req := httptest.NewRequest(http.MethodPost, "/admin/keys",
		bytes.NewBufferString("not json"))
	rr := httptest.NewRecorder()

	app.createKeyHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("esperava 400 para corpo inválido, obteve %d", rr.Code)
	}
}

func TestCreateKeyHandler_MissingName(t *testing.T) {
	app := newTestApp("master-secret")
	body, _ := json.Marshal(map[string]string{"name": ""})
	req := httptest.NewRequest(http.MethodPost, "/admin/keys", bytes.NewReader(body))
	rr := httptest.NewRecorder()

	app.createKeyHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("esperava 400 para nome vazio, obteve %d", rr.Code)
	}
}

// ── masterKeyAuthMiddleware ───────────────────────────────────────────────────

func TestMasterKeyMiddleware_Forbidden(t *testing.T) {
	app := newTestApp("correct-master-key")

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := app.masterKeyAuthMiddleware(inner)

	tests := []struct {
		name   string
		header string
	}{
		{"sem header", ""},
		{"chave errada", "Bearer wrong-key"},
		{"Bearer com chave errada", "Bearer not-the-master-key"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/keys", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusForbidden {
				t.Errorf("[%s] esperava 403, obteve %d", tc.name, rr.Code)
			}
		})
	}
}

func TestMasterKeyMiddleware_ValidKey(t *testing.T) {
	app := newTestApp("correct-master-key")
	called := false

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := app.masterKeyAuthMiddleware(inner)

	req := httptest.NewRequest(http.MethodPost, "/admin/keys", nil)
	req.Header.Set("Authorization", "Bearer correct-master-key")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("handler interno não foi chamado com chave válida")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("esperava 200, obteve %d", rr.Code)
	}
}
