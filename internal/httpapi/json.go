package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
)

type successBody struct {
	Success bool `json:"success"`
	Data    any  `json:"data"`
}

type failBody struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func writeSuccess(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, successBody{Success: true, Data: data})
}

func writeFail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, failBody{Success: false, Message: message})
}

type messageBody struct {
	Message string `json:"message"`
}

func writeMessage(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, messageBody{Message: message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(body); err != nil {
		http.Error(w, "encode failed", http.StatusInternalServerError)
		return
	}
	payload := bytes.TrimRight(buf.Bytes(), "\n")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}
