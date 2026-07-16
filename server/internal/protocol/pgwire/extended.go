package pgwire

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"strconv"
	"strings"

	"vaultdb/internal/core/executor"
	"vaultdb/internal/core/parser"
)

func (s *PGWireServer) handleParse(conn net.Conn, session *executor.Session, payload []byte, prepared map[string]preparedStmt) {
	r := bytes.NewReader(payload)
	stmtName := readString(r)
	query := readString(r)
	numOIDs := readInt16(r)
	paramOIDs := make([]uint32, numOIDs)
	for i := 0; i < int(numOIDs); i++ {
		paramOIDs[i] = uint32(readInt32(r))
	}

	stmt, err := parser.Parse(query)
	if err != nil {
		_ = sendErrorResponse(conn, err)
		return
	}

	// Count parameters dynamically from AST and expand paramOIDs if needed
	numParams := countParams(stmt)
	if len(paramOIDs) < numParams {
		extended := make([]uint32, numParams)
		copy(extended, paramOIDs)
		paramOIDs = extended
	}

	prepared[stmtName] = preparedStmt{
		Name:      stmtName,
		SQL:       query,
		ParamOIDs: paramOIDs,
		Stmt:      stmt,
	}

	_ = WriteMessage(conn, '1', nil) // ParseComplete
}

func (s *PGWireServer) handleBind(conn net.Conn, session *executor.Session, payload []byte, prepared map[string]preparedStmt, portals map[string]portal) {
	r := bytes.NewReader(payload)
	destPortal := readString(r)
	sourcePrep := readString(r)

	numFormatCodes := readInt16(r)
	formatCodes := make([]int16, numFormatCodes)
	for i := 0; i < int(numFormatCodes); i++ {
		formatCodes[i] = readInt16(r)
	}

	numValues := readInt16(r)
	paramVals := make([]parser.Value, numValues)
	prep, exists := prepared[sourcePrep]

	for i := 0; i < int(numValues); i++ {
		length := readInt32(r)
		if length == -1 {
			paramVals[i] = parser.Value{Type: "null"}
			continue
		}
		valBytes := make([]byte, length)
		if _, err := io.ReadFull(r, valBytes); err != nil {
			_ = sendErrorResponse(conn, fmt.Errorf("failed to read parameter value: %w", err))
			return
		}

		var format int16 = 0
		if len(formatCodes) == 1 {
			format = formatCodes[0]
		} else if len(formatCodes) > 1 && i < len(formatCodes) {
			format = formatCodes[i]
		}

		var oid uint32 = 0
		if exists && i < len(prep.ParamOIDs) {
			oid = prep.ParamOIDs[i]
		}

		paramVals[i] = convertBindValue(valBytes, format, oid)
	}

	// Result-column format codes
	numResultFormats := readInt16(r)
	resultFormats := make([]int16, numResultFormats)
	for i := 0; i < int(numResultFormats); i++ {
		resultFormats[i] = readInt16(r)
	}

	if !exists {
		_ = sendErrorResponse(conn, fmt.Errorf("prepared statement %q not found", sourcePrep))
		return
	}

	boundStmt, err := executor.BindParams(prep.Stmt, paramVals)
	if err != nil {
		_ = sendErrorResponse(conn, err)
		return
	}

	portals[destPortal] = portal{
		Name:          destPortal,
		Stmt:          boundStmt,
		ResultFormats: resultFormats,
	}

	_ = WriteMessage(conn, '2', nil) // BindComplete
}

func (s *PGWireServer) handleDescribe(conn net.Conn, session *executor.Session, payload []byte, prepared map[string]preparedStmt, portals map[string]portal) {
	r := bytes.NewReader(payload)
	if r.Len() < 1 {
		return
	}
	descType, _ := r.ReadByte()
	name := readString(r)

	if descType == 'S' {
		prep, ok := prepared[name]
		if !ok {
			_ = sendErrorResponse(conn, fmt.Errorf("prepared statement %q not found", name))
			return
		}

		// ParameterDescription ('t')
		paramBuf := &pgBuffer{}
		paramBuf.writeInt16(int16(len(prep.ParamOIDs)))
		for _, oid := range prep.ParamOIDs {
			paramBuf.writeInt32(int32(oid))
		}
		_ = WriteMessage(conn, 't', paramBuf.buf)

		sendStmtDescription(conn, session, prep.Stmt)
	} else if descType == 'P' {
		port, ok := portals[name]
		if !ok {
			_ = sendErrorResponse(conn, fmt.Errorf("portal %q not found", name))
			return
		}
		sendStmtDescription(conn, session, port.Stmt)
	}
}

