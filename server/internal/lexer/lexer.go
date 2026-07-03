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
	TOKEN_PRIMARY
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
	TOKEN_ILIKE
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
	TOKEN_STRING_AGG
	TOKEN_SUBSTRING
	TOKEN_WITH
	TOKEN_CONFLICT
	TOKEN_DO
	TOKEN_VIEW
	TOKEN_ALL
	TOKEN_ANY
	TOKEN_SOME
	TOKEN_REFERENCES
	TOKEN_LATERAL
	TOKEN_TRIGGER
	TOKEN_BEFORE
	TOKEN_AFTER
	TOKEN_EACH
	TOKEN_FUNCTION
	TOKEN_RETURNS
	TOKEN_UUID
	TOKEN_INTERVAL
	TOKEN_PROCEDURE
	TOKEN_CALL
	TOKEN_JSONB
	TOKEN_REPLACE
	TOKEN_NOTHING
	TOKEN_RETURNING
	TOKEN_MERGE
	TOKEN_MATCHED
	TOKEN_TRUNCATE
	TOKEN_SAVEPOINT
	TOKEN_RELEASE
	TOKEN_EXISTS
	TOKEN_IF
	TOKEN_ENCRYPTED

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
	TOKEN_STORED
	TOKEN_VIRTUAL
	TOKEN_SCHEMA
	TOKEN_AUTO_INCREMENT
	TOKEN_BIGINT
	TOKEN_NUMERIC
	TOKEN_TIMESTAMPTZ
	TOKEN_SERIAL
	TOKEN_IDENTITY
	TOKEN_BLOB

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
	TOKEN_JSON_CONTAINS
	TOKEN_JSON_CONTAINED_BY
	TOKEN_JSON_HAS_KEY
	TOKEN_JSON_MERGE
	TOKEN_FULLTEXT_MATCH

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
	lineColCache []lineCol
}

type lineCol struct {
	line, col int
}

func New(input string) *Lexer {
	l := &Lexer{input: []rune(input)}
	l.buildLineColCache()
	l.readChar()
	return l
}

