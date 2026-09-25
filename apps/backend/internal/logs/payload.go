package logs

import (
	"encoding/json"
	"fmt"

	"github.com/klauspost/compress/zstd"

	"playersense.dev/backend/internal/ingest"
)

// A log batch travels the queue compressed.
//
// The queue bills by the byte published (Pub/Sub's $40 a TiB, with a kilobyte minimum a message),
// and at log volumes that is not a rounding error: uncompressed, a gigabyte of logs costs about
// $0.04 to queue, twice what it costs to keep a month on object storage. Log text compresses five
// to ten times, so zstd here is most of the queue's bill gone for a few microseconds of CPU on
// each side. The analytics topic is untouched: its payload is small and its consumers read it raw.

var (
	encoder, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault), zstd.WithEncoderConcurrency(1))
	decoder, _ = zstd.NewReader(nil, zstd.WithDecoderConcurrency(0), zstd.WithDecoderMaxMemory(64<<20))
)

// zstdMagic opens every zstd frame. A payload without it is plain JSON, from before compression.
var zstdMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}

// EncodeBatch is a normalized batch as it is published.
func EncodeBatch(batch *ingest.NormalizedLogBatch) ([]byte, error) {
	raw, err := json.Marshal(batch)
	if err != nil {
		return nil, err
	}
	return encoder.EncodeAll(raw, make([]byte, 0, len(raw)/4)), nil
}

// DecodeBatch reads a published batch back.
func DecodeBatch(payload []byte) (*ingest.NormalizedLogBatch, error) {
	raw := payload
	if len(payload) >= 4 && string(payload[:4]) == string(zstdMagic) {
		var err error
		if raw, err = decoder.DecodeAll(payload, nil); err != nil {
			return nil, fmt.Errorf("decompress: %w", err)
		}
	}
	var batch ingest.NormalizedLogBatch
	if err := json.Unmarshal(raw, &batch); err != nil {
		return nil, err
	}
	return &batch, nil
}
