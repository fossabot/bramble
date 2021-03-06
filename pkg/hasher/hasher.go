package hasher

import (
	"bytes"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"
)

var ErrHashMismatch = errors.New("two hashes don't match")

// Hasher is used to compute path hash values. Hasher implements io.Writer and
// takes a sha256 hash of the input bytes. The output string is a lowercase
// base32 representation of the first 160 bits of the hash
type Hasher struct {
	hash hash.Hash
}

func NewHasher() *Hasher {
	return &Hasher{
		hash: sha256.New(),
	}
}

func (h *Hasher) Write(b []byte) (n int, err error) {
	return h.hash.Write(b)
}

func (h *Hasher) String() string {
	return BytesToBase32Hash(h.hash.Sum(nil))
}
func (h *Hasher) Sha256Hex() string {
	return fmt.Sprintf("%x", h.hash.Sum(nil))
}

func HashString(input string) string {
	h := NewHasher()
	_, _ = h.Write([]byte(input))
	return h.String()
}

// BytesToBase32Hash copies nix here
// https://nixos.org/nixos/nix-pills/nix-store-paths.html
// Finally the comments tell us to compute the base32 representation of the
// first 160 bits (truncation) of a sha256 of the above string:
func BytesToBase32Hash(b []byte) string {
	var buf bytes.Buffer
	_, _ = base32.NewEncoder(base32.StdEncoding, &buf).Write(b[:20])
	return strings.ToLower(buf.String())
}

func HashFile(name string, file io.ReadCloser) (fileHash, filename string, err error) {
	defer file.Close()
	hasher := NewHasher()
	if _, err = hasher.Write([]byte(name)); err != nil {
		return
	}
	if _, err = io.Copy(hasher, file); err != nil {
		return
	}
	filename = fmt.Sprintf("%s-%s", hasher.String(), name)
	return
}
