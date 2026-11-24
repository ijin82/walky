package store

import "testing"

func FuzzDecodeManifest(f *testing.F) {
	seed, err := EncodeManifest(&Manifest{Entries: []Entry{
		{Path: "dir", Type: TypeDir, Mode: 0o755},
		{Path: "dir/file", Type: TypeFile, Size: 3, Mode: 0o644, Hash: "00"},
	}})
	if err == nil {
		f.Add(seed)
	}
	f.Add([]byte{})
	f.Add([]byte{0x28, 0xb5, 0x2f, 0xfd})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = DecodeManifest(data)
	})
}

func FuzzParsePackIndex(f *testing.F) {
	seed, err := encodePackIndex([]packEntry{{
		hash: [32]byte{1}, off: 0, compLen: 1, rawLen: 1,
	}})
	if err == nil {
		f.Add(seed)
	}
	f.Add([]byte("WPK1"))
	f.Add([]byte("WPK1\x00\x00\x00\x00"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = parsePackIndex(data)
	})
}
