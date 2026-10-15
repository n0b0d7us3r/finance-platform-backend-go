package httpapi

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"finance-platform/go/internal/store"

	"github.com/go-chi/chi/v5"
)

const receiptMax = 5120 * 1024

var errReceipt = errors.New("receipt")

func detectReceipt(body []byte) (ext, mime string, ok bool) {
	switch {
	case len(body) >= 8 && bytes.Equal(body[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return "png", "image/png", true
	case len(body) >= 3 && body[0] == 0xff && body[1] == 0xd8 && body[2] == 0xff:
		return "jpg", "image/jpeg", true
	case len(body) >= 5 && bytes.HasPrefix(body, []byte("%PDF-")):
		return "pdf", "application/pdf", true
	default:
		return "", "", false
	}
}

func (h *handler) takeReceipt(r *http.Request) ([]byte, error) {
	content := r.Header.Get("Content-Type")
	var raw []byte
	switch {
	case strings.Contains(content, "multipart/form-data"):
		if err := r.ParseMultipartForm(6 << 20); err != nil {
			return nil, errReceipt
		}
		file, _, err := r.FormFile("receipt")
		if err == nil {
			defer file.Close()
			raw, err = readLimited(file)
			if err != nil {
				return nil, errReceipt
			}
			break
		}
		encoded := r.FormValue("receipt")
		if encoded == "" {
			return nil, errReceipt
		}
		raw, err = decodeReceipt(encoded)
		if err != nil {
			return nil, errReceipt
		}
	case strings.Contains(content, "application/json"):
		var body struct {
			Receipt string `json:"receipt"`
		}
		if err := decode(r, &body); err != nil || body.Receipt == "" {
			return nil, errReceipt
		}
		var err error
		raw, err = decodeReceipt(body.Receipt)
		if err != nil {
			return nil, errReceipt
		}
	default:
		if err := r.ParseForm(); err != nil {
			return nil, errReceipt
		}
		encoded := r.FormValue("receipt")
		if encoded == "" {
			return nil, errReceipt
		}
		var err error
		raw, err = decodeReceipt(encoded)
		if err != nil {
			return nil, errReceipt
		}
	}
	if len(raw) == 0 || len(raw) > receiptMax {
		return nil, errReceipt
	}
	if _, _, ok := detectReceipt(raw); !ok {
		return nil, errReceipt
	}
	return raw, nil
}

func (h *handler) saveReceipt(body []byte) (string, error) {
	ext, _, ok := detectReceipt(body)
	if !ok {
		return "", errReceipt
	}
	dir := h.deps.ReceiptsDir
	if dir == "" {
		dir = "/tmp/receipts"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	name := "receipt_" + hex.EncodeToString(buf) + "." + ext
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		return "", err
	}
	return name, nil
}

func (h *handler) removeReceipt(name string) {
	if name == "" {
		return
	}
	dir := h.deps.ReceiptsDir
	if dir == "" {
		dir = "/tmp/receipts"
	}
	_ = os.Remove(filepath.Join(dir, filepath.Base(name)))
}

func (h *handler) disputeReceipt(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.user(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeMessage(w, http.StatusNotFound, "No query results for model [App\\Models\\Dispute].")
		return
	}
	var receipt sql.NullString
	var owner sql.NullInt64
	err = h.deps.DB.SQL.QueryRowContext(r.Context(), `
		SELECT d.receipt, pd.user_id
		FROM disputes d
		INNER JOIN orders o ON o.id = d.order_id
		LEFT JOIN payment_details pd ON pd.id = o.payment_detail_id
		WHERE d.id = ?`, id).Scan(&receipt, &owner)
	if err != nil {
		writeMessage(w, http.StatusNotFound, "No query results for model [App\\Models\\Dispute].")
		return
	}
	if !receipt.Valid || receipt.String == "" {
		writeMessage(w, http.StatusNotFound, "No query results for model [App\\Models\\Dispute].")
		return
	}
	support, err := store.HasRole(r.Context(), h.deps.DB.SQL, actor, "Support")
	if err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	admin, err := store.HasRole(r.Context(), h.deps.DB.SQL, actor, "Admin")
	if err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	ownerOK := owner.Valid && owner.Int64 == actor
	if !support && !admin && !ownerOK {
		writeMessage(w, http.StatusForbidden, unauthorizedMessage)
		return
	}
	name := filepath.Base(receipt.String)
	dir := h.deps.ReceiptsDir
	if dir == "" {
		dir = "/tmp/receipts"
	}
	file, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		writeMessage(w, http.StatusNotFound, "No query results for model [App\\Models\\Dispute].")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Disposition", "inline; filename=\""+name+"\"")
	http.ServeContent(w, r, name, info.ModTime(), file)
}

func readLimited(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, receiptMax+1))
	if err != nil || len(body) > receiptMax {
		return nil, errReceipt
	}
	return body, nil
}

func decodeReceipt(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if i := strings.Index(value, ","); strings.HasPrefix(value, "data:") && i >= 0 {
		value = value[i+1:]
	}
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' {
			return -1
		}
		return r
	}, value)
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.RawStdEncoding.DecodeString(value)
}
