package server

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	zenfiles "github.com/xZenLabs/zen-fm/internal/files"
)

func TestGHSA_83xpArchiveNamesAndStreamingFormats(t *testing.T) {
	a := newTestAPI(t)
	cookie, csrf := a.finishSetup()
	if _, err := a.files.Mkdir("bundle"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.files.Write("bundle/a.txt", strings.NewReader("alpha"), false); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.txt", filepath.Join(a.files.Name(), "bundle", "link")); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"zip", "tar", "tar.gz"} {
		response := a.request(http.MethodPost, "/api/v1/files/archive", strings.NewReader(`{"paths":["/bundle"],"format":"`+format+`"}`), cookie, csrf, "")
		if response.Code != http.StatusOK {
			t.Fatalf("%s archive: %d %s", format, response.Code, response.Body.String())
		}
		names, content := archiveContents(t, format, response.Body.Bytes())
		if strings.Join(names, ",") != "bundle/,bundle/a.txt" || content != "alpha" {
			t.Fatalf("%s contents: %v %q", format, names, content)
		}
	}
	ticketResponse := a.request(http.MethodPost, "/api/v1/files/archive-tickets", strings.NewReader(`{"paths":["/bundle"],"format":"zip"}`), cookie, csrf, "")
	if ticketResponse.Code != http.StatusCreated {
		t.Fatalf("archive ticket: %d %s", ticketResponse.Code, ticketResponse.Body.String())
	}
	ticketURL, _ := decodeMap(t, ticketResponse)["url"].(string)
	secondCookie, _, _ := a.login("a secure owner password")
	wrongSession := a.request(http.MethodGet, ticketURL, nil, secondCookie, "", "")
	if wrongSession.Code != http.StatusNotFound {
		t.Fatalf("archive ticket crossed sessions: %d %s", wrongSession.Code, wrongSession.Body.String())
	}
	response := a.request(http.MethodGet, ticketURL, nil, cookie, "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("browser archive stream: %d %s", response.Code, response.Body.String())
	}
	names, content := archiveContents(t, "zip", response.Body.Bytes())
	if strings.Join(names, ",") != "bundle/,bundle/a.txt" || content != "alpha" {
		t.Fatalf("browser archive contents: %v %q", names, content)
	}
	reused := a.request(http.MethodGet, ticketURL, nil, cookie, "", "")
	if reused.Code != http.StatusNotFound {
		t.Fatalf("one-time archive ticket reused: %d %s", reused.Code, reused.Body.String())
	}
	expiring := a.request(http.MethodPost, "/api/v1/files/archive-tickets", strings.NewReader(`{"paths":["/bundle"],"format":"zip"}`), cookie, csrf, "")
	expiringURL, _ := decodeMap(t, expiring)["url"].(string)
	*a.now = a.now.Add(archiveTicketTTL)
	expired := a.request(http.MethodGet, expiringURL, nil, cookie, "", "")
	if expired.Code != http.StatusNotFound {
		t.Fatalf("expired archive ticket: %d %s", expired.Code, expired.Body.String())
	}
}

func TestGHSA_gxjxArchiveNameValidationAndCancellation(t *testing.T) {
	for _, value := range []string{"/absolute", "../escape", "a/../escape", `a\b`, "a\x00b", "a//b"} {
		if err := validateArchiveName(value); !errors.Is(err, zenfiles.ErrInvalidPath) {
			t.Errorf("validateArchiveName(%q) = %v", value, err)
		}
	}
	a := newTestAPI(t)
	if _, err := a.files.Write("book.txt", strings.NewReader("book"), false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.server.archiveManifest(ctx, []string{"/book.txt"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled manifest = %v", err)
	}
}

func archiveContents(t *testing.T, format string, data []byte) ([]string, string) {
	t.Helper()
	var names []string
	var content string
	switch format {
	case "zip":
		reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range reader.File {
			names = append(names, file.Name)
			if !file.FileInfo().IsDir() {
				stream, _ := file.Open()
				body, _ := io.ReadAll(stream)
				_ = stream.Close()
				content += string(body)
			}
		}
	case "tar", "tar.gz":
		var source io.Reader = bytes.NewReader(data)
		if format == "tar.gz" {
			gzipReader, err := gzip.NewReader(source)
			if err != nil {
				t.Fatal(err)
			}
			defer gzipReader.Close()
			source = gzipReader
		}
		reader := tar.NewReader(source)
		for {
			header, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			names = append(names, header.Name)
			if header.Typeflag == tar.TypeReg {
				body, _ := io.ReadAll(reader)
				content += string(body)
			}
		}
	}
	return names, content
}
