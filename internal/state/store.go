// Package state owns ZenFM's single bbolt database schema.
package state

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xZenLabs/zen-fm/internal/auth"
	"github.com/xZenLabs/zen-fm/internal/platform"
	bolt "go.etcd.io/bbolt"
)

const (
	SetupPassword           = "koreader123456789"
	credentialTouchInterval = time.Minute
)

var (
	bucketOwner          = []byte("owner")
	bucketSettings       = []byte("settings")
	bucketSessions       = []byte("sessions")
	bucketTokens         = []byte("tokens")
	bucketShares         = []byte("shares")
	bucketShareSecrets   = []byte("shareSecrets")
	bucketPublicSessions = []byte("publicSessions")
	bucketUploads        = []byte("uploads")
	ownerKey             = []byte("single")
	settingsKey          = []byte("settings")
	chmodDataDirectory   = os.Chmod
)

type Options struct {
	PasswordParams      auth.PasswordParams
	Now                 func() time.Time
	ModeLessFilesystem  bool
	ShowHiddenByDefault bool
}

type Store struct {
	db  *bolt.DB
	now func() time.Time

	// Pending touches preserve exact idle-session semantics between coalesced
	// bbolt writes. sessionMu also makes them atomic with revocation and pruning.
	sessionMu      sync.Mutex
	sessionTouches map[string]pendingSessionTouch
}

type pendingSessionTouch struct {
	idleUntil   int64
	lastSeenAt  int64
	id          string
	csrfToken   string
	createdAt   int64
	absoluteEnd int64
}

type Owner struct {
	PasswordHash  string `json:"passwordHash"`
	SetupRequired bool   `json:"setupRequired"`
	PasswordSetAt int64  `json:"passwordSetAt"`
}

type Settings struct {
	Theme                string `json:"theme"`
	Locale               string `json:"locale"`
	ShowHidden           bool   `json:"showHidden"`
	ClientTimeoutSeconds int    `json:"clientTimeoutSeconds"`
}

type Session struct {
	ID          string `json:"id"`
	CSRFToken   string `json:"csrfToken"`
	CreatedAt   int64  `json:"createdAt"`
	LastSeenAt  int64  `json:"lastSeenAt"`
	IdleUntil   int64  `json:"idleUntil"`
	AbsoluteEnd int64  `json:"absoluteEnd"`
}

type APIToken struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CreatedAt  int64  `json:"createdAt"`
	ExpiresAt  int64  `json:"expiresAt"`
	LastUsedAt int64  `json:"lastUsedAt,omitempty"`
}

type Share struct {
	ID           string `json:"id"`
	Path         string `json:"path"`
	Name         string `json:"name"`
	SecretDigest []byte `json:"secretDigest"`
	PasswordHash string `json:"passwordHash,omitempty"`
	CreatedAt    int64  `json:"createdAt"`
	ExpiresAt    int64  `json:"expiresAt,omitempty"`
}

type PublicSession struct {
	ShareID   string `json:"shareId"`
	ExpiresAt int64  `json:"expiresAt"`
}

type Upload struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Length    int64  `json:"length"`
	Offset    int64  `json:"offset"`
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt"`
	Overwrite bool   `json:"overwrite"`
}

func Open(path string, opts Options) (*Store, error) {
	if path == "" {
		return nil, errors.New("database path is empty")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.PasswordParams == (auth.PasswordParams{}) {
		opts.PasswordParams = auth.DefaultPasswordParams
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	if err := platform.ModeChangeError(chmodDataDirectory(filepath.Dir(path), 0o700), opts.ModeLessFilesystem); err != nil {
		return nil, fmt.Errorf("secure data directory: %w", err)
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second, NoGrowSync: false})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	s := &Store{db: db, now: opts.Now}
	if err := s.initialize(opts.PasswordParams, opts.ShowHiddenByDefault); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) initialize(params auth.PasswordParams, showHiddenByDefault bool) error {
	err := s.db.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{bucketOwner, bucketSettings, bucketSessions, bucketTokens, bucketShares, bucketShareSecrets, bucketPublicSessions, bucketUploads} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}
	var exists bool
	err = s.db.View(func(tx *bolt.Tx) error {
		exists = tx.Bucket(bucketOwner).Get(ownerKey) != nil
		return nil
	})
	if err != nil || exists {
		return err
	}
	hash, err := auth.HashPassword(SetupPassword, params)
	if err != nil {
		return fmt.Errorf("hash setup password: %w", err)
	}
	now := s.now().Unix()
	owner := Owner{PasswordHash: hash, SetupRequired: true, PasswordSetAt: now}
	settings := Settings{Theme: "system", Locale: "en", ShowHidden: showHiddenByDefault, ClientTimeoutSeconds: 30}
	return s.db.Update(func(tx *bolt.Tx) error {
		if tx.Bucket(bucketOwner).Get(ownerKey) != nil {
			return nil
		}
		if err := putJSON(tx.Bucket(bucketOwner), ownerKey, owner); err != nil {
			return err
		}
		return putJSON(tx.Bucket(bucketSettings), settingsKey, settings)
	})
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) DataDir() string { return filepath.Dir(s.db.Path()) }

