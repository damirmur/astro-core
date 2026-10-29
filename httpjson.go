package main

import (
	"encoding/json"
	"net/http"
)

// writeJSONError отдаёт ошибку в виде {"error": "..."}.
//
// Раньше тело собиралось через fmt.Sprintf + http.Error, поэтому кавычки и
// переводы строк внутри текста ошибки не экранировались: на клиенте resp.json()
// падал с SyntaxError, и вместо понятного сообщения пользователь видел
// «что-то пошло не так». Теперь сериализацией занимается encoding/json.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
