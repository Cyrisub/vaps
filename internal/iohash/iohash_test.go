package iohash

import "testing"

func TestSumHexUsesBLAKE3Leading160Bits(t *testing.T) {
	got := SumHex([]byte("hello"))
	want := "ea8f163db38682925e4491c5e58d4bb3506ef8c1"
	if got != want {
		t.Fatalf("SumHex = %q, want %q", got, want)
	}
}

func TestValidAcceptsFortyHexCharacters(t *testing.T) {
	if !Valid("ea8f163db38682925e4491c5e58d4bb3506ef8c1") {
		t.Fatalf("Valid returned false for BLAKE3-160 hex")
	}
	if Valid("ea8f163db38682925e4491c5e58d4bb3506ef8c") {
		t.Fatalf("Valid returned true for short hash")
	}
}