func (s *Store) Owner() (Owner, error) {
	var v Owner
	err := s.db.View(func(tx *bolt.Tx) error { return getJSON(tx.Bucket(bucketOwner), ownerKey, &v) })
	return v, err
}

func (s *Store) ReplacePassword(encoded string, setupRequired bool) error {
	if encoded == "" {
		return errors.New("password hash is empty")
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	err := s.db.Update(func(tx *bolt.Tx) error {
		var owner Owner
		if err := getJSON(tx.Bucket(bucketOwner), ownerKey, &owner); err != nil {
			return err
		}
		owner.PasswordHash = encoded
		owner.SetupRequired = setupRequired
		owner.PasswordSetAt = s.now().Unix()
		if err := putJSON(tx.Bucket(bucketOwner), ownerKey, owner); err != nil {
			return err
		}
		if err := clearBucket(tx.Bucket(bucketSessions)); err != nil {
			return err
		}
		return clearBucket(tx.Bucket(bucketTokens))
	})
	if err == nil {
		clear(s.sessionTouches)
	}
	return err
}

func (s *Store) ResetLogin(params auth.PasswordParams) error {
	hash, err := auth.HashPassword(SetupPassword, params)
	if err != nil {
		return err
	}
	return s.ReplacePassword(hash, true)
}

func (s *Store) CreateSession(rawToken, csrf string, idle, absolute time.Duration) (Session, error) {
	if rawToken == "" || csrf == "" || idle <= 0 || absolute <= 0 {
		return Session{}, errors.New("invalid session parameters")
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	now := s.now()
	v := Session{ID: randomID(rawToken), CSRFToken: csrf, CreatedAt: now.Unix(), LastSeenAt: now.Unix(), IdleUntil: now.Add(idle).Unix(), AbsoluteEnd: now.Add(absolute).Unix()}
	digest := digestKey(rawToken)
	err := s.db.Update(func(tx *bolt.Tx) error { return putJSON(tx.Bucket(bucketSessions), digest, v) })
	if err == nil {
		delete(s.sessionTouches, string(digest))
	}
	return v, err
}

func (s *Store) Session(rawToken string, idle time.Duration, touch bool) (Session, error) {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()

	var stored Session
	digest := digestKey(rawToken)
	now := s.now()
	nowUnix := now.Unix()
	err := s.db.View(func(tx *bolt.Tx) error {
		return getJSON(tx.Bucket(bucketSessions), digest, &stored)
	})
	if err != nil {
		delete(s.sessionTouches, string(digest))
		return stored, err
	}
	effective := s.sessionWithPendingTouch(digest, stored)
	if !sessionExpired(effective, nowUnix) {
		if !touch {
			return effective, nil
		}
		s.recordSessionTouch(digest, stored, now, idle)
		effective = s.sessionWithPendingTouch(digest, stored)
		if !credentialTouchDue(nowUnix, stored.LastSeenAt, sessionTouchInterval(idle)) {
			return effective, nil
		}
	}

	expired := false
	persistedTouch := false
	err = s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketSessions)
		stored = Session{}
		if err := getJSON(b, digest, &stored); err != nil {
			return err
		}
		updateNow := s.now()
		updateUnix := updateNow.Unix()
		effective = s.sessionWithPendingTouch(digest, stored)
		if sessionExpired(effective, updateUnix) {
			if err := b.Delete(digest); err != nil {
				return err
			}
			expired = true
			return nil
		}
		if touch && credentialTouchDue(updateUnix, stored.LastSeenAt, sessionTouchInterval(idle)) {
			stored.LastSeenAt = updateUnix
			stored.IdleUntil = min(updateNow.Add(idle).Unix(), stored.AbsoluteEnd)
			effective = stored
			persistedTouch = true
			return putJSON(b, digest, stored)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			delete(s.sessionTouches, string(digest))
		}
		return effective, err
	}
	if expired {
		delete(s.sessionTouches, string(digest))
		return effective, ErrExpired
	}
	if persistedTouch {
		s.clearSessionTouchThrough(digest, stored, stored.LastSeenAt)
	}
	return effective, nil
}

