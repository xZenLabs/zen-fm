package server

import (
	"path"
	"strings"
	"testing"
)

func FuzzArchiveNameValidation(f *testing.F) {
	f.Add("folder/book.epub")
	f.Add("../escape")
	f.Add(`folder\escape`)
	f.Fuzz(func(t *testing.T, name string) {
		if validateArchiveName(name) != nil {
			return
		}
		if name == "" || path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\\x00") {
			t.Fatalf("unsafe archive name accepted: %q", name)
		}
		for _, component := range strings.Split(name, "/") {
			if component == "" || component == "." || component == ".." {
				t.Fatalf("unsafe archive component accepted: %q", name)
			}
		}
	})
}

func FuzzUploadMetadataParser(f *testing.F) {
	f.Add("path L0Jvb2tzL25vdGUudHh0,overwrite ZmFsc2U=")
	f.Add("path !!!")
	f.Add("path Lw==,path Lw==")
	f.Fuzz(func(t *testing.T, header string) {
		metadata, err := parseUploadMetadata(header)
		if err != nil {
			return
		}
		if len(header) > 8<<10 {
			t.Fatal("oversized metadata accepted")
		}
		for key, value := range metadata {
			if !validMetadataKey(key) || len(value) > 4096 || strings.ContainsRune(value, 0) {
				t.Fatalf("invalid decoded metadata accepted: %q=%q", key, value)
			}
		}
	})
}
