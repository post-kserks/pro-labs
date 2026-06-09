package lexer

import (
	"fmt"
	"strings"
	"unicode"
)

type TokenType int

const (
	// Keywords
	TOKEN_SELECT TokenType = iota
	TOKEN_INSERT
	TOKEN_UPDATE
	TOKEN_DELETE
	TOKEN_FROM
	TOKEN_INTO
	TOKEN_WHERE
	TOKEN_SET
	TOKEN_VALUES
	TOKEN_CREATE
	TOKEN_DROP
	TOKEN_DATABASE
	TOKEN_TABLE
	TOKEN_USE
	TOKEN_SHOW
	TOKEN_DATABASES
	TOKEN_TABLES
	TOKEN_DESCRIBE
	TOKEN_LIMIT
	TOKEN_EXPLAIN
	TOKEN_ANALYZE
	TOKEN_AS
	TOKEN_OF
	TOKEN_ON
	TOKEN_TIMESTAMP
	TOKEN_VERSION
	TOKEN_HISTORY
	TOKEN_KEY
	TOKEN_AND
	TOKEN_OR
	TOKEN_NOT
	TOKEN_NULL
	TOKEN_TRUE
	TOKEN_FALSE

	TOKEN_ORDER
	TOKEN_BY
	TOKEN_ASC
	TOKEN_DESC
	TOKEN_OFFSET
	TOKEN_GROUP
	TOKEN_HAVING
	TOKEN_JOIN
	TOKEN_INNER
	TOKEN_LEFT
	TOKEN_RIGHT
	TOKEN_FULL
	TOKEN_CROSS
	TOKEN_UNION
	TOKEN_INTERSECT
	TOKEN_EXCEPT
	TOKEN_FOR
	TOKEN_USING
	TOKEN_OVER
	TOKEN_PARTITION
	TOKEN_ROWS
	TOKEN_RANGE
	TOKEN_UNBOUNDED
	TOKEN_PRECEDING
	TOKEN_FOLLOWING
	TOKEN_CURRENT
	TOKEN_ROW
	TOKEN_BETWEEN
	TOKEN_IN
	TOKEN_IS
	TOKEN_LIKE
	TOKEN_SEMANTIC_MATCH
	TOKEN_FTS_MATCH
	TOKEN_ALTER
	TOKEN_ADD
	TOKEN_COLUMN
	TOKEN_RENAME
	TOKEN_TO
	TOKEN_TYPE
	TOKEN_DEFAULT
	TOKEN_CAST
	TOKEN_CASE
	TOKEN_WHEN
	TOKEN_THEN
	TOKEN_ELSE
	TOKEN_END
	TOKEN_COALESCE

	TOKEN_VACUUM
	TOKEN_MIGRATION
	TOKEN_POLICY
	TOKEN_ENABLE
	TOKEN_RLS
	TOKEN_INDEX
	TOKEN_INDEXES
	TOKEN_BEGIN
	TOKEN_COMMIT
	TOKEN_ROLLBACK
	TOKEN_PREPARE
	TOKEN_EXECUTE
	TOKEN_DEALLOCATE
	TOKEN_APPLY
	TOKEN_PREVIEW

	// Data types
	TOKEN_INT
	TOKEN_FLOAT_TYPE
	TOKEN_BOOL
	TOKEN_TEXT
	TOKEN_VARCHAR
	TOKEN_DATE
	TOKEN_TIME
	TOKEN_DECIMAL
	TOKEN_ENUM
	TOKEN_ARRAY
	TOKEN_VECTOR
	TOKEN_FLEXIBLE
	TOKEN_INFER
	TOKEN_GENERATED
	TOKEN_ALWAYS
	TOKEN_SCHEMA

	// Literals and identifiers
	TOKEN_IDENT
	TOKEN_INT_LIT
	TOKEN_FLOAT_LIT
	TOKEN_STRING_LIT

	// Operators and symbols
	TOKEN_EQ
	TOKEN_NEQ
	TOKEN_LT
	TOKEN_GT
	TOKEN_LTE
	TOKEN_GTE
	TOKEN_COMMA
	TOKEN_DOT
	TOKEN_ARROW
	TOKEN_DBL_ARROW
	TOKEN_SEMICOLON
	TOKEN_LPAREN
	TOKEN_RPAREN
	TOKEN_STAR
	TOKEN_MINUS
	TOKEN_PLUS
	TOKEN_SLASH
	TOKEN_PARAM

	TOKEN_EOF
	TOKEN_ILLEGAL
)