func (s *Store) DeleteSession(rawToken string) error {
	digest := digestKey(rawToken)
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	err := s.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(bucketSessions).Delete(digest) })
	if err == nil {
		delete(s.sessionTouches, string(digest))
	}
	return err
}

func (s *Store) CreateToken(rawToken string, v APIToken) error {
	if rawToken == "" || v.ID == "" || v.Name == "" || v.ExpiresAt <= s.now().Unix() {
		return errors.New("invalid API token")
	}
	return s.db.Update(func(tx *bolt.Tx) error { return putJSON(tx.Bucket(bucketTokens), digestKey(rawToken), v) })
}

func (s *Store) Token(rawToken string, touch bool) (APIToken, error) {
	var v APIToken
	key := digestKey(rawToken)
	now := s.now().Unix()
	err := s.db.View(func(tx *bolt.Tx) error {
		return getJSON(tx.Bucket(bucketTokens), key, &v)
	})
	if err != nil {
		return v, err
	}
	if now < v.ExpiresAt && (!touch || !credentialTouchDue(now, v.LastUsedAt, credentialTouchInterval)) {
		return v, nil
	}

	expired := false
	err = s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketTokens)
		v = APIToken{}
		if err := getJSON(b, key, &v); err != nil {
			return err
		}
		updateNow := s.now().Unix()
		if updateNow >= v.ExpiresAt {
			if err := b.Delete(key); err != nil {
				return err
			}
			expired = true
			return nil
		}
		if touch && credentialTouchDue(updateNow, v.LastUsedAt, credentialTouchInterval) {
			v.LastUsedAt = updateNow
			return putJSON(b, key, v)
		}
		return nil
	})
	if err == nil && expired {
		err = ErrExpired
	}
	return v, err
}

func sessionExpired(v Session, now int64) bool {
	return now >= v.IdleUntil || now >= v.AbsoluteEnd
}

func sessionTouchInterval(idle time.Duration) time.Duration {
	interval := idle / 2
	if interval <= 0 || interval > credentialTouchInterval {
		return credentialTouchInterval
	}
	return interval
}

func (s *Store) sessionWithPendingTouch(digest []byte, stored Session) Session {
	pending, ok := s.sessionTouches[string(digest)]
	if !ok || !pending.matches(stored) || pending.lastSeenAt <= stored.LastSeenAt {
		return stored
	}
	stored.LastSeenAt = pending.lastSeenAt
	stored.IdleUntil = pending.idleUntil
	return stored
}

func (s *Store) recordSessionTouch(digest []byte, stored Session, now time.Time, idle time.Duration) {
	key := string(digest)
	if now.Unix() <= stored.LastSeenAt {
		return
	}
	pending := pendingSessionTouch{
		id:          stored.ID,
		csrfToken:   stored.CSRFToken,
		createdAt:   stored.CreatedAt,
		absoluteEnd: stored.AbsoluteEnd,
		lastSeenAt:  now.Unix(),
		idleUntil:   min(now.Add(idle).Unix(), stored.AbsoluteEnd),
	}
	if previous, ok := s.sessionTouches[key]; ok && previous.matches(stored) && previous.lastSeenAt > pending.lastSeenAt {
		return
	}
	if s.sessionTouches == nil {
		s.sessionTouches = make(map[string]pendingSessionTouch)
	}
	s.sessionTouches[key] = pending
}

func (s *Store) clearSessionTouchThrough(digest []byte, stored Session, lastSeenAt int64) {
	key := string(digest)
	pending, ok := s.sessionTouches[key]
	if ok && pending.matches(stored) && pending.lastSeenAt <= lastSeenAt {
		delete(s.sessionTouches, key)
	}
}

func (p pendingSessionTouch) matches(session Session) bool {
	return p.id == session.ID && p.csrfToken == session.CSRFToken && p.createdAt == session.CreatedAt && p.absoluteEnd == session.AbsoluteEnd
}

