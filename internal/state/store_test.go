package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
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

func TestOpenModeLessFilesystemAllowsPermissionDeniedChmod(t *testing.T) {
	original := chmodDataDirectory
	chmodDataDirectory = func(string, os.FileMode) error { return syscall.EPERM }
	t.Cleanup(func() { chmodDataDirectory = original })

	options := Options{
		ModeLessFilesystem: true,
		PasswordParams:     auth.PasswordParams{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16},
	}
	store, err := Open(filepath.Join(t.TempDir(), "portable", "zenfm.db"), options)
	if err != nil {
		t.Fatalf("mode-less filesystem failed to open state: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	options.ModeLessFilesystem = false
	if _, err := Open(filepath.Join(t.TempDir(), "strict", "zenfm.db"), options); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("strict mode did not preserve chmod failure: %v", err)
	}
}

func TestInitialOwnerAndSettings(t *testing.T) {
	now := time.Unix(1000, 0)
	s := testStore(t, &now)
	owner, err := s.Owner()
	if err != nil {
		t.Fatal(err)
	}
	if !owner.SetupRequired || !auth.VerifyPassword(owner.PasswordHash, SetupPassword) {
		t.Fatalf("unexpected owner: %+v", owner)
	}
	settings, err := s.Settings()
	if err != nil || settings.Theme != "system" || settings.ClientTimeoutSeconds != 30 {
		t.Fatalf("unexpected settings: %+v, %v", settings, err)
	}
}

func TestInitialSettingsCanShowHiddenByDefaultWithoutOverwritingSavedPreference(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "zenfm.db")
	options := Options{
		PasswordParams:      auth.PasswordParams{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16},
		ShowHiddenByDefault: true,
	}
	s, err := Open(path, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	settings, err := s.Settings()
	if err != nil || !settings.ShowHidden {
		t.Fatalf("unexpected initial settings: %+v, %v", settings, err)
	}
	settings.ShowHidden = false
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(path, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	settings, err = s.Settings()
	if err != nil || settings.ShowHidden {
		t.Fatalf("saved preference was overwritten: %+v, %v", settings, err)
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

func TestSessionTouchesUseReadTransactionsAndCoalesce(t *testing.T) {
	now := time.Unix(1000, 0)
	s := testStore(t, &now)
	idle := 10 * time.Minute
	created, err := s.CreateSession("session", "csrf", idle, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	now = now.Add(30 * time.Second)
	stats := s.db.Stats()
	coalesced, err := s.Session("session", idle, true)
	if err != nil {
		t.Fatal(err)
	}
	if reads := s.db.Stats().TxN - stats.TxN; reads != 1 {
		t.Fatalf("coalesced lookup started %d read transactions, want 1", reads)
	}
	if coalesced.LastSeenAt != now.Unix() || coalesced.IdleUntil != now.Add(idle).Unix() {
		t.Fatalf("coalesced touch did not return its effective deadline: got %+v, created %+v", coalesced, created)
	}

	now = now.Add(30 * time.Second)
	touched, err := s.Session("session", idle, true)
	if err != nil {
		t.Fatal(err)
	}
	if touched.LastSeenAt != now.Unix() || touched.IdleUntil != now.Add(idle).Unix() {
		t.Fatalf("persisted touch = %+v", touched)
	}
	persisted, err := s.Session("session", idle, false)
	if err != nil || persisted.LastSeenAt != touched.LastSeenAt || persisted.IdleUntil != touched.IdleUntil {
		t.Fatalf("stored touch = %+v, %v", persisted, err)
	}
}

func TestSessionTouchIntervalIsShorterThanShortIdleWindow(t *testing.T) {
	now := time.Unix(1000, 0)
	s := testStore(t, &now)
	idle := 40 * time.Second
	_, err := s.CreateSession("short-session", "csrf", idle, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	now = now.Add(19 * time.Second)
	coalesced, err := s.Session("short-session", idle, true)
	if err != nil || coalesced.LastSeenAt != now.Unix() || coalesced.IdleUntil != now.Add(idle).Unix() {
		t.Fatalf("early short-idle touch = %+v, %v", coalesced, err)
	}
	now = now.Add(time.Second)
	touched, err := s.Session("short-session", idle, true)
	if err != nil || touched.LastSeenAt != now.Unix() || touched.IdleUntil != now.Add(idle).Unix() {
		t.Fatalf("due short-idle touch = %+v, %v", touched, err)
	}
}

func TestCoalescedSessionTouchSurvivesPersistedIdleDeadline(t *testing.T) {
	now := time.Unix(1000, 0)
	s := testStore(t, &now)
	idle := 40 * time.Second
	_, err := s.CreateSession("active-short-session", "csrf", idle, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	now = now.Add(19 * time.Second)
	coalesced, err := s.Session("active-short-session", idle, true)
	if err != nil || coalesced.LastSeenAt != now.Unix() || coalesced.IdleUntil != now.Add(idle).Unix() {
		t.Fatalf("coalesced touch = %+v, %v", coalesced, err)
	}

	// This request is still within one idle window of the coalesced touch even
	// though the older, persisted idle deadline has just been reached.
	now = now.Add(21 * time.Second)
	touched, err := s.Session("active-short-session", idle, true)
	if err != nil {
		t.Fatalf("active session expired at persisted deadline: %v", err)
	}
	if touched.LastSeenAt != now.Unix() || touched.IdleUntil != now.Add(idle).Unix() {
		t.Fatalf("persisted boundary touch = %+v", touched)
	}
}

func TestPruneExpiredPersistsPendingSessionTouch(t *testing.T) {
	now := time.Unix(1000, 0)
	s := testStore(t, &now)
	idle := 40 * time.Second
	if _, err := s.CreateSession("pruned-short-session", "csrf", idle, time.Hour); err != nil {
		t.Fatal(err)
	}

	now = now.Add(19 * time.Second)
	if _, err := s.Session("pruned-short-session", idle, true); err != nil {
		t.Fatal(err)
	}
	now = now.Add(21 * time.Second)
	if _, err := s.PruneExpired(); err != nil {
		t.Fatal(err)
	}
	persisted, err := s.Session("pruned-short-session", idle, false)
	if err != nil {
		t.Fatalf("prune removed active session: %v", err)
	}
	if persisted.LastSeenAt != now.Add(-21*time.Second).Unix() || persisted.IdleUntil != now.Add(19*time.Second).Unix() {
		t.Fatalf("session touch persisted by prune = %+v", persisted)
	}

	now = now.Add(19 * time.Second)
	if _, err := s.PruneExpired(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session("pruned-short-session", idle, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session survived its effective idle deadline: %v", err)
	}
}

func TestExpiredSessionLookupDeletesRecord(t *testing.T) {
	now := time.Unix(1000, 0)
	s := testStore(t, &now)
	idle := 2 * time.Minute
	if _, err := s.CreateSession("expired-session", "csrf", idle, time.Hour); err != nil {
		t.Fatal(err)
	}

	now = now.Add(idle)
	if _, err := s.Session("expired-session", idle, true); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired lookup = %v", err)
	}
	if _, err := s.Session("expired-session", idle, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session was not deleted: %v", err)
	}
}

func TestSessionTouchRereadsBeforePersisting(t *testing.T) {
	now := time.Unix(1000, 0)
	s := testStore(t, &now)
	idle := 10 * time.Minute
	if _, err := s.CreateSession("revoked-session", "csrf", idle, time.Hour); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)

	writer, err := s.db.Begin(true)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	stats := s.db.Stats()
	result := make(chan error, 1)
	go func() {
		_, lookupErr := s.Session("revoked-session", idle, true)
		result <- lookupErr
	}()
	deadline := time.Now().Add(time.Second)
	for {
		current := s.db.Stats()
		if current.TxN > stats.TxN && current.OpenTxN == stats.OpenTxN {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("session lookup did not complete its read transaction")
		}
		runtime.Gosched()
	}
	if err := writer.Bucket(bucketSessions).Delete(digestKey("revoked-session")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrNotFound) {
		t.Fatalf("touch restored revoked session: %v", err)
	}
}

func TestTokenTouchesCoalescePersistAndExpire(t *testing.T) {
	now := time.Unix(1000, 0)
	s := testStore(t, &now)
	expiresAt := now.Add(3 * time.Minute).Unix()
	if err := s.CreateToken("token", APIToken{ID: "id", Name: "phone", CreatedAt: now.Unix(), ExpiresAt: expiresAt}); err != nil {
		t.Fatal(err)
	}

	untouched, err := s.Token("token", false)
	if err != nil || untouched.LastUsedAt != 0 {
		t.Fatalf("untouched token = %+v, %v", untouched, err)
	}
	first, err := s.Token("token", true)
	if err != nil || first.LastUsedAt != now.Unix() {
		t.Fatalf("first token touch = %+v, %v", first, err)
	}

	now = now.Add(59 * time.Second)
	stats := s.db.Stats()
	coalesced, err := s.Token("token", true)
	if err != nil || coalesced.LastUsedAt != first.LastUsedAt {
		t.Fatalf("coalesced token touch = %+v, %v", coalesced, err)
	}
	if reads := s.db.Stats().TxN - stats.TxN; reads != 1 {
		t.Fatalf("coalesced token lookup started %d read transactions, want 1", reads)
	}

	now = now.Add(time.Second)
	touched, err := s.Token("token", true)
	if err != nil || touched.LastUsedAt != now.Unix() {
		t.Fatalf("persisted token touch = %+v, %v", touched, err)
	}
	persisted, err := s.Token("token", false)
	if err != nil || persisted.LastUsedAt != touched.LastUsedAt {
		t.Fatalf("stored token touch = %+v, %v", persisted, err)
	}

	now = time.Unix(expiresAt, 0)
	if _, err := s.Token("token", true); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired token lookup = %v", err)
	}
	if _, err := s.Token("token", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired token was not deleted: %v", err)
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
