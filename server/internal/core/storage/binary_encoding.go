package storage

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
)

// Binary tuple format:
// [0:8]   createdTx uint64 LE
// [8:16]  deletedTx uint64 LE
// [16:18] colCount uint16 LE
// [18:18+2*N] colOffsets uint16 LE (start offset of each column value)
// [18+2*N:] column values (raw bytes)

const (
	binTupleHeaderSize = 16 // createdTx + deletedTx
	binColCountSize    = 2
	binColOffsetSize   = 2
	binNullMarker      = 0xFF
	binTrue            = 0x01
	binFalse           = 0x00
)

func encodeBinaryTuple(createdTx, deletedTx uint64, row Row) ([]byte, error) {
	if len(row) == 0 {
		return nil, fmt.Errorf("encodeBinaryTuple: empty row")
	}

	// Phase 1: encode each column value into temporary buffers
	colBuffers := make([][]byte, len(row))
	for i, val := range row {
		buf, err := encodeColumnValue(val)
		if err != nil {
			return nil, fmt.Errorf("column %d: %w", i, err)
		}
		colBuffers[i] = buf
	}

	// Phase 2: build the tuple
	headerSize := binTupleHeaderSize + binColCountSize + len(row)*binColOffsetSize
	totalSize := headerSize
	for _, buf := range colBuffers {
		totalSize += len(buf)
	}

	if totalSize > 65535 {
		return nil, fmt.Errorf("tuple too large: %d bytes", totalSize)
	}

	tuple := make([]byte, totalSize)
	binary.LittleEndian.PutUint64(tuple[0:8], createdTx)
	binary.LittleEndian.PutUint64(tuple[8:16], deletedTx)
	binary.LittleEndian.PutUint16(tuple[16:18], uint16(len(row)))

	offset := uint16(headerSize)
	for i, buf := range colBuffers {
		offIdx := binTupleHeaderSize + binColCountSize + i*binColOffsetSize
		binary.LittleEndian.PutUint16(tuple[offIdx:offIdx+binColOffsetSize], offset)
		copy(tuple[offset:], buf)
		offset += uint16(len(buf))
	}

	return tuple, nil
}

func decodeBinaryTuple(tuple []byte, schema *TableSchema) (createdTx, deletedTx uint64, row Row, err error) {
	if len(tuple) < binTupleHeaderSize+binColCountSize {
		return 0, 0, nil, fmt.Errorf("tuple too short")
	}

	createdTx = binary.LittleEndian.Uint64(tuple[0:8])
	deletedTx = binary.LittleEndian.Uint64(tuple[8:16])
	colCount := binary.LittleEndian.Uint16(tuple[16:18])

	headerSize := binTupleHeaderSize + binColCountSize + int(colCount)*binColOffsetSize
	if len(tuple) < headerSize {
		return 0, 0, nil, fmt.Errorf("tuple header truncated")
	}

	row = make(Row, colCount)
	for i := uint16(0); i < colCount; i++ {
		offIdx := binTupleHeaderSize + binColCountSize + int(i)*binColOffsetSize
		valOffset := binary.LittleEndian.Uint16(tuple[offIdx : offIdx+binColOffsetSize])

		var nextOffset uint16
		if int(i+1) < len(row) {
			nextOffIdx := binTupleHeaderSize + binColCountSize + int(i+1)*binColOffsetSize
			nextOffset = binary.LittleEndian.Uint16(tuple[nextOffIdx : nextOffIdx+binColOffsetSize])
		} else {
			nextOffset = uint16(len(tuple))
		}

		valBytes := tuple[valOffset:nextOffset]
		val, err := decodeColumnValue(valBytes)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("column %d: %w", i, err)
		}
		row[i] = val
	}

	return createdTx, deletedTx, row, nil
}

