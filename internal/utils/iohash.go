package utils

import (
	"encoding/hex"
	"hash"

	"github.com/zeebo/blake3"
)

const (
	IoHashSize    = 20
	IoHashHexSize = IoHashSize * 2
)

func NewIoHash() hash.Hash {
	return blake3.New()
}

func IoHashSumHex(payload []byte) string {
	sum := blake3.Sum256(payload)
	return hex.EncodeToString(sum[:IoHashSize])
}

func IoHashDigestHex(hasher hash.Hash) string {
	sum := hasher.Sum(nil)
	return hex.EncodeToString(sum[:IoHashSize])
}

// Blake3DigestHex returns the full BLAKE3-256 digest. FIoHash is intentionally
// truncated for Unreal compatibility; callers that establish persistence use
// this full digest to detect the otherwise theoretical FIoHash collision.
func Blake3DigestHex(hasher hash.Hash) string {
	return hex.EncodeToString(hasher.Sum(nil))
}

func IoHashValid(value string) bool {
	if len(value) != IoHashHexSize {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
