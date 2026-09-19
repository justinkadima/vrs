// Package snap captures the working tree into snapshots and materializes
// snapshots back onto disk. M0 implements capture; content is stored as
// content-addressed, CDC-chunked, zstd-compressed blobs.
package snap

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strconv"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/chunker"
)

// chunking parameters: ~64 KiB average chunk, 8 KiB floor, 512 KiB ceiling.
const (
	avgBits    = 16 // 2^16 = 64 KiB
	minChunk   = 8 * 1024
	maxChunk   = 512 * 1024
	chunkBufSz = 1 << 20 // must be >= maxChunk
)

var (
	zEnc, _  = zstd.NewWriter(nil)
	chunkBuf = make([]byte, chunkBufSz)
)

// ChunkData is one content-addressed chunk, zstd-compressed, ready for storage.
type ChunkData struct {
	Hash    string // sha256 hex of the uncompressed chunk
	Data    []byte // compressed bytes
	RawSize int64  // uncompressed size
}

// RandomPolynomialHex returns a new chunker polynomial as hex for storage.
func RandomPolynomialHex() (string, error) {
	p, err := chunker.RandomPolynomial()
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(uint64(p), 16), nil
}

// ParsePolynomial reconstructs a chunker polynomial from its hex form.
func ParsePolynomial(s string) (chunker.Pol, error) {
	u, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0, err
	}
	return chunker.Pol(u), nil
}

// hashReader chunks r (CDC) while hashing the whole stream.
func hashChunks(r io.Reader, pol chunker.Pol) (string, []ChunkData, error) {
	fileHash := sha256.New()
	ch := chunker.New(io.TeeReader(r, fileHash), pol,
		chunker.WithAverageBits(avgBits),
		chunker.WithBoundaries(minChunk, maxChunk),
	)
	var chunks []ChunkData
	for {
		c, err := ch.Next(chunkBuf)
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, err
		}
		sum := sha256.Sum256(c.Data)
		chunks = append(chunks, ChunkData{
			Hash:    hex.EncodeToString(sum[:]),
			Data:    zEnc.EncodeAll(c.Data, nil),
			RawSize: int64(len(c.Data)),
		})
	}
	return hex.EncodeToString(fileHash.Sum(nil)), chunks, nil
}

// HashFile returns the file's sha256 and its ordered chunks.
func HashFile(f *os.File, pol chunker.Pol) (string, []ChunkData, error) {
	return hashChunks(f, pol)
}

// HashBytes is HashFile for in-memory content (used by tests and materialize paths).
func HashBytes(b []byte, pol chunker.Pol) (string, []ChunkData, error) {
	return hashChunks(bytesReader(b), pol)
}
