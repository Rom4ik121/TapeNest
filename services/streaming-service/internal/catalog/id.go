package catalog

import (
	"encoding/binary"
	"fmt"
)

// UUIDFromSeed matches apps/cinenest/src/mocks/uuid.ts so seeded rows keep the
// ids the mock catalog used. It is not a random UUID.
func UUIDFromSeed(seed string) string {
	var parts [4]uint32
	for i := range parts {
		parts[i] = fnv1a(seed, uint32(i)*0x9e3779b1)
	}
	var b [16]byte
	for i, p := range parts {
		binary.BigEndian.PutUint32(b[i*4:], p)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func fnv1a(input string, seed uint32) uint32 {
	h := uint32(0x811c9dc5) ^ seed
	for _, c := range input {
		h ^= uint32(c) //nolint:gosec // G115: JS charCode mix, low 32 bits
		h *= 0x01000193
	}
	return h
}
