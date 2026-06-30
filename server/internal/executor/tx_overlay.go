package executor

// Транзакционный overlay, сериализация буферизованных операций при spill'е,
// заморозка волатильных функций и сериализация autocommit-записей с коммитами.
//
// Инвариант консистентности (почему overlay при чтении и повтор при COMMIT дают
// одинаковый результат):
//   1. Bug #2: снимок версии таблицы берётся при ПЕРВОМ обращении (чтении или
//      записи), а autocommit-записи бьют версию под тем же per-table commit-локом,
//      что и Commit. Значит, между первым обращением транзакции и её COMMIT'ом
//      затронутые таблицы НЕ могут быть изменены никем другим (иначе COMMIT
//      упадёт с конфликтом). Поэтому повторное вычисление WHERE буферизованного
//      запроса при COMMIT даёт ровно те же строки, что и overlay при чтении —
//      для предикатов, зависящих от данных, консистентность соблюдается.
//   2. Bug #3: единственный оставшийся источник расхождения — волатильные
//      функции (NOW, CURRENT_*, UUID). Они «замораживаются» в литералы в момент
//      буферизации (freeze*), поэтому overlay и commit-apply используют
//      идентичные значения.

import (
	"encoding/json"
	"fmt"
	"strings"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

func init() {
	// Регистрируем кодек, восстанавливающий типизированный Payload при spill'е.
	txmanager.EncodePendingOp = encodePendingOp
	txmanager.DecodePendingOp = decodePendingOp
}

// applyTxOverlay накладывает буферизованные операции активной транзакции на
// набор базовых (committed) строк таблицы — это и есть read-your-own-writes
// (Bug #1). Вне транзакции возвращает base без изменений. Кроме того фиксирует
// OCC-снимок версии таблицы при чтении (Bug #2a).
//
// Применяется ТОЛЬКО на путях текущего чтения (ReadCurrentRows); для AS OF /
// time-travel overlay не применяется — там должна быть только закоммиченная
// история.
func applyTxOverlay(ctx *ExecutionContext, db, table string, base []storage.Row) ([]storage.Row, error) {
	if ctx == nil || ctx.Session == nil || !ctx.Session.IsInTx() {
		return base, nil
	}
	tx := ctx.Session.GetActiveTx()
	if tx == nil {
		return base, nil
	}

	// OCC: снимок версии при первом обращении (чтении) — Bug #2a.
	if ctx.TxManager != nil {
		ctx.TxManager.RecordAccess(tx, db, table)
	}

	ops, err := tx.ReadOps()
	if err != nil {
		return nil, err
	}

	relevant := false
	for i := range ops {
		if ops[i].DB == db && ops[i].Table == table {
			relevant = true
			break
		}
	}
	if !relevant {
		return base, nil
	}

	schema, err := ctx.Storage.GetTableSchema(db, table)
	if err != nil {
		return nil, err
	}

	result := make([]storage.Row, len(base))
	copy(result, base)

	for i := range ops {
		op := ops[i]
		if op.DB != db || op.Table != table {
			continue
		}
		switch op.Type {
		case "insert":
			s, ok := op.Payload.(*parser.InsertStatement)
			if !ok {
				return nil, fmt.Errorf("tx overlay: invalid insert payload type %T", op.Payload)
			}
			cmd := &InsertCommand{stmt: s}
			newRows, err := cmd.buildRows(schema, ctx)
			if err != nil {
				return nil, err
			}
			result = append(result, newRows...)
		case "update":
			s, ok := op.Payload.(*parser.UpdateStatement)
			if !ok {
				return nil, fmt.Errorf("tx overlay: invalid update payload type %T", op.Payload)
			}
			// Значения присваиваний вычисляем так же, как executeImmediate
			// (один раз, с nil-строкой), чтобы overlay и commit совпали.
			updates := make(map[string]storage.Value, len(s.Assignments))
			for _, a := range s.Assignments {
				val, err := evalOperand(a.Value, nil, schema, ctx)
				if err != nil {
					return nil, fmt.Errorf("tx overlay: column '%s': %w", a.Column, err)
				}
				updates[a.Column] = val
			}
			for idx := range result {
				match, err := evalExpr(s.Where, result[idx], schema, ctx)
				if err != nil {
					return nil, err
				}
				if match {
					result[idx] = applyUpdatesToRow(result[idx], schema, updates)
				}
			}
		case "delete":
			s, ok := op.Payload.(*parser.DeleteStatement)
			if !ok {
				return nil, fmt.Errorf("tx overlay: invalid delete payload type %T", op.Payload)
			}
			kept := result[:0]
			for _, row := range result {
				match, err := evalExpr(s.Where, row, schema, ctx)
				if err != nil {
					return nil, err
				}
				if !match {
					kept = append(kept, row)
				}
			}
			result = kept
		case "truncate":
			result = result[:0]
		}
	}
	return result, nil
}

// applyUpdatesToRow возвращает копию row с применёнными присваиваниями.
func applyUpdatesToRow(row storage.Row, schema *storage.TableSchema, updates map[string]storage.Value) storage.Row {
	nr := make(storage.Row, len(row))
	copy(nr, row)
	for col, val := range updates {
		for ci, sc := range schema.Columns {
			if strings.EqualFold(sc.Name, col) && ci < len(nr) {
				nr[ci] = val
				break
			}
		}
	}
	return nr
}

// mutateUnderTableLock выполняет fn под per-table commit-локом, чтобы
// autocommit-запись (мутация storage + bump версии в notifyMutation)
// сериализовалась с коммитами транзакций (Bug #2b).
//
// Deadlock guard: во время applyOps внутри Commit локи уже взяты, поэтому при
// ctx.InCommitApply=true мы НЕ берём их повторно (sync.Mutex не реентрантный).
func mutateUnderTableLock(ctx *ExecutionContext, db, table string, fn func() error) error {
	if ctx == nil || ctx.InCommitApply || ctx.TxManager == nil {
		return fn()
	}
	unlock := ctx.TxManager.LockTables([]string{txmanager.TableKey(db, table)})
	defer unlock()
	return fn()
}

// volatileFuncs — функции, значение которых меняется при каждом вызове и которые
// нужно «замораживать» при буферизации (Bug #3).
var volatileFuncs = map[string]bool{
	"NOW":               true,
	"CURRENT_TIMESTAMP": true,
	"CURRENT_DATE":      true,
	"CURRENT_TIME":      true,
	"UUID":              true,
}

// rawToParserValue превращает вычисленное Go-значение обратно в литерал AST.
func rawToParserValue(v interface{}) parser.Value {
	switch x := v.(type) {
	case nil:
		return parser.Value{Type: "null"}
	case int:
		return parser.Value{Type: "int", IntVal: int64(x)}
	case int64:
		return parser.Value{Type: "int", IntVal: x}
	case float64:
		return parser.Value{Type: "float", FltVal: x}
	case bool:
		return parser.Value{Type: "bool", BoolVal: x}
	case string:
		return parser.Value{Type: "string", StrVal: x}
	default:
		return parser.Value{Type: "string", StrVal: fmt.Sprintf("%v", x)}
	}
}

// freezeVolatileExpr заменяет вызовы волатильных функций на вычисленные литералы,
// рекурсивно обходя выражение. Прочие узлы пересобираются без изменения смысла.
func freezeVolatileExpr(e parser.Expression, ctx *ExecutionContext) (parser.Expression, error) {
	switch v := e.(type) {
	case *parser.FunctionCall:
		if volatileFuncs[strings.ToUpper(v.Name)] {
			val, err := evalFunctionCall(v, nil, nil, ctx)
			if err != nil {
				return nil, err
			}
			pv := rawToParserValue(val)
			return &pv, nil
		}
		args := make([]parser.Expression, len(v.Args))
		for i, a := range v.Args {
			fa, err := freezeVolatileExpr(a, ctx)
			if err != nil {
				return nil, err
			}
			args[i] = fa
		}
		return &parser.FunctionCall{Name: v.Name, Args: args}, nil
	case *parser.BinaryExpr:
		l, err := freezeVolatileExpr(v.Left, ctx)
		if err != nil {
			return nil, err
		}
		r, err := freezeVolatileExpr(v.Right, ctx)
		if err != nil {
			return nil, err
		}
		return &parser.BinaryExpr{Left: l, Operator: v.Operator, Right: r}, nil
	case *parser.AndExpr:
		l, err := freezeVolatileExpr(v.Left, ctx)
		if err != nil {
			return nil, err
		}
		r, err := freezeVolatileExpr(v.Right, ctx)
		if err != nil {
			return nil, err
		}
		return &parser.AndExpr{Left: l, Right: r}, nil
	case *parser.OrExpr:
		l, err := freezeVolatileExpr(v.Left, ctx)
		if err != nil {
			return nil, err
		}
		r, err := freezeVolatileExpr(v.Right, ctx)
		if err != nil {
			return nil, err
		}
		return &parser.OrExpr{Left: l, Right: r}, nil
	case *parser.NotExpr:
		inner, err := freezeVolatileExpr(v.Expr, ctx)
		if err != nil {
			return nil, err
		}
		return &parser.NotExpr{Expr: inner}, nil
	case *parser.InExpr:
		l, err := freezeVolatileExpr(v.Left, ctx)
		if err != nil {
			return nil, err
		}
		list := make([]parser.Expression, len(v.Right))
		for i, r := range v.Right {
			fr, err := freezeVolatileExpr(r, ctx)
			if err != nil {
				return nil, err
			}
			list[i] = fr
		}
		return &parser.InExpr{Left: l, Right: list, Not: v.Not}, nil
	default:
		return e, nil
	}
}

// freezeInsert возвращает копию INSERT с замороженными волатильными функциями в
// списках значений. Для INSERT ... SELECT заморозка не нужна (нет списков
// литералов) — возвращаем исходный statement.
func freezeInsert(s *parser.InsertStatement, ctx *ExecutionContext) (*parser.InsertStatement, error) {
	if s.SelectQuery != nil {
		return s, nil
	}
	cp := *s
	cp.Rows = make([][]parser.Expression, len(s.Rows))
	for i, row := range s.Rows {
		nr := make([]parser.Expression, len(row))
		for j, ex := range row {
			fe, err := freezeVolatileExpr(ex, ctx)
			if err != nil {
				return nil, err
			}
			nr[j] = fe
		}
		cp.Rows[i] = nr
	}
	return &cp, nil
}

// freezeUpdate возвращает копию UPDATE с замороженными волатильными функциями в
// присваиваниях и WHERE.
func freezeUpdate(s *parser.UpdateStatement, ctx *ExecutionContext) (*parser.UpdateStatement, error) {
	cp := *s
	cp.Assignments = make([]parser.Assignment, len(s.Assignments))
	for i, a := range s.Assignments {
		fv, err := freezeVolatileExpr(a.Value, ctx)
		if err != nil {
			return nil, err
		}
		cp.Assignments[i] = parser.Assignment{Column: a.Column, Value: fv}
	}
	if s.Where != nil {
		fw, err := freezeVolatileExpr(s.Where, ctx)
		if err != nil {
			return nil, err
		}
		cp.Where = fw
	}
	return &cp, nil
}

// freezeDelete возвращает копию DELETE с замороженным волатильным WHERE.
func freezeDelete(s *parser.DeleteStatement, ctx *ExecutionContext) (*parser.DeleteStatement, error) {
	if s.Where == nil {
		return s, nil
	}
	cp := *s
	fw, err := freezeVolatileExpr(s.Where, ctx)
	if err != nil {
		return nil, err
	}
	cp.Where = fw
	return &cp, nil
}

// --- Сериализация PendingOp для spill'а ----------------------------------
//
// JSON не умеет восстанавливать поля-интерфейсы (parser.Expression внутри
// statement'ов), поэтому используем явный wire-формат с тегами типов. Любой
// неподдерживаемый узел/форма приводит к ошибке — операция НЕ теряется молча.

type wireExpr struct {
	K     string      `json:"k"`
	Type  string      `json:"t,omitempty"`
	I     int64       `json:"i,omitempty"`
	F     float64     `json:"f,omitempty"`
	S     string      `json:"s,omitempty"`
	B     bool        `json:"b,omitempty"`
	Name  string      `json:"n,omitempty"`
	Op    string      `json:"o,omitempty"`
	Left  *wireExpr   `json:"l,omitempty"`
	Right *wireExpr   `json:"r,omitempty"`
	Args  []*wireExpr `json:"a,omitempty"`
	List  []*wireExpr `json:"li,omitempty"`
	Not   bool        `json:"not,omitempty"`
	Idx   int         `json:"ix,omitempty"`
}

type wireAssign struct {
	Column string    `json:"c"`
	Value  *wireExpr `json:"v"`
}

type wireOp struct {
	Type     string        `json:"type"`
	DB       string        `json:"db"`
	Table    string        `json:"table"`
	Pos      int           `json:"pos,omitempty"`
	Insert   *wireInsert   `json:"insert,omitempty"`
	Update   *wireUpdate   `json:"update,omitempty"`
	Delete   *wireDelete   `json:"delete,omitempty"`
	Truncate *wireTruncate `json:"truncate,omitempty"`
}

type wireInsert struct {
	TableName string        `json:"table"`
	Columns   []string      `json:"columns,omitempty"`
	Rows      [][]*wireExpr `json:"rows"`
}

type wireUpdate struct {
	TableName   string       `json:"table"`
	Assignments []wireAssign `json:"assignments"`
	Where       *wireExpr    `json:"where,omitempty"`
}

type wireDelete struct {
	TableName string    `json:"table"`
	Where     *wireExpr `json:"where,omitempty"`
}

type wireTruncate struct {
	TableName string `json:"table"`
}

func encodeExpr(e parser.Expression) (*wireExpr, error) {
	if e == nil {
		return nil, nil
	}
	switch v := e.(type) {
	case parser.Value:
		return &wireExpr{K: "val", Type: v.Type, I: v.IntVal, F: v.FltVal, S: v.StrVal, B: v.BoolVal}, nil
	case *parser.Value:
		return &wireExpr{K: "val", Type: v.Type, I: v.IntVal, F: v.FltVal, S: v.StrVal, B: v.BoolVal}, nil
	case *parser.ColumnRef:
		return &wireExpr{K: "col", Name: v.Name}, nil
	case *parser.BinaryExpr:
		l, err := encodeExpr(v.Left)
		if err != nil {
			return nil, err
		}
		r, err := encodeExpr(v.Right)
		if err != nil {
			return nil, err
		}
		return &wireExpr{K: "bin", Op: v.Operator, Left: l, Right: r}, nil
	case *parser.AndExpr:
		l, err := encodeExpr(v.Left)
		if err != nil {
			return nil, err
		}
		r, err := encodeExpr(v.Right)
		if err != nil {
			return nil, err
		}
		return &wireExpr{K: "and", Left: l, Right: r}, nil
	case *parser.OrExpr:
		l, err := encodeExpr(v.Left)
		if err != nil {
			return nil, err
		}
		r, err := encodeExpr(v.Right)
		if err != nil {
			return nil, err
		}
		return &wireExpr{K: "or", Left: l, Right: r}, nil
	case *parser.NotExpr:
		l, err := encodeExpr(v.Expr)
		if err != nil {
			return nil, err
		}
		return &wireExpr{K: "not", Left: l}, nil
	case *parser.FunctionCall:
		args := make([]*wireExpr, len(v.Args))
		for i, a := range v.Args {
			wa, err := encodeExpr(a)
			if err != nil {
				return nil, err
			}
			args[i] = wa
		}
		return &wireExpr{K: "fn", Name: v.Name, Args: args}, nil
	case *parser.InExpr:
		l, err := encodeExpr(v.Left)
		if err != nil {
			return nil, err
		}
		list := make([]*wireExpr, len(v.Right))
		for i, r := range v.Right {
			wr, err := encodeExpr(r)
			if err != nil {
				return nil, err
			}
			list[i] = wr
		}
		return &wireExpr{K: "in", Left: l, List: list, Not: v.Not}, nil
	case *parser.ParamRef:
		return &wireExpr{K: "param", Idx: v.Index}, nil
	default:
		return nil, fmt.Errorf("spill: unsupported expression type %T", e)
	}
}

func decodeExpr(w *wireExpr) (parser.Expression, error) {
	if w == nil {
		return nil, nil
	}
	switch w.K {
	case "val":
		return &parser.Value{Type: w.Type, IntVal: w.I, FltVal: w.F, StrVal: w.S, BoolVal: w.B}, nil
	case "col":
		return &parser.ColumnRef{Name: w.Name}, nil
	case "bin":
		l, err := decodeExpr(w.Left)
		if err != nil {
			return nil, err
		}
		r, err := decodeExpr(w.Right)
		if err != nil {
			return nil, err
		}
		return &parser.BinaryExpr{Left: l, Operator: w.Op, Right: r}, nil
	case "and":
		l, err := decodeExpr(w.Left)
		if err != nil {
			return nil, err
		}
		r, err := decodeExpr(w.Right)
		if err != nil {
			return nil, err
		}
		return &parser.AndExpr{Left: l, Right: r}, nil
	case "or":
		l, err := decodeExpr(w.Left)
		if err != nil {
			return nil, err
		}
		r, err := decodeExpr(w.Right)
		if err != nil {
			return nil, err
		}
		return &parser.OrExpr{Left: l, Right: r}, nil
	case "not":
		l, err := decodeExpr(w.Left)
		if err != nil {
			return nil, err
		}
		return &parser.NotExpr{Expr: l}, nil
	case "fn":
		args := make([]parser.Expression, len(w.Args))
		for i, a := range w.Args {
			da, err := decodeExpr(a)
			if err != nil {
				return nil, err
			}
			args[i] = da
		}
		return &parser.FunctionCall{Name: w.Name, Args: args}, nil
	case "in":
		l, err := decodeExpr(w.Left)
		if err != nil {
			return nil, err
		}
		list := make([]parser.Expression, len(w.List))
		for i, r := range w.List {
			dr, err := decodeExpr(r)
			if err != nil {
				return nil, err
			}
			list[i] = dr
		}
		return &parser.InExpr{Left: l, Right: list, Not: w.Not}, nil
	case "param":
		return &parser.ParamRef{Index: w.Idx}, nil
	default:
		return nil, fmt.Errorf("spill: unknown expr kind %q", w.K)
	}
}

func encodePendingOp(op txmanager.PendingOp) ([]byte, error) {
	w := wireOp{Type: op.Type, DB: op.DB, Table: op.Table, Pos: op.Pos}
	switch op.Type {
	case "insert":
		s, ok := op.Payload.(*parser.InsertStatement)
		if !ok {
			return nil, fmt.Errorf("spill: invalid insert payload type %T", op.Payload)
		}
		if s.SelectQuery != nil || s.OnConflict != nil || len(s.Returning) > 0 {
			return nil, fmt.Errorf("spill: INSERT with SELECT/ON CONFLICT/RETURNING is not supported")
		}
		wi := &wireInsert{TableName: s.TableName, Columns: s.Columns}
		wi.Rows = make([][]*wireExpr, len(s.Rows))
		for i, row := range s.Rows {
			wr := make([]*wireExpr, len(row))
			for j, ex := range row {
				we, err := encodeExpr(ex)
				if err != nil {
					return nil, err
				}
				wr[j] = we
			}
			wi.Rows[i] = wr
		}
		w.Insert = wi
	case "update":
		s, ok := op.Payload.(*parser.UpdateStatement)
		if !ok {
			return nil, fmt.Errorf("spill: invalid update payload type %T", op.Payload)
		}
		if s.FromTable != "" || s.FromSubquery != nil || len(s.Returning) > 0 {
			return nil, fmt.Errorf("spill: UPDATE with FROM/RETURNING is not supported")
		}
		wu := &wireUpdate{TableName: s.TableName}
		wu.Assignments = make([]wireAssign, len(s.Assignments))
		for i, a := range s.Assignments {
			we, err := encodeExpr(a.Value)
			if err != nil {
				return nil, err
			}
			wu.Assignments[i] = wireAssign{Column: a.Column, Value: we}
		}
		ww, err := encodeExpr(s.Where)
		if err != nil {
			return nil, err
		}
		wu.Where = ww
		w.Update = wu
	case "delete":
		s, ok := op.Payload.(*parser.DeleteStatement)
		if !ok {
			return nil, fmt.Errorf("spill: invalid delete payload type %T", op.Payload)
		}
		if len(s.Returning) > 0 {
			return nil, fmt.Errorf("spill: DELETE with RETURNING is not supported")
		}
		ww, err := encodeExpr(s.Where)
		if err != nil {
			return nil, err
		}
		w.Delete = &wireDelete{TableName: s.TableName, Where: ww}
	case "truncate":
		s, ok := op.Payload.(*parser.TruncateStatement)
		if !ok {
			return nil, fmt.Errorf("spill: invalid truncate payload type %T", op.Payload)
		}
		w.Truncate = &wireTruncate{TableName: s.TableName}
	default:
		return nil, fmt.Errorf("spill: unknown op type %q", op.Type)
	}
	// json.Marshal не вставляет переводы строк — безопасно для построчного spill.
	return json.Marshal(w)
}

func decodePendingOp(data []byte) (txmanager.PendingOp, error) {
	var w wireOp
	if err := json.Unmarshal(data, &w); err != nil {
		return txmanager.PendingOp{}, err
	}
	op := txmanager.PendingOp{Type: w.Type, DB: w.DB, Table: w.Table, Pos: w.Pos}
	switch w.Type {
	case "insert":
		if w.Insert == nil {
			return op, fmt.Errorf("spill: missing insert body")
		}
		s := &parser.InsertStatement{TableName: w.Insert.TableName, Columns: w.Insert.Columns}
		s.Rows = make([][]parser.Expression, len(w.Insert.Rows))
		for i, row := range w.Insert.Rows {
			nr := make([]parser.Expression, len(row))
			for j, we := range row {
				de, err := decodeExpr(we)
				if err != nil {
					return op, err
				}
				nr[j] = de
			}
			s.Rows[i] = nr
		}
		op.Payload = s
	case "update":
		if w.Update == nil {
			return op, fmt.Errorf("spill: missing update body")
		}
		s := &parser.UpdateStatement{TableName: w.Update.TableName}
		s.Assignments = make([]parser.Assignment, len(w.Update.Assignments))
		for i, a := range w.Update.Assignments {
			dv, err := decodeExpr(a.Value)
			if err != nil {
				return op, err
			}
			s.Assignments[i] = parser.Assignment{Column: a.Column, Value: dv}
		}
		dw, err := decodeExpr(w.Update.Where)
		if err != nil {
			return op, err
		}
		s.Where = dw
		op.Payload = s
	case "delete":
		if w.Delete == nil {
			return op, fmt.Errorf("spill: missing delete body")
		}
		s := &parser.DeleteStatement{TableName: w.Delete.TableName}
		dw, err := decodeExpr(w.Delete.Where)
		if err != nil {
			return op, err
		}
		s.Where = dw
		op.Payload = s
	case "truncate":
		if w.Truncate == nil {
			return op, fmt.Errorf("spill: missing truncate body")
		}
		op.Payload = &parser.TruncateStatement{TableName: w.Truncate.TableName}
	default:
		return op, fmt.Errorf("spill: unknown op type %q", w.Type)
	}
	return op, nil
}
