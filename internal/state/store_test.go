package state

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/xZenLabs/zen-fm/internal/auth"
)

func testStore(t *testing.T, now *time.Time) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state", "zenfm.db"), Options{
		Now:            func() time.Time { return *now },
		PasswordParams: auth.PasswordParams{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestInitialOwnerAndSettings(t *testing.T) {
	now := time.Unix(1000, 0)
	s := testStore(t, &now)
	owner, err := s.Owner()
	if err != nil {
		t.Fatal(err)
	}
	if owner.Username != SetupUsername || !owner.SetupRequired || !auth.VerifyPassword(owner.PasswordHash, SetupPassword) {
		t.Fatalf("unexpected owner: %+v", owner)
	}
	settings, err := s.Settings()
	if err != nil || settings.Theme != "system" || settings.ClientTimeoutSeconds != 30 {
		t.Fatalf("unexpected settings: %+v, %v", settings, err)
	}
}

func TestSessionIdleAndAbsoluteExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	s := testStore(t, &now)
	v, err := s.CreateSession("secret", "csrf", 2*time.Hour, 12*time.Hour)
	if err != nil || v.CSRFToken != "csrf" {
		t.Fatalf("create: %+v %v", v, err)
	}
	now = now.Add(time.Hour)
	v, err = s.Session("secret", 2*time.Hour, true)
	if err != nil || v.IdleUntil != now.Add(2*time.Hour).Unix() {
		t.Fatalf("touch: %+v %v", v, err)
	}
	now = time.Unix(1000, 0).Add(12 * time.Hour)
	if _, err := s.Session("secret", time.Hour, true); !errors.Is(err, ErrExpired) {
		t.Fatalf("expected expiry, got %v", err)
	}
}

func TestPasswordReplacementRevokesCredentials(t *testing.T) {
	now := time.Unix(1000, 0)
	s := testStore(t, &now)
	_, _ = s.CreateSession("session", "csrf", time.Hour, 2*time.Hour)
	_ = s.CreateToken("token", APIToken{ID: "id", Name: "phone", ExpiresAt: now.Add(time.Hour).Unix()})
	hash, _ := auth.HashPassword("a much longer password", auth.PasswordParams{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16})
	if err := s.ReplacePassword(hash, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session("session", time.Hour, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session not revoked: %v", err)
	}
	if _, err := s.Token("token", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("token not revoked: %v", err)
	}
}

func TestShareSecretIsHashed(t *testing.T) {
	now := time.Unix(1000, 0)
	s := testStore(t, &now)
	digest := auth.TokenDigest("capability")
	v := Share{ID: "share", Path: "book.epub", Name: "Book", SecretDigest: digest[:], CreatedAt: now.Unix()}
	if err := s.CreateShare(v); err != nil {
		t.Fatal(err)
	}
	got, err := s.ShareBySecret("capability")
	if err != nil || got.ID != "share" {
		t.Fatalf("lookup: %+v %v", got, err)
	}
	if _, err := s.ShareBySecret("wrong"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteShareRevokesPublicSessions(t *testing.T) {
	now := time.Unix(1000, 0)
	s := testStore(t, &now)
	digest := auth.TokenDigest("capability")
	if err := s.CreateShare(Share{ID: "share", Path: "book.epub", SecretDigest: digest[:], CreatedAt: now.Unix()}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreatePublicSession("public-cookie", PublicSession{ShareID: "share", ExpiresAt: now.Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	deleted, err := s.DeleteShare("share")
	if err != nil || !deleted {
		t.Fatalf("delete: %v %v", deleted, err)
	}
	if _, err := s.PublicSession("public-cookie", "share"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("public session survived revocation: %v", err)
	}
}

func TestGHSA_pp88SharePathInvalidationUsesComponentBoundaries(t *testing.T) {
	now := time.Unix(1000, 0)
	s := testStore(t, &now)
	for index, ownerPath := range []string{"/a", "/a/child", "/ab"} {
		secret := fmt.Sprintf("secret-%d", index)
		digest := auth.TokenDigest(secret)
		if err := s.CreateShare(Share{ID: fmt.Sprintf("share-%d", index), Path: ownerPath, SecretDigest: digest[:], CreatedAt: now.Unix()}); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := s.DeleteSharesAtOrBelow("/a")
	if err != nil || deleted != 2 {
		t.Fatalf("deleted = %d, %v", deleted, err)
	}
	shares, err := s.Shares()
	if err != nil || len(shares) != 1 || shares[0].Path != "/ab" {
		t.Fatalf("boundary share removed: %+v %v", shares, err)
	}
}

func TestExpiredStateGarbageCollection(t *testing.T) {
	now := time.Unix(1000, 0)
	s := testStore(t, &now)
	_, _ = s.CreateSession("session", "csrf", time.Second, time.Second)
	_ = s.CreateToken("token", APIToken{ID: "token", Name: "phone", CreatedAt: now.Unix(), ExpiresAt: now.Add(time.Second).Unix()})
	digest := auth.TokenDigest("secret")
	_ = s.CreateShare(Share{ID: "share", Path: "/book", SecretDigest: digest[:], CreatedAt: now.Unix(), ExpiresAt: now.Add(time.Second).Unix()})
	_ = s.CreatePublicSession("public", PublicSession{ShareID: "share", ExpiresAt: now.Add(time.Second).Unix()})
	_ = s.CreateUpload(Upload{ID: "upload", Path: "/book", Length: 3, CreatedAt: now.Unix(), ExpiresAt: now.Add(time.Second).Unix()})
	now = now.Add(2 * time.Second)
	expired, err := s.PruneExpired()
	if err != nil || len(expired) != 1 || expired[0].ID != "upload" {
		t.Fatalf("expired uploads: %+v %v", expired, err)
	}
	if shares, _ := s.Shares(); len(shares) != 0 {
		t.Fatalf("expired share survived: %+v", shares)
	}
	if _, err := s.PublicSession("public", "share"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("public session survived: %v", err)
	}
}