func (l *Lexer) buildLineColCache() {
	l.lineColCache = make([]lineCol, len(l.input)+1)
	line, col := 1, 1
	for i := 0; i < len(l.input); i++ {
		l.lineColCache[i] = lineCol{line, col}
		if l.input[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	l.lineColCache[len(l.input)] = lineCol{line, col}
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
	case '@':
		if l.peekChar() == '@' {
			ch := l.ch
			l.readChar()
			tok = l.newToken(TOKEN_FULLTEXT_MATCH, string([]rune{ch, l.ch}), start)
			l.readChar()
		} else if l.peekChar() == '>' {
			ch := l.ch
			l.readChar()
			tok = l.newToken(TOKEN_JSON_CONTAINS, string([]rune{ch, l.ch}), start)
			l.readChar()
		} else {
			tok = l.newToken(TOKEN_ILLEGAL, string(l.ch), start)
			l.readChar()
		}
	case '|':
		if l.peekChar() == '|' {
			ch := l.ch
			l.readChar()
			tok = l.newToken(TOKEN_JSON_MERGE, string([]rune{ch, l.ch}), start)
			l.readChar()
		} else {
			tok = l.newToken(TOKEN_ILLEGAL, string(l.ch), start)
			l.readChar()
		}
	case ',':
		tok = l.newToken(TOKEN_COMMA, ",", start)
		l.readChar()
	case '.':
		tok = l.newToken(TOKEN_DOT, ".", start)
		l.readChar()
	case '?':
		tok = l.newToken(TOKEN_JSON_HAS_KEY, "?", start)
		l.readChar()
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
	case '<':
		if l.peekChar() == '=' {
			ch := l.ch
			l.readChar()
			tok = l.newToken(TOKEN_LTE, string([]rune{ch, l.ch}), start)
			l.readChar()
		} else if l.peekChar() == '@' {
			ch := l.ch
			l.readChar()
			tok = l.newToken(TOKEN_JSON_CONTAINED_BY, string([]rune{ch, l.ch}), start)
			l.readChar()
		} else {
			tok = l.newToken(TOKEN_LT, "<", start)
			l.readChar()
		}
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
		l.ch = 0
	} else {
		l.ch = l.input[l.readPosition]
	}
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
	if position < 0 {
		position = 0
	}
	if position >= len(l.lineColCache) {
		position = len(l.lineColCache) - 1
	}
	lc := l.lineColCache[position]
	return Token{Type: tokenType, Literal: lit, Line: lc.line, Col: lc.col}
}

func isLetter(ch rune) bool {
	return unicode.IsLetter(ch) || ch == '_'
}

func isDigit(ch rune) bool {
	return ch >= '0' && ch <= '9'
}

var keywords = map[string]TokenType{
	"SELECT":         TOKEN_SELECT,
	"INSERT":         TOKEN_INSERT,
	"UPDATE":         TOKEN_UPDATE,
	"DELETE":         TOKEN_DELETE,
	"FROM":           TOKEN_FROM,
	"INTO":           TOKEN_INTO,
	"WHERE":          TOKEN_WHERE,
	"SET":            TOKEN_SET,
	"VALUES":         TOKEN_VALUES,
	"CREATE":         TOKEN_CREATE,
	"DROP":           TOKEN_DROP,
	"DATABASE":       TOKEN_DATABASE,
	"TABLE":          TOKEN_TABLE,
	"USE":            TOKEN_USE,
	"SHOW":           TOKEN_SHOW,
	"DATABASES":      TOKEN_DATABASES,
	"TABLES":         TOKEN_TABLES,
	"DESCRIBE":       TOKEN_DESCRIBE,
	"LIMIT":          TOKEN_LIMIT,
	"EXPLAIN":        TOKEN_EXPLAIN,
	"ANALYZE":        TOKEN_ANALYZE,
	"AS":             TOKEN_AS,
	"OF":             TOKEN_OF,
	"ON":             TOKEN_ON,
	"TIMESTAMP":      TOKEN_TIMESTAMP,
	"HISTORY":        TOKEN_HISTORY,
	"KEY":            TOKEN_KEY,
	"PRIMARY":        TOKEN_PRIMARY,
	"AND":            TOKEN_AND,
	"OR":             TOKEN_OR,
	"NOT":            TOKEN_NOT,
	"NULL":           TOKEN_NULL,
	"TRUE":           TOKEN_TRUE,
	"FALSE":          TOKEN_FALSE,
	"ORDER":          TOKEN_ORDER,
	"BY":             TOKEN_BY,
	"ASC":            TOKEN_ASC,
	"DESC":           TOKEN_DESC,
	"OFFSET":         TOKEN_OFFSET,
	"GROUP":          TOKEN_GROUP,
	"HAVING":         TOKEN_HAVING,
	"JOIN":           TOKEN_JOIN,
	"INNER":          TOKEN_INNER,
	"LEFT":           TOKEN_LEFT,
	"RIGHT":          TOKEN_RIGHT,
	"FULL":           TOKEN_FULL,
	"CROSS":          TOKEN_CROSS,
	"UNION":          TOKEN_UNION,
	"INTERSECT":      TOKEN_INTERSECT,
	"EXCEPT":         TOKEN_EXCEPT,
	"FOR":            TOKEN_FOR,
	"USING":          TOKEN_USING,
	"OVER":           TOKEN_OVER,
	"PARTITION":      TOKEN_PARTITION,
	"ROWS":           TOKEN_ROWS,
	"RANGE":          TOKEN_RANGE,
	"UNBOUNDED":      TOKEN_UNBOUNDED,
	"PRECEDING":      TOKEN_PRECEDING,
	"FOLLOWING":      TOKEN_FOLLOWING,
	"CURRENT":        TOKEN_CURRENT,
	"ROW":            TOKEN_ROW,
	"BETWEEN":        TOKEN_BETWEEN,
	"IN":             TOKEN_IN,
	"IS":             TOKEN_IS,
	"LIKE":           TOKEN_LIKE,
	"ILIKE":          TOKEN_ILIKE,
	"SEMANTIC_MATCH": TOKEN_SEMANTIC_MATCH,
	"FTS_MATCH":      TOKEN_FTS_MATCH,
	"ALTER":          TOKEN_ALTER,

	"ADD":            TOKEN_ADD,
	"COLUMN":         TOKEN_COLUMN,
	"RENAME":         TOKEN_RENAME,
	"TO":             TOKEN_TO,
	"TYPE":           TOKEN_TYPE,
	"DEFAULT":        TOKEN_DEFAULT,
	"CAST":           TOKEN_CAST,
	"CASE":           TOKEN_CASE,
	"WHEN":           TOKEN_WHEN,
	"THEN":           TOKEN_THEN,
	"ELSE":           TOKEN_ELSE,
	"END":            TOKEN_END,
	"COALESCE":       TOKEN_COALESCE,
	"INT":            TOKEN_INT,
	"FLOAT":          TOKEN_FLOAT_TYPE,
	"BOOL":           TOKEN_BOOL,
	"TEXT":           TOKEN_TEXT,
	"VARCHAR":        TOKEN_VARCHAR,
	"DATE":           TOKEN_DATE,
	"TIME":           TOKEN_TIME,
	"DECIMAL":        TOKEN_DECIMAL,
	"ENUM":           TOKEN_ENUM,
	"ARRAY":          TOKEN_ARRAY,
	"VECTOR":         TOKEN_VECTOR,
	"FLEXIBLE":       TOKEN_FLEXIBLE,
	"INFER":          TOKEN_INFER,
	"GENERATED":      TOKEN_GENERATED,
	"ALWAYS":         TOKEN_ALWAYS,
	"STORED":         TOKEN_STORED,
	"VIRTUAL":        TOKEN_VIRTUAL,
	"SCHEMA":         TOKEN_SCHEMA,
	"AUTO_INCREMENT": TOKEN_AUTO_INCREMENT,
	"BIGINT":         TOKEN_BIGINT,
	"NUMERIC":        TOKEN_NUMERIC,
	"TIMESTAMPTZ":    TOKEN_TIMESTAMPTZ,
	"SERIAL":         TOKEN_SERIAL,
	"IDENTITY":       TOKEN_IDENTITY,
	"VACUUM":         TOKEN_VACUUM,
	"MIGRATION":      TOKEN_MIGRATION,
	"POLICY":         TOKEN_POLICY,
	"ENABLE":         TOKEN_ENABLE,
	"RLS":            TOKEN_RLS,
	"INDEX":          TOKEN_INDEX,
	"INDEXES":        TOKEN_INDEXES,
	"BEGIN":          TOKEN_BEGIN,
	"COMMIT":         TOKEN_COMMIT,
	"ROLLBACK":       TOKEN_ROLLBACK,
	"PREPARE":        TOKEN_PREPARE,
	"EXECUTE":        TOKEN_EXECUTE,
	"DEALLOCATE":     TOKEN_DEALLOCATE,
	"APPLY":          TOKEN_APPLY,
	"PREVIEW":        TOKEN_PREVIEW,
	"SUBSTRING":      TOKEN_SUBSTRING,
	"WITH":           TOKEN_WITH,
	"CONFLICT":       TOKEN_CONFLICT,
	"DO":             TOKEN_DO,
	"NOTHING":        TOKEN_NOTHING,
	"VIEW":           TOKEN_VIEW,
	"ALL":            TOKEN_ALL,
	"ANY":            TOKEN_ANY,
	"SOME":           TOKEN_SOME,
	"REFERENCES":     TOKEN_REFERENCES,
	"LATERAL":        TOKEN_LATERAL,
	"TRIGGER":        TOKEN_TRIGGER,
	"BEFORE":         TOKEN_BEFORE,
	"AFTER":          TOKEN_AFTER,
	"EACH":           TOKEN_EACH,
	"FUNCTION":       TOKEN_FUNCTION,
	"RETURNS":        TOKEN_RETURNS,
	"UUID":           TOKEN_UUID,
	"INTERVAL":       TOKEN_INTERVAL,
	"PROCEDURE":      TOKEN_PROCEDURE,
	"CALL":           TOKEN_CALL,
	"JSONB":          TOKEN_JSONB,
	"RETURNING":      TOKEN_RETURNING,
	"MERGE":            TOKEN_MERGE,
	"MATCHED":          TOKEN_MATCHED,
	"TRUNCATE":         TOKEN_TRUNCATE,
	"SAVEPOINT":        TOKEN_SAVEPOINT,
	"RELEASE":          TOKEN_RELEASE,
	"EXISTS":           TOKEN_EXISTS,
	"IF":               TOKEN_IF,
	"BLOB":             TOKEN_BLOB,
	"ENCRYPTED":        TOKEN_ENCRYPTED,
}

func LookupIdent(ident string) TokenType {
	if tok, ok := keywords[strings.ToUpper(ident)]; ok {
		return tok
	}
	return TOKEN_IDENT
}

var tokenNames = [...]string{
	TOKEN_SELECT:            "SELECT",
	TOKEN_INSERT:            "INSERT",
	TOKEN_UPDATE:            "UPDATE",
	TOKEN_DELETE:            "DELETE",
	TOKEN_FROM:              "FROM",
	TOKEN_INTO:              "INTO",
	TOKEN_WHERE:             "WHERE",
	TOKEN_SET:               "SET",
	TOKEN_VALUES:            "VALUES",
	TOKEN_CREATE:            "CREATE",
	TOKEN_DROP:              "DROP",
	TOKEN_DATABASE:          "DATABASE",
	TOKEN_TABLE:             "TABLE",
	TOKEN_USE:               "USE",
	TOKEN_SHOW:              "SHOW",
	TOKEN_DATABASES:         "DATABASES",
	TOKEN_TABLES:            "TABLES",
	TOKEN_DESCRIBE:          "DESCRIBE",
	TOKEN_LIMIT:             "LIMIT",
	TOKEN_EXPLAIN:           "EXPLAIN",
	TOKEN_ANALYZE:           "ANALYZE",
	TOKEN_AS:                "AS",
	TOKEN_OF:                "OF",
	TOKEN_ON:                "ON",
	TOKEN_TIMESTAMP:         "TIMESTAMP",
	TOKEN_VERSION:           "VERSION",
	TOKEN_HISTORY:           "HISTORY",
	TOKEN_KEY:               "KEY",
	TOKEN_AND:               "AND",
	TOKEN_OR:                "OR",
	TOKEN_NOT:               "NOT",
	TOKEN_NULL:              "NULL",
	TOKEN_TRUE:              "TRUE",
	TOKEN_FALSE:             "FALSE",
	TOKEN_ORDER:             "ORDER",
	TOKEN_BY:                "BY",
	TOKEN_ASC:               "ASC",
	TOKEN_DESC:              "DESC",
	TOKEN_OFFSET:            "OFFSET",
	TOKEN_GROUP:             "GROUP",
	TOKEN_HAVING:            "HAVING",
	TOKEN_JOIN:              "JOIN",
	TOKEN_INNER:             "INNER",
	TOKEN_LEFT:              "LEFT",
	TOKEN_RIGHT:             "RIGHT",
	TOKEN_FULL:              "FULL",
	TOKEN_CROSS:             "CROSS",
	TOKEN_UNION:             "UNION",
	TOKEN_INTERSECT:         "INTERSECT",
	TOKEN_EXCEPT:            "EXCEPT",
	TOKEN_OVER:              "OVER",
	TOKEN_PARTITION:         "PARTITION",
	TOKEN_ROWS:              "ROWS",
	TOKEN_PRECEDING:         "PRECEDING",
	TOKEN_FOLLOWING:         "FOLLOWING",
	TOKEN_CURRENT:           "CURRENT",
	TOKEN_ROW:               "ROW",
	TOKEN_BETWEEN:           "BETWEEN",
	TOKEN_IN:                "IN",
	TOKEN_IS:                "IS",
	TOKEN_LIKE:              "LIKE",
	TOKEN_ILIKE:             "ILIKE",
	TOKEN_FOR:               "FOR",
	TOKEN_USING:             "USING",
	TOKEN_SEMANTIC_MATCH:    "SEMANTIC_MATCH",
	TOKEN_FTS_MATCH:         "FTS_MATCH",
	TOKEN_ALTER:             "ALTER",
	TOKEN_ADD:               "ADD",
	TOKEN_COLUMN:            "COLUMN",
	TOKEN_RENAME:            "RENAME",
	TOKEN_TO:                "TO",
	TOKEN_TYPE:              "TYPE",
	TOKEN_DEFAULT:           "DEFAULT",
	TOKEN_END:               "END",
	TOKEN_COALESCE:          "COALESCE",
	TOKEN_INT:               "INT",
	TOKEN_FLOAT_TYPE:        "FLOAT",
	TOKEN_BOOL:              "BOOL",
	TOKEN_TEXT:              "TEXT",
	TOKEN_VARCHAR:           "VARCHAR",
	TOKEN_DATE:              "DATE",
	TOKEN_TIME:              "TIME",
	TOKEN_DECIMAL:           "DECIMAL",
	TOKEN_ENUM:              "ENUM",
	TOKEN_ARRAY:             "ARRAY",
	TOKEN_VECTOR:            "VECTOR",
	TOKEN_FLEXIBLE:          "FLEXIBLE",
	TOKEN_INFER:             "INFER",
	TOKEN_GENERATED:         "GENERATED",
	TOKEN_ALWAYS:            "ALWAYS",
	TOKEN_STORED:            "STORED",
	TOKEN_VIRTUAL:           "VIRTUAL",
	TOKEN_SCHEMA:            "SCHEMA",
	TOKEN_AUTO_INCREMENT:    "AUTO_INCREMENT",
	TOKEN_BIGINT:            "BIGINT",
	TOKEN_NUMERIC:           "NUMERIC",
	TOKEN_TIMESTAMPTZ:       "TIMESTAMPTZ",
	TOKEN_SERIAL:            "SERIAL",
	TOKEN_IDENTITY:          "IDENTITY",
	TOKEN_VACUUM:            "VACUUM",
	TOKEN_MIGRATION:         "MIGRATION",
	TOKEN_POLICY:            "POLICY",
	TOKEN_ENABLE:            "ENABLE",
	TOKEN_RLS:               "RLS",
	TOKEN_INDEX:             "INDEX",
	TOKEN_INDEXES:           "INDEXES",
	TOKEN_BEGIN:             "BEGIN",
	TOKEN_COMMIT:            "COMMIT",
	TOKEN_ROLLBACK:          "ROLLBACK",
	TOKEN_PREPARE:           "PREPARE",
	TOKEN_EXECUTE:           "EXECUTE",
	TOKEN_DEALLOCATE:        "DEALLOCATE",
	TOKEN_SUBSTRING:         "SUBSTRING",
	TOKEN_WITH:              "WITH",
	TOKEN_CONFLICT:          "CONFLICT",
	TOKEN_DO:                "DO",
	TOKEN_NOTHING:           "NOTHING",
	TOKEN_RETURNING:         "RETURNING",
	TOKEN_VIEW:              "VIEW",
	TOKEN_ALL:               "ALL",
	TOKEN_ANY:               "ANY",
	TOKEN_SOME:              "SOME",
	TOKEN_REFERENCES:        "REFERENCES",
	TOKEN_LATERAL:           "LATERAL",
	TOKEN_TRIGGER:           "TRIGGER",
	TOKEN_BEFORE:            "BEFORE",
	TOKEN_AFTER:             "AFTER",
	TOKEN_EACH:              "EACH",
	TOKEN_FUNCTION:          "FUNCTION",
	TOKEN_RETURNS:           "RETURNS",
	TOKEN_UUID:              "UUID",
	TOKEN_INTERVAL:          "INTERVAL",
	TOKEN_PROCEDURE:         "PROCEDURE",
	TOKEN_CALL:              "CALL",
	TOKEN_JSONB:             "JSONB",
	TOKEN_REPLACE:           "REPLACE",
	TOKEN_MERGE:             "MERGE",
	TOKEN_MATCHED:           "MATCHED",
	TOKEN_TRUNCATE:          "TRUNCATE",
	TOKEN_SAVEPOINT:         "SAVEPOINT",
	TOKEN_RELEASE:           "RELEASE",
	TOKEN_EXISTS:            "EXISTS",
	TOKEN_IF:                "IF",
	TOKEN_BLOB:              "BLOB",
	TOKEN_ENCRYPTED:         "ENCRYPTED",
	TOKEN_IDENT:             "IDENT",
	TOKEN_INT_LIT:           "INT_LIT",
	TOKEN_FLOAT_LIT:         "FLOAT_LIT",
	TOKEN_STRING_LIT:        "STRING_LIT",
	TOKEN_EQ:                "=",
	TOKEN_NEQ:               "!=",
	TOKEN_LT:                "<",
	TOKEN_GT:                ">",
	TOKEN_LTE:               "<=",
	TOKEN_GTE:               ">=",
	TOKEN_COMMA:             ",",
	TOKEN_DOT:               ".",
	TOKEN_SEMICOLON:         ";",
	TOKEN_LPAREN:            "(",
	TOKEN_RPAREN:            ")",
	TOKEN_STAR:              "*",
	TOKEN_MINUS:             "-",
	TOKEN_PLUS:              "+",
	TOKEN_SLASH:             "/",
	TOKEN_PARAM:             "PARAM",
	TOKEN_ARROW:             "ARROW",
	TOKEN_DBL_ARROW:         "DBL_ARROW",
	TOKEN_JSON_CONTAINS:     "JSON_CONTAINS",
	TOKEN_JSON_CONTAINED_BY: "JSON_CONTAINED_BY",
	TOKEN_JSON_HAS_KEY:      "JSON_HAS_KEY",
	TOKEN_JSON_MERGE:        "JSON_MERGE",
	TOKEN_FULLTEXT_MATCH:    "FULLTEXT_MATCH",
	TOKEN_EOF:               "EOF",
	TOKEN_ILLEGAL:           "ILLEGAL",
}

func (t TokenType) String() string {
	if int(t) < len(tokenNames) && tokenNames[t] != "" {
		return tokenNames[t]
	}
	return fmt.Sprintf("TokenType(%d)", int(t))
}
