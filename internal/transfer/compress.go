package transfer

import (
	"sync"

	"github.com/klauspost/compress/zstd"
)

// Compression flag byte stored inside the encrypted payload.
const (
	flagRaw  = byte(0)
	flagZstd = byte(1)
)

var (
	zenc     *zstd.Encoder
	zdec     *zstd.Decoder
	zencOnce sync.Once
	zdecOnce sync.Once
)

func getEncoder() *zstd.Encoder {
	zencOnce.Do(func() {
		// SpeedFastest ≈ 500 MB/s — faster than any network link
		zenc, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest))
	})
	return zenc
}

func getDecoder() *zstd.Decoder {
	zdecOnce.Do(func() {
		zdec, _ = zstd.NewReader(nil)
	})
	return zdec
}

// tryCompress attempts zstd compression.
// Returns (compressed, true) if it saved space, or (original, false) otherwise.
// Already-compressed formats (jpg, mp4, zip) fall through unchanged.
func tryCompress(data []byte) ([]byte, bool) {
	enc := getEncoder()
	compressed := enc.EncodeAll(data, make([]byte, 0, len(data)/2))
	if len(compressed) >= len(data) {
		return data, false
	}
	return compressed, true
}

func decompressZstd(data []byte) ([]byte, error) {
	return getDecoder().DecodeAll(data, nil)
}