type Token struct {
	Type    TokenType
	Literal string
	Line    int
	Col     int
}

type Lexer struct {
	input        []rune
	position     int
	readPosition int
	ch           rune
}

func New(input string) *Lexer {
	l := &Lexer{input: []rune(input)}
	l.readChar()
	return l
}

func (l *Lexer) NextToken() Token {
	l.skipWhitespace()
	start := l.position

	var tok Token
	switch l.ch {
	case 0:
		tok = l.newToken(TOKEN_EOF, "", start)
	case '=':
		tok = l.newToken(TOKEN_EQ, "=", start)
		l.readChar()
	case '!':
		if l.peekChar() == '=' {
			ch := l.ch
			l.readChar()
			tok = l.newToken(TOKEN_NEQ, string([]rune{ch, l.ch}), start)
			l.readChar()
		} else {
			tok = l.newToken(TOKEN_ILLEGAL, string(l.ch), start)
			l.readChar()
		}
	case '<':
		if l.peekChar() == '=' {
			ch := l.ch
			l.readChar()
			tok = l.newToken(TOKEN_LTE, string([]rune{ch, l.ch}), start)
			l.readChar()
		} else {
			tok = l.newToken(TOKEN_LT, "<", start)
			l.readChar()
		}
	case '>':
		if l.peekChar() == '=' {
			ch := l.ch
			l.readChar()
			tok = l.newToken(TOKEN_GTE, string([]rune{ch, l.ch}), start)
			l.readChar()
		} else {
			tok = l.newToken(TOKEN_GT, ">", start)
			l.readChar()
		}
	case ',':
		tok = l.newToken(TOKEN_COMMA, ",", start)
		l.readChar()
	case '.':
		tok = l.newToken(TOKEN_DOT, ".", start)
		l.readChar()
	case ';':
		tok = l.newToken(TOKEN_SEMICOLON, ";", start)
		l.readChar()
	case '(':
		tok = l.newToken(TOKEN_LPAREN, "(", start)
		l.readChar()
	case ')':
		tok = l.newToken(TOKEN_RPAREN, ")", start)
		l.readChar()
	case '*':
		tok = l.newToken(TOKEN_STAR, "*", start)
		l.readChar()
	case '+':
		tok = l.newToken(TOKEN_PLUS, "+", start)
		l.readChar()
	case '/':
		tok = l.newToken(TOKEN_SLASH, "/", start)
		l.readChar()
	case '$':
		l.readChar() // skip $
		startNum := l.position
		for isDigit(l.ch) {
			l.readChar()
		}
		if l.position == startNum {
			tok = l.newToken(TOKEN_ILLEGAL, "$", start)
		} else {
			tok = l.newToken(TOKEN_PARAM, string(l.input[startNum:l.position]), start)
		}
		return tok
	case '-':
		if isDigit(l.peekChar()) {
			lit, isFloat := l.readNumber()
			if isFloat {
				tok = l.newToken(TOKEN_FLOAT_LIT, lit, start)
			} else {
				tok = l.newToken(TOKEN_INT_LIT, lit, start)
			}
			return tok
		}
		if l.peekChar() == '>' {
			l.readChar() // consume first '>'
			if l.peekChar() == '>' {
				l.readChar() // consume second '>'
				tok = l.newToken(TOKEN_DBL_ARROW, "->>", start)
			} else {
				tok = l.newToken(TOKEN_ARROW, "->", start)
			}
			l.readChar() // advance past the last '>'
			return tok
		}
		tok = l.newToken(TOKEN_MINUS, "-", start)
		l.readChar()
		return tok
	case '\'':
		lit, ok := l.readString()
		if !ok {
			tok = l.newToken(TOKEN_ILLEGAL, "unterminated string literal", start)
		} else {
			tok = l.newToken(TOKEN_STRING_LIT, lit, start)
		}
	default:
		if isLetter(l.ch) {
			ident := l.readIdentifier()
			typeOf := LookupIdent(ident)
			return l.newToken(typeOf, ident, start)
		}
		if isDigit(l.ch) {
			lit, isFloat := l.readNumber()
			if isFloat {
				return l.newToken(TOKEN_FLOAT_LIT, lit, start)
			}
			return l.newToken(TOKEN_INT_LIT, lit, start)
		}
		tok = l.newToken(TOKEN_ILLEGAL, string(l.ch), start)
		l.readChar()
	}

	return tok
}