// encodeColumnValue encodes a single value into bytes.
func encodeColumnValue(val interface{}) ([]byte, error) {
	if val == nil {
		return []byte{binNullMarker}, nil
	}

	switch v := val.(type) {
	case int64:
		buf := make([]byte, 9) // type tag + 8 bytes
		buf[0] = 'i'
		binary.LittleEndian.PutUint64(buf[1:9], uint64(v))
		return buf, nil

	case float64:
		buf := make([]byte, 9) // type tag + 8 bytes
		buf[0] = 'f'
		binary.LittleEndian.PutUint64(buf[1:9], math.Float64bits(v))
		return buf, nil

	case bool:
		buf := make([]byte, 2) // type tag + 1 byte
		buf[0] = 'b'
		if v {
			buf[1] = binTrue
		} else {
			buf[1] = binFalse
		}
		return buf, nil

	case string:
		// Format: type tag + 2B length + UTF-8 bytes
		strBytes := []byte(v)
		if len(strBytes) > 65535 {
			return nil, fmt.Errorf("string too long for binary encoding: %d bytes (max 65535)", len(strBytes))
		}
		buf := make([]byte, 3+len(strBytes))
		buf[0] = 's'
		binary.LittleEndian.PutUint16(buf[1:3], uint16(len(strBytes)))
		copy(buf[3:], strBytes)
		return buf, nil

	case map[string]interface{}:
		// JSONB: encode as JSON string with 'j' tag
		jsonBytes, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		if len(jsonBytes) > 65535 {
			return nil, fmt.Errorf("JSONB too long for binary encoding: %d bytes (max 65535)", len(jsonBytes))
		}
		buf := make([]byte, 3+len(jsonBytes))
		buf[0] = 'j'
		binary.LittleEndian.PutUint16(buf[1:3], uint16(len(jsonBytes)))
		copy(buf[3:], jsonBytes)
		return buf, nil

	case []float64:
		// Format: type tag + 2B count + N*8B floats
		buf := make([]byte, 3+len(v)*8)
		buf[0] = 'v'
		binary.LittleEndian.PutUint16(buf[1:3], uint16(len(v)))
		for i, f := range v {
			binary.LittleEndian.PutUint64(buf[3+i*8:3+i*8+8], math.Float64bits(f))
		}
		return buf, nil

	default:
		return nil, fmt.Errorf("unsupported value type %T for binary encoding", v)
	}
}

// decodeColumnValue decodes bytes into a value.
func decodeColumnValue(data []byte) (interface{}, error) {
	if len(data) == 0 {
		return nil, nil
	}

	tag := data[0]
	switch tag {
	case binNullMarker:
		return nil, nil

	case 'i': // int64
		if len(data) < 9 {
			return nil, fmt.Errorf("int64 truncated")
		}
		return int64(binary.LittleEndian.Uint64(data[1:9])), nil

	case 'f': // float64
		if len(data) < 9 {
			return nil, fmt.Errorf("float64 truncated")
		}
		return math.Float64frombits(binary.LittleEndian.Uint64(data[1:9])), nil

	case 'b': // bool
		if len(data) < 2 {
			return nil, fmt.Errorf("bool truncated")
		}
		return data[1] == binTrue, nil

	case 's': // string
		if len(data) < 3 {
			return nil, fmt.Errorf("string header truncated")
		}
		strLen := binary.LittleEndian.Uint16(data[1:3])
		if len(data) < 3+int(strLen) {
			return nil, fmt.Errorf("string data truncated")
		}
		return string(data[3 : 3+int(strLen)]), nil

	case 'j': // JSONB
		if len(data) < 3 {
			return nil, fmt.Errorf("jsonb header truncated")
		}
		jsonLen := binary.LittleEndian.Uint16(data[1:3])
		if len(data) < 3+int(jsonLen) {
			return nil, fmt.Errorf("jsonb data truncated")
		}
		jsonStr := string(data[3 : 3+int(jsonLen)])
		raw, err := DecodeJSON([]byte(jsonStr))
		if err != nil {
			// Not valid JSON — return as string
			return jsonStr, nil
		}
		m, ok := raw.(map[string]interface{})
		if !ok {
			return jsonStr, nil
		}
		return m, nil

	case 'v': // vector
		if len(data) < 3 {
			return nil, fmt.Errorf("vector header truncated")
		}
		count := binary.LittleEndian.Uint16(data[1:3])
		if len(data) < 3+int(count)*8 {
			return nil, fmt.Errorf("vector data truncated")
		}
		vec := make([]float64, count)
		for i := uint16(0); i < count; i++ {
			vec[i] = math.Float64frombits(binary.LittleEndian.Uint64(data[3+int(i)*8 : 3+int(i)*8+8]))
		}
		return vec, nil

	default:
		return nil, fmt.Errorf("unknown type tag: %c", tag)
	}
}