func (s *PGWireServer) handleExecute(conn net.Conn, session *executor.Session, payload []byte, portals map[string]portal) {
	r := bytes.NewReader(payload)
	portalName := readString(r)
	_ = readInt32(r) // maxRows

	port, ok := portals[portalName]
	if !ok {
		_ = sendErrorResponse(conn, fmt.Errorf("portal %q not found", portalName))
		return
	}

	result, err := session.Execute(port.Stmt)
	if err != nil {
		_ = sendErrorResponse(conn, err)
		return
	}

	if len(result.Columns) > 0 {
		var colTypes []uint32
		if selStmt, ok := port.Stmt.(*parser.SelectStatement); ok {
			_, colTypes = resolveSelectSchema(selStmt, session)
		} else if result.Schema != nil {
			for _, col := range result.Schema.Columns {
				colTypes = append(colTypes, typeToOID(col.Type))
			}
		}

		for _, row := range result.Rows {
			rowBuf := &pgBuffer{}
			rowBuf.writeInt16(int16(len(row)))
			for i, val := range row {
				var format int16 = 0
				if len(port.ResultFormats) == 1 {
					format = port.ResultFormats[0]
				} else if len(port.ResultFormats) > 1 && i < len(port.ResultFormats) {
					format = port.ResultFormats[i]
				}

				if format == 1 { // binary format
					var oid uint32 = 25 // default text
					if i < len(colTypes) {
						oid = colTypes[i]
					}
					binBytes, _ := formatBinaryValue(val, oid)
					rowBuf.writeInt32(int32(len(binBytes)))
					rowBuf.writeBytes(binBytes)
				} else { // text format
					rowBuf.writeInt32(int32(len(val)))
					rowBuf.writeBytes([]byte(val))
				}
			}
			_ = WriteMessage(conn, 'D', rowBuf.buf)
		}
	}

	tag := getCommandTag(port.Stmt, result)
	_ = WriteMessage(conn, 'C', []byte(tag+"\x00"))
}

func (s *PGWireServer) handleSync(conn net.Conn, session *executor.Session) {
	_ = WriteMessage(conn, 'Z', []byte{getTxStatusChar(session)})
}