func (l *Lexer) readChar() {
	if l.readPosition >= len(l.input) {
		l.position = len(l.input)
		l.ch = 0
		l.readPosition++
		return
	}
	l.ch = l.input[l.readPosition]
	l.position = l.readPosition
	l.readPosition++
}

func (l *Lexer) peekChar() rune {
	if l.readPosition >= len(l.input) {
		return 0
	}
	return l.input[l.readPosition]
}

func (l *Lexer) skipWhitespace() {
	for unicode.IsSpace(l.ch) {
		l.readChar()
	}
}

func (l *Lexer) readIdentifier() string {
	start := l.position
	for isLetter(l.ch) || isDigit(l.ch) {
		l.readChar()
	}
	return string(l.input[start:l.position])
}

func (l *Lexer) readNumber() (string, bool) {
	start := l.position
	if l.ch == '-' {
		l.readChar()
	}

	hasDot := false
	for isDigit(l.ch) || (!hasDot && l.ch == '.') {
		if l.ch == '.' {
			hasDot = true
		}
		l.readChar()
	}

	return string(l.input[start:l.position]), hasDot
}

func (l *Lexer) readString() (string, bool) {
	var b strings.Builder

	// Skip opening quote.
	l.readChar()
	for l.ch != '\'' && l.ch != 0 {
		if l.ch == '\\' {
			next := l.peekChar()
			switch next {
			case '\\', '\'', 'n', 't', 'r':
				l.readChar()
				switch l.ch {
				case 'n':
					b.WriteRune('\n')
				case 't':
					b.WriteRune('\t')
				case 'r':
					b.WriteRune('\r')
				default:
					b.WriteRune(l.ch)
				}
				l.readChar()
				continue
			}
		}
		b.WriteRune(l.ch)
		l.readChar()
	}

	if l.ch != '\'' {
		return "", false
	}

	// Skip closing quote.
	l.readChar()
	return b.String(), true
}

func (l *Lexer) newToken(tokenType TokenType, lit string, position int) Token {
	line, col := l.positionToLineCol(position)
	return Token{Type: tokenType, Literal: lit, Line: line, Col: col}
}

func (l *Lexer) positionToLineCol(position int) (line, col int) {
	if position < 0 {
		position = 0
	}
	if position > len(l.input) {
		position = len(l.input)
	}

	line = 1
	col = 1
	for i := 0; i < position; i++ {
		if l.input[i] == '\n' {
			line++
			col = 1
			continue
		}
		col++
	}
	return line, col
}

func isLetter(ch rune) bool {
	return unicode.IsLetter(ch) || ch == '_'
}

func isDigit(ch rune) bool {
	return ch >= '0' && ch <= '9'
}

