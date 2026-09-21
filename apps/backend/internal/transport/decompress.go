// Package transport carries the wire-level half of the ingestion contract (project.md §17,
// §19).
//
// One thing lives here today: request decompression. §17 asks SDKs for "zstd where
// supported, gzip fallback" and §19 step 3 lists `decompress` among the cheap local
// operations the gateway performs before anything else — and until now nothing did, so a
// compressed body was a parse failure and every SDK had to send JSON in the clear.
//
// # Why this is worth a package
//
// Measured on the real §17 envelope with `cmd/wiresize`: a 500-event Unity batch is 290
// bytes an event as JSON and 31.6 compressed with zstd — a 9.2× reduction, or 8.0× with
// gzip. At §4's ceiling of a billion events a day that is 290 GB of ingress a day against
// 32. The bandwidth bill is the smaller half of the argument; the larger half is that most
// of those bytes are paid for by a player's mobile data plan.
//
// # The attack this opens, and the shape of the defence
//
// Accepting compressed input means accepting a small request that expands into a large one.
// A few hundred kilobytes of zstd can decompress to gigabytes, and a server that reads it
// into memory before noticing is a server one request can take down.
//
// The defence is not a ratio heuristic, which is guesswork about what a legitimate client
// compresses like. It is the pair of limits §19.1 already names, applied at the only two
// places they can be applied honestly:
//
//   - **maximum compressed request bytes**, enforced here before a single byte is
//     decompressed;
//   - **maximum decompressed bytes**, which is the route's own body limit — the framework
//     applies it while binding, to whatever this package leaves in the body. So a
//     compressed batch and an uncompressed one are held to the same size, and the bomb
//     stops being read at the limit rather than being read and then measured.
//
// That second point is why this is a middleware rather than a step inside the handler: by
// the time a handler runs the body is already decoded, and a limit applied after the fact
// is not a limit.
package transport

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
	muzak "muzak.dev/framework"
)

// Encodings this gateway accepts, and no others.
//
// A closed set on purpose. `deflate` is the raw-DEFLATE/zlib ambiguity that has produced a
// decade of interoperability bugs, `br` needs a dependency for a format no game SDK ships,
// and an encoding nobody sends is an attack surface nobody tests.
const (
	EncodingGzip = "gzip"
	EncodingZstd = "zstd"
)

// DefaultMaxCompressedBytes is the wire cap for a compressed body.
//
// Derived rather than picked. A batch is at most 1000 events (§19.1) and about 290 bytes an
// event of JSON, so the largest legitimate batch is a little under 300 KB uncompressed and
// roughly 32 KB compressed. A megabyte is thirty times that headroom, and still four times
// smaller than the uncompressed body limit — which is the point: compression is for sending
// the same batch in fewer bytes, not a bigger one.
const DefaultMaxCompressedBytes int64 = 1 << 20

// Options configures the middleware.
type Options struct {
	// MaxCompressedBytes bounds the body on the wire when it carries a Content-Encoding.
	// Zero takes DefaultMaxCompressedBytes.
	MaxCompressedBytes int64
}

// Decompress returns middleware that decodes a compressed request body.
//
// A request with no `Content-Encoding`, or with `identity`, passes through untouched, so
// this costs a header lookup on the path that does not use it.
func Decompress(opts Options) muzak.Middleware {
	maxCompressed := opts.MaxCompressedBytes
	if maxCompressed <= 0 {
		maxCompressed = DefaultMaxCompressedBytes
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			encoding, ok := requestEncoding(r)
			if !ok {
				// A list of encodings — `gzip, gzip` — is legal HTTP and is nothing any
				// SDK should send. Refused rather than unwrapped in a loop, because a
				// loop is how one request becomes several bombs.
				writeError(w, r, http.StatusUnsupportedMediaType,
					"content_encoding_unsupported",
					"this endpoint accepts a single Content-Encoding of gzip or zstd")
				return
			}
			if encoding == "" {
				next.ServeHTTP(w, r)
				return
			}

			// Refused before anything is read, when the client declared a length. A
			// chunked request has no length to check and falls through to the reader
			// below, which stops at the same bound.
			if r.ContentLength > maxCompressed {
				writeError(w, r, http.StatusRequestEntityTooLarge, "batch_too_large",
					fmt.Sprintf("a compressed batch may be at most %d bytes on the wire", maxCompressed))
				return
			}

			original := r.Body
			compressed := http.MaxBytesReader(w, original, maxCompressed)

			decoded, release, err := decoder(encoding, compressed)
			if err != nil {
				// A header that says gzip over bytes that are not gzip fails here, before
				// the handler, and is a 400 rather than a 500: the request is malformed.
				writeError(w, r, http.StatusBadRequest, "malformed_body",
					"the body is not valid "+encoding)
				return
			}
			defer release()

			// The body downstream is the decoded stream, and the route's own body limit
			// applies to it — which is §19.1's "maximum decompressed bytes", enforced by
			// the framework while binding.
			//
			// The length and the header go with it. Leaving either in place would tell
			// everything downstream that the body is still compressed and still that many
			// bytes, and both would be wrong.
			r.Body = io.NopCloser(decoded)
			r.ContentLength = -1
			r.Header.Del("Content-Encoding")

			next.ServeHTTP(w, r)
		})
	}
}