func sendStmtDescription(conn net.Conn, session *executor.Session, stmt parser.Statement) {
	if stmt == nil {
		_ = WriteMessage(conn, 'n', nil) // NoData
		return
	}

	selStmt, isSelect := stmt.(*parser.SelectStatement)
	if !isSelect {
		_ = WriteMessage(conn, 'n', nil) // NoData
		return
	}

	colNames, colTypes := resolveSelectSchema(selStmt, session)
	if len(colNames) == 0 {
		_ = WriteMessage(conn, 'n', nil) // NoData
		return
	}

	descBuf := &pgBuffer{}
	descBuf.writeInt16(int16(len(colNames)))
	for i, colName := range colNames {
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
}

func getCommandTag(stmt parser.Statement, result *executor.Result) string {
	tag := strings.ToUpper(stmt.StatementType())
	tag = strings.ReplaceAll(tag, "_", " ")

	switch tag {
	case "SELECT":
		return fmt.Sprintf("SELECT %d", len(result.Rows))
	case "INSERT":
		return fmt.Sprintf("INSERT 0 %d", result.Affected)
	case "UPDATE":
		return fmt.Sprintf("UPDATE %d", result.Affected)
	case "DELETE":
		return fmt.Sprintf("DELETE %d", result.Affected)
	default:
		return tag
	}
}

func countParams(stmt parser.Statement) int {
	if stmt == nil {
		return 0
	}
	m := 0
	switch s := stmt.(type) {
	case *parser.SelectStatement:
		m = maxVal(m, findMaxParam(s.Where))
		m = maxVal(m, findMaxParam(s.Having))
		m = maxVal(m, findMaxParam(s.LimitExpr))
		m = maxVal(m, findMaxParam(s.OffsetExpr))
		for _, join := range s.Joins {
			m = maxVal(m, findMaxParam(join.Condition))
		}
		for _, col := range s.Columns {
			m = maxVal(m, findMaxParam(col.Expr))
		}
	case *parser.InsertStatement:
		for _, row := range s.Rows {
			for _, expr := range row {
				m = maxVal(m, findMaxParam(expr))
			}
		}
	case *parser.UpdateStatement:
		for _, a := range s.Assignments {
			m = maxVal(m, findMaxParam(a.Value))
		}
		m = maxVal(m, findMaxParam(s.Where))
	case *parser.DeleteStatement:
		m = maxVal(m, findMaxParam(s.Where))
	}
	return m
}

func findMaxParam(expr parser.Expression) int {
	if expr == nil {
		return 0
	}
	switch e := expr.(type) {
	case *parser.ParamRef:
		return e.Index
	case *parser.BinaryExpr:
		return maxVal(findMaxParam(e.Left), findMaxParam(e.Right))
	case *parser.AndExpr:
		return maxVal(findMaxParam(e.Left), findMaxParam(e.Right))
	case *parser.OrExpr:
		return maxVal(findMaxParam(e.Left), findMaxParam(e.Right))
	case *parser.NotExpr:
		return findMaxParam(e.Expr)
	}
	return 0
}

func maxVal(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func convertBindValue(valBytes []byte, format int16, oid uint32) parser.Value {
	if format == 0 { // text
		valStr := string(valBytes)
		if i, err := strconv.ParseInt(valStr, 10, 64); err == nil {
			return parser.Value{Type: "int", IntVal: i}
		}
		if f, err := strconv.ParseFloat(valStr, 64); err == nil {
			return parser.Value{Type: "float", FltVal: f}
		}
		lowerVal := strings.ToLower(valStr)
		if lowerVal == "true" || lowerVal == "t" || lowerVal == "1" || lowerVal == "yes" {
			return parser.Value{Type: "bool", BoolVal: true}
		}
		if lowerVal == "false" || lowerVal == "f" || lowerVal == "0" || lowerVal == "no" {
			return parser.Value{Type: "bool", BoolVal: false}
		}
		return parser.Value{Type: "string", StrVal: valStr}
	}

	// binary
	switch oid {
	case 16: // bool
		if len(valBytes) > 0 {
			return parser.Value{Type: "bool", BoolVal: valBytes[0] != 0}
		}
	case 23: // int4
		if len(valBytes) >= 4 {
			v := int32(binary.BigEndian.Uint32(valBytes[:4]))
			return parser.Value{Type: "int", IntVal: int64(v)}
		}
	case 20: // int8
		if len(valBytes) >= 8 {
			v := int64(binary.BigEndian.Uint64(valBytes[:8]))
			return parser.Value{Type: "int", IntVal: v}
		}
	case 701: // float8
		if len(valBytes) >= 8 {
			bits := binary.BigEndian.Uint64(valBytes[:8])
			v := math.Float64frombits(bits)
			return parser.Value{Type: "float", FltVal: v}
		}
	}

	// Fallback heuristics by length if OID is not matched/provided
	switch len(valBytes) {
	case 1:
		return parser.Value{Type: "bool", BoolVal: valBytes[0] != 0}
	case 4:
		v := int32(binary.BigEndian.Uint32(valBytes))
		return parser.Value{Type: "int", IntVal: int64(v)}
	case 8:
		v := int64(binary.BigEndian.Uint64(valBytes))
		return parser.Value{Type: "int", IntVal: v}
	default:
		return parser.Value{Type: "string", StrVal: string(valBytes)}
	}
}

func formatBinaryValue(valStr string, oid uint32) ([]byte, error) {
	switch oid {
	case 16: // bool
		var b byte = 0
		if valStr == "true" || valStr == "t" || valStr == "1" {
			b = 1
		}
		return []byte{b}, nil
	case 23: // int4
		v, _ := strconv.ParseInt(valStr, 10, 32)
		buf := make([]byte, 4)
		binary.BigEndian.PutUint32(buf, uint32(v))
		return buf, nil
	case 20: // int8
		v, _ := strconv.ParseInt(valStr, 10, 64)
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, uint64(v))
		return buf, nil
	case 701: // float8
		v, _ := strconv.ParseFloat(valStr, 64)
		bits := math.Float64bits(v)
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, bits)
		return buf, nil
	default: // text/varchar/etc.
		return []byte(valStr), nil
	}
}
