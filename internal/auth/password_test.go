package auth

import (
	"strings"
	"testing"
)

func TestPasswordRoundTrip(t *testing.T) {
	p := PasswordParams{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16}
	hash, err := HashPassword("correct horse battery staple", p)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("correct password rejected")
	}
	if VerifyPassword(hash, "wrong") || VerifyPassword("garbage", "wrong") {
		t.Fatal("invalid password accepted")
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("unexpected encoding: %q", hash)
	}
}

func TestVerifyPasswordBoundsUntrustedParameters(t *testing.T) {
	bad := "$argon2id$v=19$m=4294967295,t=1,p=1$MTIzNDU2Nzg5MDEyMzQ1Ng$MTIzNDU2Nzg5MDEyMzQ1Ng"
	if VerifyPassword(bad, "anything") {
		t.Fatal("malicious hash accepted")
	}
}

func TestRandomToken(t *testing.T) {
	a, err := RandomToken("zfm_", 256)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := RandomToken("zfm_", 256)
	if a == b || !strings.HasPrefix(a, "zfm_") || TokenDigest(a) == TokenDigest(b) {
		t.Fatal("tokens are not distinct")
	}
}