var keywords = map[string]TokenType{
	"SELECT":     TOKEN_SELECT,
	"INSERT":     TOKEN_INSERT,
	"UPDATE":     TOKEN_UPDATE,
	"DELETE":     TOKEN_DELETE,
	"FROM":       TOKEN_FROM,
	"INTO":       TOKEN_INTO,
	"WHERE":      TOKEN_WHERE,
	"SET":        TOKEN_SET,
	"VALUES":     TOKEN_VALUES,
	"CREATE":     TOKEN_CREATE,
	"DROP":       TOKEN_DROP,
	"DATABASE":   TOKEN_DATABASE,
	"TABLE":      TOKEN_TABLE,
	"USE":        TOKEN_USE,
	"SHOW":       TOKEN_SHOW,
	"DATABASES":  TOKEN_DATABASES,
	"TABLES":     TOKEN_TABLES,
	"DESCRIBE":   TOKEN_DESCRIBE,
	"LIMIT":      TOKEN_LIMIT,
	"EXPLAIN":    TOKEN_EXPLAIN,
	"ANALYZE":    TOKEN_ANALYZE,
	"AS":         TOKEN_AS,
	"OF":         TOKEN_OF,
	"ON":         TOKEN_ON,
	"TIMESTAMP":  TOKEN_TIMESTAMP,
	"VERSION":    TOKEN_VERSION,
	"HISTORY":    TOKEN_HISTORY,
	"KEY":        TOKEN_KEY,
	"AND":        TOKEN_AND,
	"OR":         TOKEN_OR,
	"NOT":        TOKEN_NOT,
	"NULL":       TOKEN_NULL,
	"TRUE":       TOKEN_TRUE,
	"FALSE":      TOKEN_FALSE,
	"ORDER":      TOKEN_ORDER,
	"BY":         TOKEN_BY,
	"ASC":        TOKEN_ASC,
	"DESC":       TOKEN_DESC,
	"OFFSET":     TOKEN_OFFSET,
	"GROUP":      TOKEN_GROUP,
	"HAVING":     TOKEN_HAVING,
	"JOIN":       TOKEN_JOIN,
	"INNER":      TOKEN_INNER,
	"LEFT":       TOKEN_LEFT,
	"RIGHT":      TOKEN_RIGHT,
	"FULL":       TOKEN_FULL,
	"CROSS":      TOKEN_CROSS,
	"UNION":      TOKEN_UNION,
	"INTERSECT":  TOKEN_INTERSECT,
	"EXCEPT":     TOKEN_EXCEPT,
	"FOR":        TOKEN_FOR,
	"USING":      TOKEN_USING,
	"OVER":       TOKEN_OVER,
	"PARTITION":  TOKEN_PARTITION,
	"ROWS":       TOKEN_ROWS,
	"RANGE":      TOKEN_RANGE,
	"UNBOUNDED":  TOKEN_UNBOUNDED,
	"PRECEDING":  TOKEN_PRECEDING,
	"FOLLOWING":  TOKEN_FOLLOWING,
	"CURRENT":    TOKEN_CURRENT,
	"ROW":        TOKEN_ROW,
	"BETWEEN":    TOKEN_BETWEEN,
	"IN":         TOKEN_IN,
	"IS":         TOKEN_IS,
	"LIKE":           TOKEN_LIKE,
	"SEMANTIC_MATCH": TOKEN_SEMANTIC_MATCH,
	"FTS_MATCH":      TOKEN_FTS_MATCH,
	"ALTER":          TOKEN_ALTER,

	"ADD":        TOKEN_ADD,
	"COLUMN":     TOKEN_COLUMN,
	"RENAME":     TOKEN_RENAME,
	"TO":         TOKEN_TO,
	"TYPE":       TOKEN_TYPE,
	"DEFAULT":    TOKEN_DEFAULT,
	"CAST":       TOKEN_CAST,
	"CASE":       TOKEN_CASE,
	"WHEN":       TOKEN_WHEN,
	"THEN":       TOKEN_THEN,
	"ELSE":       TOKEN_ELSE,
	"END":        TOKEN_END,
	"COALESCE":   TOKEN_COALESCE,
	"INT":        TOKEN_INT,
	"FLOAT":      TOKEN_FLOAT_TYPE,
	"BOOL":       TOKEN_BOOL,
	"TEXT":       TOKEN_TEXT,
	"VARCHAR":    TOKEN_VARCHAR,
	"DATE":       TOKEN_DATE,
	"TIME":       TOKEN_TIME,
	"DECIMAL":    TOKEN_DECIMAL,
	"ENUM":       TOKEN_ENUM,
	"ARRAY":      TOKEN_ARRAY,
	"VECTOR":     TOKEN_VECTOR,
	"FLEXIBLE":   TOKEN_FLEXIBLE,
	"INFER":      TOKEN_INFER,
	"GENERATED":  TOKEN_GENERATED,
	"ALWAYS":     TOKEN_ALWAYS,
	"SCHEMA":     TOKEN_SCHEMA,
	"VACUUM":     TOKEN_VACUUM,
	"MIGRATION":  TOKEN_MIGRATION,
	"POLICY":     TOKEN_POLICY,
	"ENABLE":     TOKEN_ENABLE,
	"RLS":        TOKEN_RLS,
	"INDEX":      TOKEN_INDEX,
	"INDEXES":    TOKEN_INDEXES,
	"BEGIN":      TOKEN_BEGIN,
	"COMMIT":     TOKEN_COMMIT,
	"ROLLBACK":   TOKEN_ROLLBACK,
	"PREPARE":    TOKEN_PREPARE,
	"EXECUTE":    TOKEN_EXECUTE,
	"DEALLOCATE": TOKEN_DEALLOCATE,
	"APPLY":      TOKEN_APPLY,
	"PREVIEW":    TOKEN_PREVIEW,
}

