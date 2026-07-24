package raft

import (
	"hash/crc32"
	"io"
)

// SnapshotChunk represents a piece of a Raft binary snapshot.
type SnapshotChunk struct {
	Offset int64
	Data   []byte
	CRC32  uint32
}

// SnapshotStreamer streams binary snapshots in chunks.
type SnapshotStreamer struct {
	reader io.Reader
	offset int64
}

func NewSnapshotStreamer(r io.Reader) *SnapshotStreamer {
	return &SnapshotStreamer{
		reader: r,
		offset: 0,
	}
}

// NextChunk reads the next chunk from the snapshot stream and calculates its CRC32.
func (s *SnapshotStreamer) NextChunk(chunkSize int) (*SnapshotChunk, error) {
	buf := make([]byte, chunkSize)
	n, err := s.reader.Read(buf)
	if n > 0 {
		data := buf[:n]
		checksum := crc32.ChecksumIEEE(data)
		chunk := &SnapshotChunk{
			Offset: s.offset,
			Data:   data,
			CRC32:  checksum,
		}
		s.offset += int64(n)

		// If we read >0 bytes and hit EOF, return the chunk with nil error
		// The next call will return 0 bytes and EOF.
		if err == io.EOF {
			return chunk, nil
		}
		return chunk, err
	}

	if err != nil {
		return nil, err
	}
	return nil, io.EOF
}
