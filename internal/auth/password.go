// Package auth contains password and opaque-token primitives used by ZenFM.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// PasswordParams are deliberately explicit so password hashes are self
// describing and can be upgraded without a database migration.
type PasswordParams struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultPasswordParams follow OWASP's Argon2id baseline while remaining
// practical on older e-readers.
var DefaultPasswordParams = PasswordParams{
	Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32,
}

func HashPassword(password string, p PasswordParams) (string, error) {
	if password == "" {
		return "", errors.New("password is empty")
	}
	if p.Memory < 8*1024 || p.Iterations == 0 || p.Parallelism == 0 || p.SaltLength < 16 || p.KeyLength < 16 {
		return "", errors.New("unsafe Argon2id parameters")
	}
	salt := make([]byte, p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", p.Memory, p.Iterations, p.Parallelism, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	var p PasswordParams
	for _, setting := range strings.Split(parts[3], ",") {
		kv := strings.SplitN(setting, "=", 2)
		if len(kv) != 2 {
			return false
		}
		n, err := strconv.ParseUint(kv[1], 10, 32)
		if err != nil {
			return false
		}
		switch kv[0] {
		case "m":
			p.Memory = uint32(n)
		case "t":
			p.Iterations = uint32(n)
		case "p":
			p.Parallelism = uint8(n)
		default:
			return false
		}
	}
	// Refuse attacker-controlled hashes that could exhaust memory or CPU.
	if p.Memory < 8*1024 || p.Memory > 256*1024 || p.Iterations == 0 || p.Iterations > 10 || p.Parallelism == 0 || p.Parallelism > 8 {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 64 {
		return false
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) < 16 || len(want) > 64 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// RandomToken returns a URL-safe token containing bits of entropy.
func RandomToken(prefix string, bits int) (string, error) {
	if bits < 128 || bits%8 != 0 {
		return "", errors.New("token entropy must be a multiple of 8 and at least 128 bits")
	}
	b := make([]byte, bits/8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func TokenDigest(token string) [32]byte { return sha256.Sum256([]byte(token)) }
