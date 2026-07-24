package toast

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
)

const (
	ToastChunkSize = 2000
	ToastThreshold = 2048 // If attribute > 2KB, we TOAST it
)

// ToastPointer represents a reference to a TOASTed value.
type ToastPointer struct {
	ChunkID          uint64
	TotalSize        uint32
	ChunkCount       uint32
	UncompressedSize uint32
}

func (tp *ToastPointer) Encode() []byte {
	buf := make([]byte, 20)
	binary.LittleEndian.PutUint64(buf[0:8], tp.ChunkID)
	binary.LittleEndian.PutUint32(buf[8:12], tp.TotalSize)
	binary.LittleEndian.PutUint32(buf[12:16], tp.ChunkCount)
	binary.LittleEndian.PutUint32(buf[16:20], tp.UncompressedSize)
	return buf
}

func DecodeToastPointer(data []byte) (*ToastPointer, error) {
	if len(data) < 20 {
		return nil, fmt.Errorf("invalid toast pointer length")
	}
	return &ToastPointer{
		ChunkID:          binary.LittleEndian.Uint64(data[0:8]),
		TotalSize:        binary.LittleEndian.Uint32(data[8:12]),
		ChunkCount:       binary.LittleEndian.Uint32(data[12:16]),
		UncompressedSize: binary.LittleEndian.Uint32(data[16:20]),
	}, nil
}

// Compress data using gzip
func compressGzip(data []byte) ([]byte, error) {
	buf := new(bytes.Buffer)
	zw := gzip.NewWriter(buf)
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Decompress data using gzip
func decompressGzip(data []byte) ([]byte, error) {
	buf := bytes.NewReader(data)
	zr, err := gzip.NewReader(buf)
	if err != nil {
		return nil, err
	}
	out := new(bytes.Buffer)
	if _, err := io.CopyN(out, zr, 1<<30); err != nil {
		return nil, err
	}
	if err := zr.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// ChunkToastedValue takes a large value, compresses it, and chunks it.
func ChunkToastedValue(data []byte) ([][]byte, uint32, error) {
	compressed, err := compressGzip(data)
	if err != nil {
		return nil, 0, err
	}

	totalSize := uint32(len(compressed))
	chunkCount := (totalSize + ToastChunkSize - 1) / ToastChunkSize

	chunks := make([][]byte, chunkCount)
	for i := uint32(0); i < chunkCount; i++ {
		start := i * ToastChunkSize
		end := start + ToastChunkSize
		if end > totalSize {
			end = totalSize
		}
		chunks[i] = compressed[start:end]
	}

	return chunks, totalSize, nil
}

// AssembleToastedValue takes chunks and decompresses them.
func AssembleToastedValue(chunks [][]byte) ([]byte, error) {
	compressed := new(bytes.Buffer)
	for _, chunk := range chunks {
		compressed.Write(chunk)
	}
	return decompressGzip(compressed.Bytes())
}