func LookupIdent(ident string) TokenType {
	if tok, ok := keywords[strings.ToUpper(ident)]; ok {
		return tok
	}
	return TOKEN_IDENT
}

func (t TokenType) String() string {
	switch t {
	case TOKEN_SELECT:
		return "SELECT"
	case TOKEN_INSERT:
		return "INSERT"
	case TOKEN_UPDATE:
		return "UPDATE"
	case TOKEN_DELETE:
		return "DELETE"
	case TOKEN_FROM:
		return "FROM"
	case TOKEN_INTO:
		return "INTO"
	case TOKEN_WHERE:
		return "WHERE"
	case TOKEN_SET:
		return "SET"
	case TOKEN_VALUES:
		return "VALUES"
	case TOKEN_CREATE:
		return "CREATE"
	case TOKEN_DROP:
		return "DROP"
	case TOKEN_DATABASE:
		return "DATABASE"
	case TOKEN_TABLE:
		return "TABLE"
	case TOKEN_USE:
		return "USE"
	case TOKEN_SHOW:
		return "SHOW"
	case TOKEN_DATABASES:
		return "DATABASES"
	case TOKEN_TABLES:
		return "TABLES"
	case TOKEN_DESCRIBE:
		return "DESCRIBE"
	case TOKEN_LIMIT:
		return "LIMIT"
	case TOKEN_EXPLAIN:
		return "EXPLAIN"
	case TOKEN_ANALYZE:
		return "ANALYZE"
	case TOKEN_AS:
		return "AS"
	case TOKEN_OF:
		return "OF"
	case TOKEN_ON:
		return "ON"
	case TOKEN_TIMESTAMP:
		return "TIMESTAMP"
	case TOKEN_VERSION:
		return "VERSION"
	case TOKEN_HISTORY:
		return "HISTORY"
	case TOKEN_KEY:
		return "KEY"
	case TOKEN_AND:
		return "AND"
	case TOKEN_OR:
		return "OR"
	case TOKEN_NOT:
		return "NOT"
	case TOKEN_NULL:
		return "NULL"
	case TOKEN_TRUE:
		return "TRUE"
	case TOKEN_FALSE:
		return "FALSE"
	case TOKEN_ORDER:
		return "ORDER"
	case TOKEN_BY:
		return "BY"
	case TOKEN_ASC:
		return "ASC"
	case TOKEN_DESC:
		return "DESC"
	case TOKEN_OFFSET:
		return "OFFSET"
	case TOKEN_GROUP:
		return "GROUP"
	case TOKEN_HAVING:
		return "HAVING"
	case TOKEN_JOIN:
		return "JOIN"
	case TOKEN_INNER:
		return "INNER"
	case TOKEN_LEFT:
		return "LEFT"
	case TOKEN_RIGHT:
		return "RIGHT"
	case TOKEN_FULL:
		return "FULL"
	case TOKEN_CROSS:
		return "CROSS"
	case TOKEN_UNION:
		return "UNION"
	case TOKEN_INTERSECT:
		return "INTERSECT"
	case TOKEN_EXCEPT:
		return "EXCEPT"
	case TOKEN_OVER:
		return "OVER"
	case TOKEN_PARTITION:
		return "PARTITION"
	case TOKEN_ROWS:
		return "ROWS"
	case TOKEN_PRECEDING:
		return "PRECEDING"
	case TOKEN_FOLLOWING:
		return "FOLLOWING"
	case TOKEN_CURRENT:
		return "CURRENT"
	case TOKEN_ROW:
		return "ROW"
	case TOKEN_BETWEEN:
		return "BETWEEN"
	case TOKEN_IN:
		return "IN"
	case TOKEN_IS:
		return "IS"
	case TOKEN_LIKE:
		return "LIKE"
	case TOKEN_ALTER:
		return "ALTER"
	case TOKEN_ADD:
		return "ADD"
	case TOKEN_COLUMN:
		return "COLUMN"
	case TOKEN_RENAME:
		return "RENAME"
	case TOKEN_TO:
		return "TO"
	case TOKEN_TYPE:
		return "TYPE"
	case TOKEN_DEFAULT:
		return "DEFAULT"
	case TOKEN_INT:
		return "INT"
	case TOKEN_FLOAT_TYPE:
		return "FLOAT"
	case TOKEN_BOOL:
		return "BOOL"
	case TOKEN_TEXT:
		return "TEXT"
	case TOKEN_VARCHAR:
		return "VARCHAR"
	case TOKEN_DATE:
		return "DATE"
	case TOKEN_TIME:
		return "TIME"
	case TOKEN_DECIMAL:
		return "DECIMAL"
	case TOKEN_ENUM:
		return "ENUM"
	case TOKEN_ARRAY:
		return "ARRAY"
	case TOKEN_VECTOR:
		return "VECTOR"
	case TOKEN_VACUUM:
		return "VACUUM"
	case TOKEN_INDEX:
		return "INDEX"
	case TOKEN_INDEXES:
		return "INDEXES"
	case TOKEN_BEGIN:
		return "BEGIN"
	case TOKEN_COMMIT:
		return "COMMIT"
	case TOKEN_ROLLBACK:
		return "ROLLBACK"
	case TOKEN_PREPARE:
		return "PREPARE"
	case TOKEN_EXECUTE:
		return "EXECUTE"
	case TOKEN_DEALLOCATE:
		return "DEALLOCATE"
	case TOKEN_IDENT:
		return "IDENT"
	case TOKEN_INT_LIT:
		return "INT_LIT"
	case TOKEN_FLOAT_LIT:
		return "FLOAT_LIT"
	case TOKEN_STRING_LIT:
		return "STRING_LIT"
	case TOKEN_EQ:
		return "="
	case TOKEN_NEQ:
		return "!="
	case TOKEN_LT:
		return "<"
	case TOKEN_GT:
		return ">"
	case TOKEN_LTE:
		return "<="
	case TOKEN_GTE:
		return ">="
	case TOKEN_COMMA:
		return ","
	case TOKEN_DOT:
		return "."
	case TOKEN_SEMICOLON:
		return ";"
	case TOKEN_LPAREN:
		return "("
	case TOKEN_RPAREN:
		return ")"
	case TOKEN_STAR:
		return "*"
	case TOKEN_MINUS:
		return "-"
	case TOKEN_PLUS:
		return "+"
	case TOKEN_SLASH:
		return "/"
	case TOKEN_PARAM:
		return "PARAM"
	case TOKEN_EOF:
		return "EOF"
	case TOKEN_ILLEGAL:
		return "ILLEGAL"
	default:
		return fmt.Sprintf("TokenType(%d)", int(t))
	}
}
