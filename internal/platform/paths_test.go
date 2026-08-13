package platform

import "testing"

func TestDefaultRootIncludesPocketBookStorage(t *testing.T) {
	found := false
	for _, candidate := range ereaderRootCandidates {
		found = found || candidate == "/mnt/ext1"
	}
	if !found {
		t.Fatal("PocketBook storage is not a default root candidate")
	}
}