func credentialTouchDue(now, previous int64, interval time.Duration) bool {
	seconds := max(int64(interval/time.Second), 1)
	return previous == 0 || now >= previous && now-previous >= seconds
}

func (s *Store) Tokens() ([]APIToken, error) {
	var out []APIToken
	err := s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketTokens)
		var expired [][]byte
		if err := bucket.ForEach(func(key, value []byte) error {
			var v APIToken
			if err := json.Unmarshal(value, &v); err != nil {
				return err
			}
			if v.ExpiresAt > s.now().Unix() {
				out = append(out, v)
			} else {
				expired = append(expired, append([]byte(nil), key...))
			}
			return nil
		}); err != nil {
			return err
		}
		for _, key := range expired {
			if err := bucket.Delete(key); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

func (s *Store) DeleteToken(id string) (bool, error) {
	deleted := false
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketTokens)
		var keys [][]byte
		if err := b.ForEach(func(key, value []byte) error {
			var v APIToken
			if json.Unmarshal(value, &v) == nil && v.ID == id {
				keys = append(keys, append([]byte(nil), key...))
			}
			return nil
		}); err != nil {
			return err
		}
		for _, key := range keys {
			if err := b.Delete(key); err != nil {
				return err
			}
			deleted = true
		}
		return nil
	})
	return deleted, err
}

func (s *Store) Settings() (Settings, error) {
	var v Settings
	err := s.db.View(func(tx *bolt.Tx) error { return getJSON(tx.Bucket(bucketSettings), settingsKey, &v) })
	return v, err
}

func (s *Store) SaveSettings(v Settings) error {
	return s.db.Update(func(tx *bolt.Tx) error { return putJSON(tx.Bucket(bucketSettings), settingsKey, v) })
}

func (s *Store) CreateShare(v Share) error {
	if v.ID == "" || v.Path == "" || len(v.SecretDigest) != sha256.Size {
		return errors.New("invalid share")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		if err := putJSON(tx.Bucket(bucketShares), []byte(v.ID), v); err != nil {
			return err
		}
		return tx.Bucket(bucketShareSecrets).Put(v.SecretDigest, []byte(v.ID))
	})
}

func (s *Store) Shares() ([]Share, error) {
	var out []Share
	err := s.db.Update(func(tx *bolt.Tx) error {
		var expired []string
		if err := tx.Bucket(bucketShares).ForEach(func(_, value []byte) error {
			var v Share
			if err := json.Unmarshal(value, &v); err != nil {
				return err
			}
			if v.ExpiresAt != 0 && s.now().Unix() >= v.ExpiresAt {
				expired = append(expired, v.ID)
			} else {
				out = append(out, v)
			}
			return nil
		}); err != nil {
			return err
		}
		for _, id := range expired {
			if _, err := deleteShareTx(tx, id); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

func (s *Store) Share(id string) (Share, error) {
	var v Share
	err := s.db.Update(func(tx *bolt.Tx) error {
		if err := getJSON(tx.Bucket(bucketShares), []byte(id), &v); err != nil {
			return err
		}
		if v.ExpiresAt != 0 && s.now().Unix() >= v.ExpiresAt {
			_, _ = deleteShareTx(tx, id)
			return ErrExpired
		}
		return nil
	})
	return v, err
}

func (s *Store) ShareBySecret(secret string) (Share, error) {
	var v Share
	err := s.db.Update(func(tx *bolt.Tx) error {
		id := tx.Bucket(bucketShareSecrets).Get(digestKey(secret))
		if id == nil {
			return ErrNotFound
		}
		if err := getJSON(tx.Bucket(bucketShares), id, &v); err != nil {
			return err
		}
		if v.ExpiresAt != 0 && s.now().Unix() >= v.ExpiresAt {
			_, _ = deleteShareTx(tx, string(id))
			return ErrExpired
		}
		return nil
	})
	return v, err
}

func (s *Store) DeleteShare(id string) (bool, error) {
	deleted := false
	err := s.db.Update(func(tx *bolt.Tx) error {
		var err error
		deleted, err = deleteShareTx(tx, id)
		return err
	})
	return deleted, err
}

// DeleteSharesAtOrBelow revokes exact and descendant capabilities using path
// component boundaries (/a never matches /ab).
func (s *Store) DeleteSharesAtOrBelow(ownerPath string) (int, error) {
	deleted := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		var ids []string
		if err := tx.Bucket(bucketShares).ForEach(func(_, value []byte) error {
			var share Share
			if err := json.Unmarshal(value, &share); err != nil {
				return err
			}
			if ownerPath == "/" || share.Path == ownerPath || strings.HasPrefix(share.Path, strings.TrimSuffix(ownerPath, "/")+"/") {
				ids = append(ids, share.ID)
			}
			return nil
		}); err != nil {
			return err
		}
		for _, id := range ids {
			removed, err := deleteShareTx(tx, id)
			if err != nil {
				return err
			}
			if removed {
				deleted++
			}
		}
		return nil
	})
	return deleted, err
}

func (s *Store) CreatePublicSession(rawToken string, v PublicSession) error {
	return s.db.Update(func(tx *bolt.Tx) error { return putJSON(tx.Bucket(bucketPublicSessions), digestKey(rawToken), v) })
}

func (s *Store) PublicSession(rawToken, shareID string) (PublicSession, error) {
	var v PublicSession
	err := s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketPublicSessions)
		key := digestKey(rawToken)
		if err := getJSON(bucket, key, &v); err != nil {
			return err
		}
		if v.ShareID != shareID || s.now().Unix() >= v.ExpiresAt {
			_ = bucket.Delete(key)
			return ErrExpired
		}
		return nil
	})
	return v, err
}

