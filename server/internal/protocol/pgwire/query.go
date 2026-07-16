package pgwire

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"strings"

	"vaultdb/internal/core/executor"
	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
)

type preparedStmt struct {
	Name      string
	SQL       string
	ParamOIDs []uint32
	Stmt      parser.Statement
}

type portal struct {
	Name          string
	Stmt          parser.Statement
	ResultFormats []int16
}

// pgBuffer helper for constructing pgwire packets.
type pgBuffer struct {
	buf []byte
}

func (b *pgBuffer) writeByte(v byte) {
	b.buf = append(b.buf, v)
}

func (b *pgBuffer) writeInt16(v int16) {
	b.buf = append(b.buf, byte(v>>8), byte(v))
}

func (b *pgBuffer) writeInt32(v int32) {
	b.buf = append(b.buf, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func (b *pgBuffer) writeString(s string) {
	b.buf = append(b.buf, s...)
	b.buf = append(b.buf, 0)
}

func (b *pgBuffer) writeBytes(bytes []byte) {
	b.buf = append(b.buf, bytes...)
}

func sendErrorResponse(conn net.Conn, err error) error {
	buf := &pgBuffer{}
	buf.writeByte('S')
	buf.writeString("ERROR")
	buf.writeByte('C')
	buf.writeString("XX000") // Internal Error
	buf.writeByte('M')
	buf.writeString(err.Error())
	buf.writeByte(0)
	return WriteMessage(conn, 'E', buf.buf)
}

func typeToOID(typ string) uint32 {
	switch strings.ToUpper(typ) {
	case "INT", "INTEGER", "INT4", "SERIAL":
		return 23 // int4
	case "BIGINT", "INT8":
		return 20 // int8
	case "FLOAT", "DOUBLE", "FLOAT8", "REAL", "NUMERIC":
		return 701 // float8
	case "BOOL", "BOOLEAN":
		return 16 // bool
	default:
		return 25 // text
	}
}

func getTypeSize(oid int32) int16 {
	switch oid {
	case 23: // int4
		return 4
	case 20: // int8
		return 8
	case 701: // float8
		return 8
	case 16: // bool
		return 1
	default:
		return -1 // variable length (text)
	}
}

func inferExprType(expr parser.Expression) uint32 {
	if expr == nil {
		return 25
	}
	switch e := expr.(type) {
	case *parser.ParamRef:
		return 25
	case *parser.ColumnRef:
		return 25
	case *parser.Value:
		switch e.Type {
		case "int":
			return 23
		case "float":
			return 701
		case "bool":
			return 16
		default:
			return 25
		}
	case *parser.AggregateExpr:
		if strings.EqualFold(e.Name, "COUNT") {
			return 23 // int4
		}
		if strings.EqualFold(e.Name, "AVG") {
			return 701 // float8
		}
		if len(e.Args) > 0 {
			return inferExprType(e.Args[0])
		}
	case *parser.FunctionCall:
		if strings.EqualFold(e.Name, "COUNT") {
			return 23
		}
		if strings.EqualFold(e.Name, "AVG") {
			return 701
		}
		if len(e.Args) > 0 {
			return inferExprType(e.Args[0])
		}
	case *parser.WindowFunctionExpr:
		if strings.EqualFold(e.FuncName, "COUNT") {
			return 23
		}
		if strings.EqualFold(e.FuncName, "AVG") {
			return 701
		}
		if len(e.Args) > 0 {
			return inferExprType(e.Args[0])
		}
	}
	return 25
}

func resolveSelectSchema(stmt *parser.SelectStatement, sess *executor.Session) ([]string, []uint32) {
	dbName := sess.CurrentDatabase()
	if dbName == "" {
		dbName = "default"
	}
	var tableSchema *storage.TableSchema
	if stmt.TableName != "" {
		ts, err := sess.Storage().GetTableSchema(dbName, stmt.TableName)
		if err == nil {
			tableSchema = ts
		}
	}

	var colNames []string
	var colTypes []uint32

	if tableSchema != nil {
		if len(stmt.Columns) == 0 {
			for _, col := range tableSchema.Columns {
				colNames = append(colNames, col.Name)
				colTypes = append(colTypes, typeToOID(col.Type))
			}
		} else {
			for _, col := range stmt.Columns {
				name := col.Alias
				var typeOID uint32 = 25 // default text
				if ref, ok := col.Expr.(*parser.ColumnRef); ok {
					if name == "" {
						name = ref.Name
					}
					for _, tc := range tableSchema.Columns {
						if strings.EqualFold(tc.Name, ref.Name) {
							typeOID = typeToOID(tc.Type)
							break
						}
					}
				} else {
					typeOID = inferExprType(col.Expr)
				}
				if name == "" {
					name = "?column?"
				}
				colNames = append(colNames, name)
				colTypes = append(colTypes, typeOID)
			}
		}
	} else {
		for i, col := range stmt.Columns {
			name := col.Alias
			if name == "" {
				if ref, ok := col.Expr.(*parser.ColumnRef); ok {
					name = ref.Name
				} else {
					name = fmt.Sprintf("column%d", i+1)
				}
			}
			typeOID := inferExprType(col.Expr)
			colNames = append(colNames, name)
			colTypes = append(colTypes, typeOID)
		}
	}
	return colNames, colTypes
}

func (s *PGWireServer) handleConn(conn net.Conn) {
	defer conn.Close()

	params, err := HandleHandshake(conn)
	if err != nil {
		s.Logger.Error("PGWire handshake failed", "error", err)
		return
	}

	dbName := params["database"]
	user := params["user"]

	// Create Session
	session := executor.NewSession(s.Store, s.Metrics, s.TxManager, s.Broadcaster)
	defer session.Close()

	if s.Embedder != nil {
		session.SetEmbedder(s.Embedder)
	}
	if s.AuthManager != nil {
		session.SetAuthManager(s.AuthManager)
	}
	if s.WAL != nil {
		session.SetWAL(s.WAL)
	}
	if dbName != "" {
		session.SetCurrentDatabase(dbName)
	}
	if user != "" {
		session.SetUser(user)
	}

	prepared := make(map[string]preparedStmt)
	portals := make(map[string]portal)

	// Message Loop
	for {
		typeByte, msgPayload, err := ReadMessage(conn)
		if err != nil {
			break
		}

		switch typeByte {
		case 'Q': // Simple Query
			s.handleSimpleQuery(conn, session, msgPayload)
		case 'P': // Parse
			s.handleParse(conn, session, msgPayload, prepared)
		case 'B': // Bind
			s.handleBind(conn, session, msgPayload, prepared, portals)
		case 'D': // Describe
			s.handleDescribe(conn, session, msgPayload, prepared, portals)
		case 'E': // Execute
			s.handleExecute(conn, session, msgPayload, portals)
		case 'S': // Sync
			s.handleSync(conn, session)
		case 'H': // Flush
			// No-op for network flush (conn writes are unbuffered/immediate)
		case 'X': // Terminate
			return
		default:
			s.Logger.Warn("Unsupported message type received", "type", string(typeByte))
			_ = sendErrorResponse(conn, fmt.Errorf("unsupported message type: %c", typeByte))
			_ = WriteMessage(conn, 'Z', []byte{getTxStatusChar(session)})
		}
	}
}

func (s *PGWireServer) handleSimpleQuery(conn net.Conn, session *executor.Session, payload []byte) {
	if len(payload) == 0 {
		_ = WriteMessage(conn, 'Z', []byte{getTxStatusChar(session)})
		return
	}
	// Strip trailing null byte
	query := string(payload)
	if payload[len(payload)-1] == 0 {
		query = string(payload[:len(payload)-1])
	}
	query = strings.TrimSpace(query)
	if query == "" {
		_ = WriteMessage(conn, 'Z', []byte{getTxStatusChar(session)})
		return
	}

	stmt, err := parser.Parse(query)
	if err != nil {
		_ = sendErrorResponse(conn, err)
		_ = WriteMessage(conn, 'Z', []byte{getTxStatusChar(session)})
		return
	}

	result, err := session.Execute(stmt)
	if err != nil {
		_ = sendErrorResponse(conn, err)
		_ = WriteMessage(conn, 'Z', []byte{getTxStatusChar(session)})
		return
	}

	// 1. Send RowDescription if returning columns
	if len(result.Columns) > 0 {
		descBuf := &pgBuffer{}
		descBuf.writeInt16(int16(len(result.Columns)))

		// Find static types for SelectStatement if schema is available
		var colTypes []uint32
		if selStmt, ok := stmt.(*parser.SelectStatement); ok {
			_, colTypes = resolveSelectSchema(selStmt, session)
		} else if result.Schema != nil {
			for _, col := range result.Schema.Columns {
				colTypes = append(colTypes, typeToOID(col.Type))
			}
		}

		for i, colName := range result.Columns {
			descBuf.writeString(colName)
			descBuf.writeInt32(0) // Table OID
			descBuf.writeInt16(0) // Column attribute
			var o uint32 = 25     // default text
			if i < len(colTypes) {
				o = colTypes[i]
			}
			descBuf.writeInt32(int32(o))
			descBuf.writeInt16(getTypeSize(int32(o)))
			descBuf.writeInt32(-1) // Type modifier
			descBuf.writeInt16(0)  // Format code (0 = text)
		}
		_ = WriteMessage(conn, 'T', descBuf.buf)

		// 2. Send DataRow for each row
		for _, row := range result.Rows {
			rowBuf := &pgBuffer{}
			rowBuf.writeInt16(int16(len(row)))
			for _, val := range row {
				rowBuf.writeInt32(int32(len(val)))
				rowBuf.writeBytes([]byte(val))
			}
			_ = WriteMessage(conn, 'D', rowBuf.buf)
		}
	}

	// 3. Send CommandComplete
	tag := getCommandTag(stmt, result)
	_ = WriteMessage(conn, 'C', []byte(tag+"\x00"))

	// 4. Send ReadyForQuery
	_ = WriteMessage(conn, 'Z', []byte{getTxStatusChar(session)})
}

func getTxStatusChar(session *executor.Session) byte {
	if session.IsInTx() {
		return 'T'
	}
	return 'I'
}

func readInt16(r *bytes.Reader) int16 {
	var v int16
	_ = binary.Read(r, binary.BigEndian, &v)
	return v
}

func readInt32(r *bytes.Reader) int32 {
	var v int32
	_ = binary.Read(r, binary.BigEndian, &v)
	return v
}

func readString(r *bytes.Reader) string {
	var buf []byte
	for {
		b, err := r.ReadByte()
		if err != nil || b == 0 {
			break
		}
		buf = append(buf, b)
	}
	return string(buf)
}
