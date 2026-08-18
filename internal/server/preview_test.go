package server

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	zenfiles "github.com/xZenLabs/zen-fm/internal/files"
	"golang.org/x/image/tiff"
)

func writeSparsePreviewFile(t *testing.T, api *testAPI, name string, size int64, prefix string) {
	t.Helper()
	file, err := os.OpenFile(filepath.Join(api.files.Name(), name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(prefix); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Truncate(size); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func unsafeDimensionPNG(width, height uint32) []byte {
	data := make([]byte, 13)
	binary.BigEndian.PutUint32(data[0:4], width)
	binary.BigEndian.PutUint32(data[4:8], height)
	data[8], data[9] = 8, 2
	chunk := append([]byte("IHDR"), data...)
	encoded := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\r"), chunk...)
	checksum := make([]byte, 4)
	binary.BigEndian.PutUint32(checksum, crc32.ChecksumIEEE(chunk))
	return append(encoded, checksum...)
}

func TestTIFFPreviewIsBoundedBrowserSafePNG(t *testing.T) {
	a := newTestAPI(t)
	cookie, _ := a.finishSetup()
	imageData := image.NewRGBA(image.Rect(0, 0, 4, 2))
	imageData.Set(0, 0, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := tiff.Encode(&encoded, imageData, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.files.Write("page.tiff", bytes.NewReader(encoded.Bytes()), false); err != nil {
		t.Fatal(err)
	}
	response := a.request(http.MethodGet, "/api/v1/files/preview?"+url.Values{"path": {"/page.tiff"}, "width": {"32"}, "height": {"32"}}.Encode(), nil, cookie, "", "")
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/png" || !bytes.HasPrefix(response.Body.Bytes(), []byte("\x89PNG")) {
		t.Fatalf("TIFF preview: %d %q %x", response.Code, response.Header().Get("Content-Type"), response.Body.Bytes())
	}
}

func TestGHSA_5vprHTMLAndEPUBActiveContentIsRemoved(t *testing.T) {
	a := newTestAPI(t)
	cookie, _ := a.finishSetup()
	malicious := `<h1>Safe</h1><script>alert("evil")</script><img src=x onerror=evil><a href="javascript:evil">bad</a>`
	if _, err := a.files.Write("page.html", strings.NewReader(malicious), false); err != nil {
		t.Fatal(err)
	}
	response := a.request(http.MethodGet, "/api/v1/files/preview?"+url.Values{"path": {"/page.html"}}.Encode(), nil, cookie, "", "")
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/html; charset=utf-8" || strings.Contains(response.Body.String(), "script") || strings.Contains(response.Body.String(), "evil") || strings.Contains(response.Body.String(), "img") {
		t.Fatalf("HTML sanitizer: %d %q %s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	var epub bytes.Buffer
	writer := zip.NewWriter(&epub)
	chapter, _ := writer.Create("OPS/chapter.xhtml")
	_, _ = chapter.Write([]byte(`<html><body><p>Readable chapter</p><script>epubEvil()</script></body></html>`))
	bad, _ := writer.Create("../escape.xhtml")
	_, _ = bad.Write([]byte("leak"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.files.Write("book.epub", bytes.NewReader(epub.Bytes()), false); err != nil {
		t.Fatal(err)
	}
	response = a.request(http.MethodGet, "/api/v1/files/preview?"+url.Values{"path": {"/book.epub"}}.Encode(), nil, cookie, "", "")
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/plain; charset=utf-8" || !strings.Contains(response.Body.String(), "Readable chapter") || strings.Contains(response.Body.String(), "epubEvil") || strings.Contains(response.Body.String(), "leak") {
		t.Fatalf("EPUB preview: %d %q %s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
}

func TestPreviewContentTypesForCSVPDFAndMedia(t *testing.T) {
	a := newTestAPI(t)
	cookie, _ := a.finishSetup()
	for name, content := range map[string]string{"table.csv": "a,b\n1,2\n", "paper.pdf": "%PDF-1.4\n%%EOF", "sound.mp3": "ID3media"} {
		if _, err := a.files.Write(name, strings.NewReader(content), false); err != nil {
			t.Fatal(err)
		}
	}
	checks := map[string]string{"table.csv": "text/csv; charset=utf-8", "paper.pdf": "application/pdf", "sound.mp3": "audio/mpeg"}
	for name, contentType := range checks {
		response := a.request(http.MethodGet, "/api/v1/files/preview?"+url.Values{"path": {"/" + name}}.Encode(), nil, cookie, "", "")
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != contentType {
			t.Errorf("%s preview: %d %q %s", name, response.Code, response.Header().Get("Content-Type"), response.Body.String())
		}
	}
}

func TestTextPreviewAllowsEightMiBLog(t *testing.T) {
	a := newTestAPI(t)
	cookie, _ := a.finishSetup()
	const size = int64(8 << 20)
	writeSparsePreviewFile(t, a, "crash.log", size, "panic: crash\n")

	response := a.request(http.MethodGet, "/api/v1/files/preview?"+url.Values{"path": {"/crash.log"}}.Encode(), nil, cookie, "", "")
	contentType := response.Header().Get("Content-Type")
	if response.Code != http.StatusOK || !strings.HasPrefix(contentType, "text/") || !strings.Contains(contentType, "charset=utf-8") || int64(response.Body.Len()) != size {
		t.Fatalf("large text preview: status=%d content-type=%q size=%d", response.Code, contentType, response.Body.Len())
	}
}

func TestGHSA_7xqmPreviewInputsAndDimensionsAreBounded(t *testing.T) {
	a := newTestAPI(t)
	cookie, _ := a.finishSetup()
	for _, item := range []struct {
		name   string
		size   int64
		prefix string
	}{
		{name: "large.txt", size: maxTextPreviewBytes + 1, prefix: "text"},
		{name: "large.pdf", size: maxDocumentBytes + 1, prefix: "%PDF-"},
		{name: "large.mp3", size: maxMediaPreviewBytes + 1, prefix: "ID3"},
	} {
		writeSparsePreviewFile(t, a, item.name, item.size, item.prefix)
		response := a.request(http.MethodGet, "/api/v1/files/preview?"+url.Values{"path": {"/" + item.name}}.Encode(), nil, cookie, "", "")
		if response.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s oversized preview: %d %s", item.name, response.Code, response.Body.String())
		}
	}
	if _, err := a.files.Write("unsafe.png", bytes.NewReader(unsafeDimensionPNG(5_000, 4_000)), false); err != nil {
		t.Fatal(err)
	}
	response := a.request(http.MethodGet, "/api/v1/files/preview?"+url.Values{"path": {"/unsafe.png"}}.Encode(), nil, cookie, "", "")
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("unsafe image dimensions: %d %s", response.Code, response.Body.String())
	}
}

func TestSanitizeHTMLRejectsExcessiveNestingWithoutRecursion(t *testing.T) {
	deep := strings.Repeat("<div>", 300) + "content" + strings.Repeat("</div>", 300)
	if _, err := sanitizeHTML([]byte(deep)); !errors.Is(err, zenfiles.ErrTooLarge) {
		t.Fatalf("sanitizeHTML error = %v, want ErrTooLarge", err)
	}
}

func FuzzSanitizeHTML(f *testing.F) {
	f.Add([]byte(`<p>Hello <a href="https://example.test/">world</a></p>`))
	f.Add([]byte(`<script><div>active</div></script><p>safe</p>`))
	f.Add([]byte(strings.Repeat("<div>", 300)))
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > int(maxTextPreviewBytes) {
			t.Skip()
		}
		output, err := sanitizeHTML(input)
		if err != nil {
			return
		}
		if len(output) > int(maxTextPreviewBytes) {
			t.Fatalf("sanitized output exceeds limit: %d", len(output))
		}
	})
}
