package httpapi

import "testing"

func TestDetectReceipt(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00}
	ext, mime, ok := detectReceipt(png)
	if !ok || ext != "png" || mime != "image/png" {
		t.Fatalf("png: %s %s %v", ext, mime, ok)
	}
	jpg := []byte{0xff, 0xd8, 0xff, 0xd9}
	ext, mime, ok = detectReceipt(jpg)
	if !ok || ext != "jpg" || mime != "image/jpeg" {
		t.Fatalf("jpg: %s %s %v", ext, mime, ok)
	}
	pdf := []byte("%PDF-1.4")
	ext, mime, ok = detectReceipt(pdf)
	if !ok || ext != "pdf" || mime != "application/pdf" {
		t.Fatalf("pdf: %s %s %v", ext, mime, ok)
	}
	if _, _, ok = detectReceipt([]byte("not a receipt")); ok {
		t.Fatal("plain text was accepted")
	}
}
