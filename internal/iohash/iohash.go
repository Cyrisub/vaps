package iohash

import (
	"encoding/hex"
	"hash"

	"github.com/zeebo/blake3"
)

const (
	Size    = 20
	HexSize = Size * 2
)

func New() hash.Hash {
	return blake3.New()
}

func DigestHex(hasher hash.Hash) string {
	sum := hasher.Sum(nil)
	return hex.EncodeToString(sum[:Size])
}

func SumHex(payload []byte) string {
	sum := blake3.Sum256(payload)
	return hex.EncodeToString(sum[:Size])
}

func Valid(value string) bool {
	if len(value) != HexSize {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