func (s *Store) CreateUpload(v Upload) error {
	if v.ID == "" || v.Path == "" || v.Length < 0 || v.Offset != 0 || v.ExpiresAt <= s.now().Unix() {
		return errors.New("invalid upload")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketUploads)
		if bucket.Get([]byte(v.ID)) != nil {
			return ErrConflict
		}
		return putJSON(bucket, []byte(v.ID), v)
	})
}

func (s *Store) Upload(id string) (Upload, error) {
	var v Upload
	err := s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketUploads)
		if err := getJSON(bucket, []byte(id), &v); err != nil {
			return err
		}
		if s.now().Unix() >= v.ExpiresAt {
			_ = bucket.Delete([]byte(id))
			return ErrExpired
		}
		return nil
	})
	return v, err
}

func (s *Store) AdvanceUpload(id string, expectedOffset, newOffset int64) (Upload, error) {
	var v Upload
	err := s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketUploads)
		if err := getJSON(bucket, []byte(id), &v); err != nil {
			return err
		}
		if v.Offset != expectedOffset || newOffset < expectedOffset || newOffset > v.Length {
			return ErrConflict
		}
		v.Offset = newOffset
		return putJSON(bucket, []byte(id), v)
	})
	return v, err
}

func (s *Store) DeleteUpload(id string) (bool, error) {
	deleted := false
	err := s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketUploads)
		if bucket.Get([]byte(id)) == nil {
			return nil
		}
		deleted = true
		return bucket.Delete([]byte(id))
	})
	return deleted, err
}

func (s *Store) Uploads() ([]Upload, error) {
	var values []Upload
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketUploads).ForEach(func(_, value []byte) error {
			var upload Upload
			if err := json.Unmarshal(value, &upload); err != nil {
				return err
			}
			values = append(values, upload)
			return nil
		})
	})
	return values, err
}