// requestEncoding reads the single encoding this request declares.
//
// Returns ok=false for anything that is not empty, identity, gzip or zstd — including a
// list, whatever it contains.
func requestEncoding(r *http.Request) (encoding string, ok bool) {
	raw := strings.TrimSpace(strings.ToLower(r.Header.Get("Content-Encoding")))
	switch raw {
	case "", "identity":
		return "", true
	case EncodingGzip, EncodingZstd:
		return raw, true
	default:
		return "", false
	}
}

// decoder wraps the compressed stream, and returns the function that puts the decoder back.
func decoder(encoding string, r io.Reader) (io.Reader, func(), error) {
	switch encoding {
	case EncodingGzip:
		zr := gzipPool.Get().(*gzip.Reader)
		if err := zr.Reset(r); err != nil {
			gzipPool.Put(zr)
			return nil, nil, err
		}
		return zr, func() { _ = zr.Close(); gzipPool.Put(zr) }, nil

	case EncodingZstd:
		zr := zstdPool.Get().(*zstd.Decoder)
		if err := zr.Reset(r); err != nil {
			zstdPool.Put(zr)
			return nil, nil, err
		}
		// Reset(nil) releases the window before the decoder goes back, so a pooled
		// decoder holds no reference to the request it just served.
		return zr, func() { _ = zr.Reset(nil); zstdPool.Put(zr) }, nil
	}
	return nil, nil, fmt.Errorf("unsupported encoding %q", encoding)
}

// Pooled decoders, because the window buffer is the expensive part.
//
// A zstd decoder allocates a window sized for the stream it is about to read, and a gzip
// reader allocates its own tables — per request, at the ingestion path's rate, that is the
// kind of allocation that shows up as garbage collection rather than as a slow endpoint.
//
// Single-threaded decoders deliberately: the concurrency here is requests, and a decoder
// that spawns goroutines of its own turns one slow client into several.
var (
	gzipPool = sync.Pool{New: func() any { return new(gzip.Reader) }}
	zstdPool = sync.Pool{New: func() any {
		d, err := zstd.NewReader(nil,
			zstd.WithDecoderConcurrency(1),
			// A ceiling on what one stream may ask the decoder to hold. The route's body
			// limit already bounds what is read; this bounds what is allocated to read
			// it, which is the half a limit on output does not cover.
			zstd.WithDecoderMaxMemory(maxDecoderMemory))
		if err != nil {
			panic("transport: zstd decoder: " + err.Error())
		}
		return d
	}}
)

// maxDecoderMemory bounds one zstd stream's window.
//
// Generous against any real batch and far below what a crafted header can ask for, which is
// the whole point: a stream declaring a gigabyte window is refused when it is declared
// rather than when it is allocated.
const maxDecoderMemory = 16 << 20

// writeError renders a refusal in the framework's own envelope.
//
// Middleware runs before the route, so the framework's error renderer is not available —
// and an SDK that had to parse a second error shape for these two cases would be an SDK
// with a second error path nobody tests. Built from the exported types instead, so it
// cannot drift from the shape every other refusal uses.
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	body := muzak.ErrorResponse{
		Error: muzak.ErrorBody{
			Code:    code,
			Message: message,
			Status:  status,
		},
		RequestID: r.Header.Get(muzak.HeaderRequestID),
	}
	payload, err := json.Marshal(body)
	if err != nil {
		http.Error(w, message, status)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}
