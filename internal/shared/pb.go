// Клиент PocketBase-sidecar: базовый URL, токен superuser с кэшем
// и повторной авторизацией при 401. Выделено из pb_billing.go без
// изменения логики — все сервисы ходят в PB только через него.
package shared

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// PBBaseURL — адрес sidecar (по умолчанию только localhost :8090).
func PBBaseURL() string {
	if u := strings.TrimSpace(os.Getenv("PB_URL")); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://127.0.0.1:8090"
}

var pbTokenMu sync.Mutex
var pbTokenCached = struct {
	token string
	at    time.Time
}{}

// PBAuthToken — токен superuser (кэш 24ч, в PB живёт ~7 суток).
func PBAuthToken() (string, error) {
	pbTokenMu.Lock()
	defer pbTokenMu.Unlock()
	if pbTokenCached.token != "" && time.Since(pbTokenCached.at) < 24*time.Hour {
		return pbTokenCached.token, nil
	}
	email := strings.TrimSpace(os.Getenv("PB_ADMIN_EMAIL"))
	pass := os.Getenv("PB_ADMIN_PASSWORD")
	if email == "" || pass == "" {
		return "", fmt.Errorf("не заданы PB_ADMIN_EMAIL/PB_ADMIN_PASSWORD")
	}
	body, _ := json.Marshal(map[string]string{"identity": email, "password": pass})
	req, err := http.NewRequest(http.MethodPost,
		PBBaseURL()+"/api/collections/_superusers/auth-with-password",
		bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("вход PB не удался (HTTP %d)", resp.StatusCode)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil || out.Token == "" {
		return "", fmt.Errorf("некорректный ответ auth PB")
	}
	pbTokenCached.token = out.Token
	pbTokenCached.at = time.Now()
	return out.Token, nil
}

// PBDropToken сбрасывает кэш токена (после 401).
func PBDropToken() {
	pbTokenMu.Lock()
	pbTokenCached.token = ""
	pbTokenMu.Unlock()
}

// PBAPI — запрос к PB REST API от имени superuser. При 401 сбрасывает
// токен и повторяет один раз. Возвращает тело, статус и ошибку.
func PBAPI(method, path string, body any) ([]byte, int, error) {
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		token, err := PBAuthToken()
		if err != nil {
			return nil, 0, err
		}
		var reqBody *bytes.Reader
		if data != nil {
			reqBody = bytes.NewReader(data)
		} else {
			reqBody = bytes.NewReader(nil)
		}
		req, err := http.NewRequest(method, PBBaseURL()+path, reqBody)
		if err != nil {
			return nil, 0, err
		}
		if data != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, 0, err
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			PBDropToken()
			continue
		}
		return respBody, resp.StatusCode, nil
	}
	return nil, 0, fmt.Errorf("не удалось выполнить запрос PB")
}
