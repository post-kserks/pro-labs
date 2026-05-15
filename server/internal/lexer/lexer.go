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

	// Data types
	TOKEN_INT
	TOKEN_FLOAT_TYPE
	TOKEN_BOOL
	TOKEN_TEXT
	TOKEN_VARCHAR

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
	TOKEN_SEMICOLON
	TOKEN_LPAREN
	TOKEN_RPAREN
	TOKEN_STAR
	TOKEN_MINUS

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
		tok = l.newToken(TOKEN_MINUS, "-", start)
		l.readChar()
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
	"SELECT":    TOKEN_SELECT,
	"INSERT":    TOKEN_INSERT,
	"UPDATE":    TOKEN_UPDATE,
	"DELETE":    TOKEN_DELETE,
	"FROM":      TOKEN_FROM,
	"INTO":      TOKEN_INTO,
	"WHERE":     TOKEN_WHERE,
	"SET":       TOKEN_SET,
	"VALUES":    TOKEN_VALUES,
	"CREATE":    TOKEN_CREATE,
	"DROP":      TOKEN_DROP,
	"DATABASE":  TOKEN_DATABASE,
	"TABLE":     TOKEN_TABLE,
	"USE":       TOKEN_USE,
	"SHOW":      TOKEN_SHOW,
	"DATABASES": TOKEN_DATABASES,
	"TABLES":    TOKEN_TABLES,
	"DESCRIBE":  TOKEN_DESCRIBE,
	"LIMIT":     TOKEN_LIMIT,
	"EXPLAIN":   TOKEN_EXPLAIN,
	"ANALYZE":   TOKEN_ANALYZE,
	"AS":        TOKEN_AS,
	"OF":        TOKEN_OF,
	"TIMESTAMP": TOKEN_TIMESTAMP,
	"VERSION":   TOKEN_VERSION,
	"HISTORY":   TOKEN_HISTORY,
	"KEY":       TOKEN_KEY,
	"AND":       TOKEN_AND,
	"OR":        TOKEN_OR,
	"NOT":       TOKEN_NOT,
	"NULL":      TOKEN_NULL,
	"TRUE":      TOKEN_TRUE,
	"FALSE":     TOKEN_FALSE,
	"INT":       TOKEN_INT,
	"FLOAT":     TOKEN_FLOAT_TYPE,
	"BOOL":      TOKEN_BOOL,
	"TEXT":      TOKEN_TEXT,
	"VARCHAR":   TOKEN_VARCHAR,
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
	case TOKEN_EOF:
		return "EOF"
	case TOKEN_ILLEGAL:
		return "ILLEGAL"
	default:
		return fmt.Sprintf("TokenType(%d)", int(t))
	}
}
