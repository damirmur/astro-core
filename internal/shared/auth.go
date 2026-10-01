// Общие хелперы авторизации: свои HMAC-токены (auth.go), ключ
// балансировщика LLM (llm_balancer.go) и notify_key (main.go/proxy.go).
package shared

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Версия формата своих токенов (authTokenVer в auth.go).
const TokenVer = "v1"

// AuthClaims — полезная нагрузка своего токена (uid = id bl_users).
type AuthClaims struct {
	UID         string `json:"uid"`
	Provider    string `json:"provider"`
	ExternalID  string `json:"external_id"`
	DisplayName string `json:"display_name,omitempty"`
	Iat         int64  `json:"iat"`
	Exp         int64  `json:"exp"`
}

// AuthSecret — секрет подписи токенов (минимум 16 символов).
func AuthSecret() ([]byte, error) {
	s := os.Getenv("AUTH_SECRET")
	if len(s) < 16 {
		return nil, fmt.Errorf("не задан AUTH_SECRET (минимум 16 символов)")
	}
	return []byte(s), nil
}

// AuthTTL — срок жизни токена (AUTH_TTL_DAYS, по умолчанию 30 суток).
func AuthTTL() time.Duration {
	if d, err := strconv.Atoi(strings.TrimSpace(os.Getenv("AUTH_TTL_DAYS"))); err == nil && d > 0 && d <= 365 {
		return time.Duration(d) * 24 * time.Hour
	}
	return 30 * 24 * time.Hour
}

// MintAuthToken подписывает новую сессию, возвращает токен и exp (unix).
func MintAuthToken(uid, provider, externalID, displayName string) (string, int64, error) {
	secret, err := AuthSecret()
	if err != nil {
		return "", 0, err
	}
	now := time.Now()
	exp := now.Add(AuthTTL()).Unix()
	payload, err := json.Marshal(AuthClaims{
		UID: uid, Provider: provider, ExternalID: externalID,
		DisplayName: displayName, Iat: now.Unix(), Exp: exp,
	})
	if err != nil {
		return "", 0, err
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	sig := mac.Sum(nil)
	tok := TokenVer + "." +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(sig)
	return tok, exp, nil
}

// VerifyAuthToken проверяет подпись и срок токена.
func VerifyAuthToken(tok string) (*AuthClaims, error) {
	secret, err := AuthSecret()
	if err != nil {
		return nil, err
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || parts[0] != TokenVer {
		return nil, fmt.Errorf("неверный формат токена")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("неверный формат токена")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("неверный формат токена")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	if subtle.ConstantTimeCompare(mac.Sum(nil), sig) != 1 {
		return nil, fmt.Errorf("неверная подпись токена")
	}
	var c AuthClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, fmt.Errorf("неверный формат токена")
	}
	if c.UID == "" || c.Provider == "" || c.ExternalID == "" {
		return nil, fmt.Errorf("неверный формат токена")
	}
	if time.Now().Unix() > c.Exp {
		return nil, fmt.Errorf("срок действия токена истёк")
	}
	return &c, nil
}

// BearerToken достаёт токен из Authorization: Bearer.
func BearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// AuthIdentity — личность вызывающего по Bearer-токену (401 при ошибке).
func AuthIdentity(r *http.Request) (*AuthClaims, error) {
	tok := BearerToken(r)
	if tok == "" {
		return nil, fmt.Errorf("нет токена авторизации")
	}
	return VerifyAuthToken(tok)
}

// ExtractBalancerKey — ключ балансировщика из заголовков или query
// (X-API-Key / X-LLM-Proxy-Key / Authorization / ?api_key=…).
func ExtractBalancerKey(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-API-Key")); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.Header.Get("X-LLM-Proxy-Key")); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.Header.Get("X-LLM-Key")); v != "" {
		return v
	}
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); auth != "" {
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			return strings.TrimSpace(auth[7:])
		}
		return auth
	}
	for _, q := range []string{"api_key", "key", "llm_proxy_key", "balancer_key", "llm_key"} {
		if v := strings.TrimSpace(r.URL.Query().Get(q)); v != "" {
			return v
		}
	}
	return ""
}

// BalancerKeyExpected — ожидаемый LLM_PROXY_KEY (фолбэк NOTIFY_KEY).
func BalancerKeyExpected() string {
	if v := strings.TrimSpace(os.Getenv("LLM_PROXY_KEY")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("NOTIFY_KEY"))
}

// IsBalancerAuthorized — constant-time проверка ключа балансировщика.
func IsBalancerAuthorized(r *http.Request) bool {
	expected := BalancerKeyExpected()
	if expected == "" {
		return false
	}
	provided := ExtractBalancerKey(r)
	if provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

// IsNotifyAuthorized — проверка notify_key (query или поле тела).
func IsNotifyAuthorized(r *http.Request, bodyKey string) bool {
	expected := strings.TrimSpace(os.Getenv("NOTIFY_KEY"))
	if expected == "" {
		return false
	}
	candidates := []string{
		r.URL.Query().Get("notify_key"),
		bodyKey,
		r.URL.Query().Get("key"),
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(c), []byte(expected)) == 1 {
			return true
		}
	}
	return false
}
