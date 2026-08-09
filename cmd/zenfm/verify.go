package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"io"
	"os"
	"strings"
)

const maxManifestBytes = 1 << 20

func runVerifyManifest(args []string) error {
	flags := flag.NewFlagSet("verify-manifest", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	publicKeyHex := flags.String("public-key", "", "Ed25519 public key as hex")
	manifestPath := flags.String("manifest", "", "signed manifest path")
	signaturePath := flags.String("signature", "", "base64 signature path")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid arguments")
	}
	publicKey, err := hex.DecodeString(*publicKeyHex)
	if err != nil || len(publicKey) != ed25519.PublicKeySize || len(*publicKeyHex) != ed25519.PublicKeySize*2 {
		return errors.New("invalid public key")
	}
	manifest, err := readRegularBounded(*manifestPath, maxManifestBytes)
	if err != nil {
		return err
	}
	encodedSignature, err := readRegularBounded(*signaturePath, 256)
	if err != nil {
		return err
	}
	encoded := strings.TrimSpace(string(encodedSignature))
	if encoded == "" || strings.ContainsAny(encoded, " \t\r\n") {
		return errors.New("invalid signature encoding")
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		signature, err = base64.RawStdEncoding.Strict().DecodeString(encoded)
	}
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(publicKey), manifest, signature) {
		return errors.New("signature verification failed")
	}
	return nil
}

func readRegularBounded(name string, maximum int64) ([]byte, error) {
	if name == "" {
		return nil, errors.New("path is empty")
	}
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maximum {
		return nil, errors.New("input is not a bounded regular file")
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(data)) > maximum {
		return nil, errors.New("input exceeds limit")
	}
	return data, nil
}