// PruneExpired removes expired credentials, shares, public sessions, and
// upload metadata. Upload records are returned so their private partial files
// can be removed without ever storing an arbitrary cleanup path in bbolt.
func (s *Store) PruneExpired() ([]Upload, error) {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()

	var expiredUploads []Upload
	var expiredSessionKeys [][]byte
	type refreshedSession struct {
		key     []byte
		session Session
	}
	var refreshedSessions []refreshedSession
	err := s.db.Update(func(tx *bolt.Tx) error {
		now := s.now().Unix()
		sessions := tx.Bucket(bucketSessions)
		if err := sessions.ForEach(func(key, value []byte) error {
			var stored Session
			if json.Unmarshal(value, &stored) != nil {
				return nil
			}
			effective := s.sessionWithPendingTouch(key, stored)
			if sessionExpired(effective, now) {
				expiredSessionKeys = append(expiredSessionKeys, append([]byte(nil), key...))
			} else if sessionExpired(stored, now) {
				refreshedSessions = append(refreshedSessions, refreshedSession{key: append([]byte(nil), key...), session: effective})
			}
			return nil
		}); err != nil {
			return err
		}
		for _, key := range expiredSessionKeys {
			if err := sessions.Delete(key); err != nil {
				return err
			}
		}
		for _, refreshed := range refreshedSessions {
			if err := putJSON(sessions, refreshed.key, refreshed.session); err != nil {
				return err
			}
		}
		for _, spec := range []struct {
			bucket []byte
			expiry func([]byte) (int64, bool)
		}{
			{bucketTokens, func(value []byte) (int64, bool) {
				var v APIToken
				err := json.Unmarshal(value, &v)
				return v.ExpiresAt, err == nil
			}},
			{bucketPublicSessions, func(value []byte) (int64, bool) {
				var v PublicSession
				err := json.Unmarshal(value, &v)
				return v.ExpiresAt, err == nil
			}},
		} {
			bucket := tx.Bucket(spec.bucket)
			var keys [][]byte
			if err := bucket.ForEach(func(key, value []byte) error {
				expires, ok := spec.expiry(value)
				if ok && now >= expires {
					keys = append(keys, append([]byte(nil), key...))
				}
				return nil
			}); err != nil {
				return err
			}
			for _, key := range keys {
				if err := bucket.Delete(key); err != nil {
					return err
				}
			}
		}
		var shareIDs []string
		if err := tx.Bucket(bucketShares).ForEach(func(_, value []byte) error {
			var v Share
			if json.Unmarshal(value, &v) == nil && v.ExpiresAt != 0 && now >= v.ExpiresAt {
				shareIDs = append(shareIDs, v.ID)
			}
			return nil
		}); err != nil {
			return err
		}
		for _, id := range shareIDs {
			if _, err := deleteShareTx(tx, id); err != nil {
				return err
			}
		}
		uploads := tx.Bucket(bucketUploads)
		var uploadKeys [][]byte
		if err := uploads.ForEach(func(key, value []byte) error {
			var v Upload
			if json.Unmarshal(value, &v) == nil && now >= v.ExpiresAt {
				expiredUploads = append(expiredUploads, v)
				uploadKeys = append(uploadKeys, append([]byte(nil), key...))
			}
			return nil
		}); err != nil {
			return err
		}
		for _, key := range uploadKeys {
			if err := uploads.Delete(key); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		for _, key := range expiredSessionKeys {
			delete(s.sessionTouches, string(key))
		}
		for _, refreshed := range refreshedSessions {
			s.clearSessionTouchThrough(refreshed.key, refreshed.session, refreshed.session.LastSeenAt)
		}
	}
	return expiredUploads, err
}

var (
	ErrNotFound = errors.New("state record not found")
	ErrExpired  = errors.New("state record expired")
	ErrConflict = errors.New("state record conflict")
)

func deleteShareTx(tx *bolt.Tx, id string) (bool, error) {
	bucket := tx.Bucket(bucketShares)
	var share Share
	if err := getJSON(bucket, []byte(id), &share); err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if err := bucket.Delete([]byte(id)); err != nil {
		return false, err
	}
	if err := tx.Bucket(bucketShareSecrets).Delete(share.SecretDigest); err != nil {
		return false, err
	}
	public := tx.Bucket(bucketPublicSessions)
	var publicKeys [][]byte
	if err := public.ForEach(func(key, value []byte) error {
		var session PublicSession
		if json.Unmarshal(value, &session) == nil && session.ShareID == id {
			publicKeys = append(publicKeys, append([]byte(nil), key...))
		}
		return nil
	}); err != nil {
		return false, err
	}
	for _, key := range publicKeys {
		if err := public.Delete(key); err != nil {
			return false, err
		}
	}
	return true, nil
}

func getJSON(b *bolt.Bucket, key []byte, dst any) error {
	v := b.Get(key)
	if v == nil {
		return ErrNotFound
	}
	if err := json.Unmarshal(v, dst); err != nil {
		return fmt.Errorf("decode state: %w", err)
	}
	return nil
}

func putJSON(b *bolt.Bucket, key []byte, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return b.Put(key, data)
}

func clearBucket(b *bolt.Bucket) error {
	var keys [][]byte
	if err := b.ForEach(func(k, _ []byte) error {
		keys = append(keys, append([]byte(nil), k...))
		return nil
	}); err != nil {
		return err
	}
	for _, k := range keys {
		if err := b.Delete(k); err != nil {
			return err
		}
	}
	return nil
}

func digestKey(raw string) []byte {
	digest := sha256.Sum256([]byte(raw))
	return digest[:]
}

func randomID(raw string) string {
	digest := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", digest[:12])
}
