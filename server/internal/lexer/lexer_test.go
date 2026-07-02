package lexer

import (
	"strings"
	"testing"
)

// Helper: collect all tokens until EOF
func lexAll(input string) []Token {
	l := New(input)
	var tokens []Token
	for {
		tok := l.NextToken()
		tokens = append(tokens, tok)
		if tok.Type == TOKEN_EOF {
			break
		}
	}
	return tokens
}

// Helper: collect only non-EOF tokens
func lexNonEOF(input string) []Token {
	all := lexAll(input)
	return all[:len(all)-1] // strip EOF
}

func TestLexerBasic(t *testing.T) {
	sql := "SeLeCt id, name FROM heroes WHERE level >= -3 AND alive = TRUE;"
	l := New(sql)

	want := []TokenType{
		TOKEN_SELECT,
		TOKEN_IDENT,
		TOKEN_COMMA,
		TOKEN_IDENT,
		TOKEN_FROM,
		TOKEN_IDENT,
		TOKEN_WHERE,
		TOKEN_IDENT,
		TOKEN_GTE,
		TOKEN_INT_LIT,
		TOKEN_AND,
		TOKEN_IDENT,
		TOKEN_EQ,
		TOKEN_TRUE,
		TOKEN_SEMICOLON,
		TOKEN_EOF,
	}

	for i, tokenType := range want {
		tok := l.NextToken()
		if tok.Type != tokenType {
			t.Fatalf("token[%d]: expected %s, got %s (%q)", i, tokenType, tok.Type, tok.Literal)
		}
	}
}

func TestLexerStringLiteral(t *testing.T) {
	sql := "INSERT INTO heroes VALUES ('Legolas\\'s bow', 9);"
	l := New(sql)

	for {
		tok := l.NextToken()
		if tok.Type == TOKEN_STRING_LIT {
			if tok.Literal != "Legolas's bow" {
				t.Fatalf("unexpected string literal: %q", tok.Literal)
			}
			return
		}
		if tok.Type == TOKEN_EOF {
			t.Fatal("string literal token not found")
		}
	}
}

func TestLexerExplainAndTimeTravelTokens(t *testing.T) {
	sql := "EXPLAIN ANALYZE SELECT * FROM heroes AS OF TIMESTAMP '2025-08-01 12:00:00' WHERE id = 1;"
	l := New(sql)

	want := []TokenType{
		TOKEN_EXPLAIN,
		TOKEN_ANALYZE,
		TOKEN_SELECT,
		TOKEN_STAR,
		TOKEN_FROM,
		TOKEN_IDENT,
		TOKEN_AS,
		TOKEN_OF,
		TOKEN_TIMESTAMP,
		TOKEN_STRING_LIT,
		TOKEN_WHERE,
		TOKEN_IDENT,
		TOKEN_EQ,
		TOKEN_INT_LIT,
		TOKEN_SEMICOLON,
		TOKEN_EOF,
	}

	for i, tokenType := range want {
		tok := l.NextToken()
		if tok.Type != tokenType {
			t.Fatalf("token[%d]: expected %s, got %s (%q)", i, tokenType, tok.Type, tok.Literal)
		}
	}
}

// --- Operator edge cases ---

func TestAllOperatorTokens(t *testing.T) {
	tests := []struct {
		input string
		want  TokenType
		lit   string
	}{
		{"=", TOKEN_EQ, "="},
		{"!=", TOKEN_NEQ, "!="},
		{"<", TOKEN_LT, "<"},
		{">", TOKEN_GT, ">"},
		{"<=", TOKEN_LTE, "<="},
		{">=", TOKEN_GTE, ">="},
		{"->", TOKEN_ARROW, "->"},
		{"->>", TOKEN_DBL_ARROW, "->>"},
		{"@>", TOKEN_JSON_CONTAINS, "@>"},
		{"<@", TOKEN_JSON_CONTAINED_BY, "<@"},
		{"?", TOKEN_JSON_HAS_KEY, "?"},
		{"||", TOKEN_JSON_MERGE, "||"},
		{"@@", TOKEN_FULLTEXT_MATCH, "@@"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tok := New(tt.input).NextToken()
			if tok.Type != tt.want {
				t.Errorf("input %q: got type %s, want %s", tt.input, tok.Type, tt.want)
			}
			if tok.Literal != tt.lit {
				t.Errorf("input %q: got literal %q, want %q", tt.input, tok.Literal, tt.lit)
			}
		})
	}
}

func TestOperatorWithoutContext(t *testing.T) {
	// Single | should be illegal
	tok := New("|").NextToken()
	if tok.Type != TOKEN_ILLEGAL {
		t.Errorf("single |: expected ILLEGAL, got %s", tok.Type)
	}
	// Single @ should be illegal
	tok = New("@").NextToken()
	if tok.Type != TOKEN_ILLEGAL {
		t.Errorf("single @: expected ILLEGAL, got %s", tok.Type)
	}
}

func TestOperatorsInExpression(t *testing.T) {
	input := "a=b AND c!=d AND e<f AND g<=h AND i>j AND k>=l AND m->n AND o->>p AND q@>r AND s<@t AND u?v AND w||x"
	tokens := lexNonEOF(input)
	expected := []TokenType{
		TOKEN_IDENT, TOKEN_EQ, TOKEN_IDENT,
		TOKEN_AND,
		TOKEN_IDENT, TOKEN_NEQ, TOKEN_IDENT,
		TOKEN_AND,
		TOKEN_IDENT, TOKEN_LT, TOKEN_IDENT,
		TOKEN_AND,
		TOKEN_IDENT, TOKEN_LTE, TOKEN_IDENT,
		TOKEN_AND,
		TOKEN_IDENT, TOKEN_GT, TOKEN_IDENT,
		TOKEN_AND,
		TOKEN_IDENT, TOKEN_GTE, TOKEN_IDENT,
		TOKEN_AND,
		TOKEN_IDENT, TOKEN_ARROW, TOKEN_IDENT,
		TOKEN_AND,
		TOKEN_IDENT, TOKEN_DBL_ARROW, TOKEN_IDENT,
		TOKEN_AND,
		TOKEN_IDENT, TOKEN_JSON_CONTAINS, TOKEN_IDENT,
		TOKEN_AND,
		TOKEN_IDENT, TOKEN_JSON_CONTAINED_BY, TOKEN_IDENT,
		TOKEN_AND,
		TOKEN_IDENT, TOKEN_JSON_HAS_KEY, TOKEN_IDENT,
		TOKEN_AND,
		TOKEN_IDENT, TOKEN_JSON_MERGE, TOKEN_IDENT,
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d] %q: got %s, want %s", i, tokens[i].Literal, tokens[i].Type, want)
		}
	}
}

// --- String edge cases ---

func TestStringEdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		input string
		lit   string
	}{
		{"empty string", "''", ""},
		{"basic", "'hello'", "hello"},
		{"escaped quote backslash", `'it\'s'`, "it's"},
		{"backslash-quote", `'it\'s'`, "it's"},
		{"escaped newline", `'\n'`, "\n"},
		{"escaped tab", `'\t'`, "\t"},
		{"escaped cr", `'\r'`, "\r"},
		{"escaped backslash", `'\\'`, "\\"},
		{"unicode", "'\u00e9\u00e8\u00ea'", "\u00e9\u00e8\u00ea"},
		{"emoji", "'\U0001f600'", "\U0001f600"},
		{"mixed escapes", `'line1\nline2\ttab'`, "line1\nline2\ttab"},
		{"double backslash-quote", `'\\"'`, `\"`},
		{"string with spaces", "'hello world'", "hello world"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := lexNonEOF(tt.input)
			if len(tokens) != 1 || tokens[0].Type != TOKEN_STRING_LIT {
				t.Fatalf("expected 1 STRING_LIT, got %d tokens: %v", len(tokens), tokens)
			}
			if tokens[0].Literal != tt.lit {
				t.Errorf("got literal %q, want %q", tokens[0].Literal, tt.lit)
			}
		})
	}
}

func TestStringWithUnicodeEscapes(t *testing.T) {
	// The lexer handles \n, \t, \r, \\, \' — other backslash sequences pass through literal
	tokens := lexNonEOF(`'abc\ndef'`)
	if tokens[0].Literal != "abc\ndef" {
		t.Errorf("got %q, want %q", tokens[0].Literal, "abc\ndef")
	}
}

func TestStringUnterminated(t *testing.T) {
	tokens := lexNonEOF("'hello world")
	if len(tokens) != 1 || tokens[0].Type != TOKEN_ILLEGAL {
		t.Errorf("expected ILLEGAL for unterminated string, got %v", tokens)
	}
	if !strings.Contains(tokens[0].Literal, "unterminated") {
		t.Errorf("expected 'unterminated' in literal, got %q", tokens[0].Literal)
	}
}

func TestStringUnterminatedAtEOF(t *testing.T) {
	// Unterminated string at very end of input
	tokens := lexAll("SELECT 'abc")
	// Should have EOF eventually, but the string should produce an ILLEGAL
	found := false
	for _, tok := range tokens {
		if tok.Type == TOKEN_ILLEGAL {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected ILLEGAL token for unterminated string")
	}
}

func TestStringEmptyAfterWhitespace(t *testing.T) {
	tokens := lexNonEOF("  '  '  ")
	if len(tokens) != 1 || tokens[0].Type != TOKEN_STRING_LIT {
		t.Fatalf("expected 1 STRING_LIT, got %v", tokens)
	}
	if tokens[0].Literal != "  " {
		t.Errorf("got %q, want %q", tokens[0].Literal, "  ")
	}
}

func TestStringOnlyEscapes(t *testing.T) {
	tokens := lexNonEOF(`'\n\t\r\\\'`)
	// This is unterminated because closing ' is missing
	if tokens[0].Type != TOKEN_ILLEGAL {
		t.Errorf("expected ILLEGAL for unterminated string with escapes, got %s", tokens[0].Type)
	}
}

func TestStringUnicodeMultiByte(t *testing.T) {
	input := "'\u4e16\u754c'" // 世界
	tokens := lexNonEOF(input)
	if tokens[0].Literal != "\u4e16\u754c" {
		t.Errorf("got %q, want %q", tokens[0].Literal, "\u4e16\u754c")
	}
}

// --- Number edge cases ---

func TestNumberEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		lit     string
		isFloat bool
	}{
		{"zero", "0", "0", false},
		{"positive int", "42", "42", false},
		{"negative int", "-7", "-7", false},
		{"float", "3.14", "3.14", true},
		{"negative float", "-2.5", "-2.5", true},
		{"large int", "999999999999999999", "999999999999999999", false},
		{"negative zero", "-0", "-0", false},
		{"float with trailing dot", "5.", "5.", true},
		{"int in expression", "1+2", "1", false},
		{"float in expression", "1.0+2.5", "1.0", true},
		{"leading zeros", "007", "007", false},
		{"negative large", "-999999999", "-999999999", false},
		{"negative float large", "-9999999.999", "-9999999.999", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tok := New(tt.input).NextToken()
			if tok.Literal != tt.lit {
				t.Errorf("got literal %q, want %q", tok.Literal, tt.lit)
			}
			if tt.isFloat {
				if tok.Type != TOKEN_FLOAT_LIT {
					t.Errorf("expected FLOAT_LIT, got %s", tok.Type)
				}
			} else {
				if tok.Type != TOKEN_INT_LIT {
					t.Errorf("expected INT_LIT, got %s", tok.Type)
				}
			}
		})
	}
}

func TestNumberFloatPrecision(t *testing.T) {
	// Very precise float
	tokens := lexNonEOF("3.14159265358979323846")
	if tokens[0].Type != TOKEN_FLOAT_LIT {
		t.Errorf("expected FLOAT_LIT, got %s", tokens[0].Type)
	}
	if tokens[0].Literal != "3.14159265358979323846" {
		t.Errorf("got %q", tokens[0].Literal)
	}
}

func TestNumberMinusFollowedByNonDigit(t *testing.T) {
	// - followed by letter should be MINUS then IDENT
	tokens := lexNonEOF("-foo")
	expected := []TokenType{TOKEN_MINUS, TOKEN_IDENT}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d]: got %s, want %s", i, tokens[i].Type, want)
		}
	}
}

func TestNumberMinusAlone(t *testing.T) {
	tokens := lexNonEOF("-")
	if tokens[0].Type != TOKEN_MINUS {
		t.Errorf("expected MINUS, got %s", tokens[0].Type)
	}
}

func TestNumberDots(t *testing.T) {
	// Multiple dots: should lex as DOT DOT DOT
	tokens := lexNonEOF("1.2.3")
	expected := []TokenType{TOKEN_FLOAT_LIT, TOKEN_DOT, TOKEN_INT_LIT}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d: %v", len(expected), len(tokens), tokens)
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d]: got %s (%q), want %s", i, tokens[i].Type, tokens[i].Literal, want)
		}
	}
}

// --- Keyword token tests ---

func TestAllKeywords(t *testing.T) {
	kwTests := []struct {
		input string
		want  TokenType
	}{
		{"SELECT", TOKEN_SELECT},
		{"INSERT", TOKEN_INSERT},
		{"UPDATE", TOKEN_UPDATE},
		{"DELETE", TOKEN_DELETE},
		{"FROM", TOKEN_FROM},
		{"INTO", TOKEN_INTO},
		{"WHERE", TOKEN_WHERE},
		{"SET", TOKEN_SET},
		{"VALUES", TOKEN_VALUES},
		{"CREATE", TOKEN_CREATE},
		{"DROP", TOKEN_DROP},
		{"DATABASE", TOKEN_DATABASE},
		{"TABLE", TOKEN_TABLE},
		{"USE", TOKEN_USE},
		{"SHOW", TOKEN_SHOW},
		{"DATABASES", TOKEN_DATABASES},
		{"TABLES", TOKEN_TABLES},
		{"DESCRIBE", TOKEN_DESCRIBE},
		{"LIMIT", TOKEN_LIMIT},
		{"EXPLAIN", TOKEN_EXPLAIN},
		{"ANALYZE", TOKEN_ANALYZE},
		{"AS", TOKEN_AS},
		{"OF", TOKEN_OF},
		{"ON", TOKEN_ON},
		{"TIMESTAMP", TOKEN_TIMESTAMP},
		{"HISTORY", TOKEN_HISTORY},
		{"KEY", TOKEN_KEY},
		{"PRIMARY", TOKEN_PRIMARY},
		{"AND", TOKEN_AND},
		{"OR", TOKEN_OR},
		{"NOT", TOKEN_NOT},
		{"NULL", TOKEN_NULL},
		{"TRUE", TOKEN_TRUE},
		{"FALSE", TOKEN_FALSE},
		{"ORDER", TOKEN_ORDER},
		{"BY", TOKEN_BY},
		{"ASC", TOKEN_ASC},
		{"DESC", TOKEN_DESC},
		{"OFFSET", TOKEN_OFFSET},
		{"GROUP", TOKEN_GROUP},
		{"HAVING", TOKEN_HAVING},
		{"JOIN", TOKEN_JOIN},
		{"INNER", TOKEN_INNER},
		{"LEFT", TOKEN_LEFT},
		{"RIGHT", TOKEN_RIGHT},
		{"FULL", TOKEN_FULL},
		{"CROSS", TOKEN_CROSS},
		{"UNION", TOKEN_UNION},
		{"INTERSECT", TOKEN_INTERSECT},
		{"EXCEPT", TOKEN_EXCEPT},
		{"FOR", TOKEN_FOR},
		{"USING", TOKEN_USING},
		{"OVER", TOKEN_OVER},
		{"PARTITION", TOKEN_PARTITION},
		{"ROWS", TOKEN_ROWS},
		{"RANGE", TOKEN_RANGE},
		{"UNBOUNDED", TOKEN_UNBOUNDED},
		{"PRECEDING", TOKEN_PRECEDING},
		{"FOLLOWING", TOKEN_FOLLOWING},
		{"CURRENT", TOKEN_CURRENT},
		{"ROW", TOKEN_ROW},
		{"BETWEEN", TOKEN_BETWEEN},
		{"IN", TOKEN_IN},
		{"IS", TOKEN_IS},
		{"LIKE", TOKEN_LIKE},
		{"SEMANTIC_MATCH", TOKEN_SEMANTIC_MATCH},
		{"FTS_MATCH", TOKEN_FTS_MATCH},
		{"ALTER", TOKEN_ALTER},
		{"ADD", TOKEN_ADD},
		{"COLUMN", TOKEN_COLUMN},
		{"RENAME", TOKEN_RENAME},
		{"TO", TOKEN_TO},
		{"TYPE", TOKEN_TYPE},
		{"DEFAULT", TOKEN_DEFAULT},
		{"CAST", TOKEN_CAST},
		{"CASE", TOKEN_CASE},
		{"WHEN", TOKEN_WHEN},
		{"THEN", TOKEN_THEN},
		{"ELSE", TOKEN_ELSE},
		{"END", TOKEN_END},
		{"COALESCE", TOKEN_COALESCE},
		{"INT", TOKEN_INT},
		{"FLOAT", TOKEN_FLOAT_TYPE},
		{"BOOL", TOKEN_BOOL},
		{"TEXT", TOKEN_TEXT},
		{"VARCHAR", TOKEN_VARCHAR},
		{"DATE", TOKEN_DATE},
		{"TIME", TOKEN_TIME},
		{"DECIMAL", TOKEN_DECIMAL},
		{"ENUM", TOKEN_ENUM},
		{"ARRAY", TOKEN_ARRAY},
		{"VECTOR", TOKEN_VECTOR},
		{"FLEXIBLE", TOKEN_FLEXIBLE},
		{"INFER", TOKEN_INFER},
		{"GENERATED", TOKEN_GENERATED},
		{"ALWAYS", TOKEN_ALWAYS},
		{"SCHEMA", TOKEN_SCHEMA},
		{"AUTO_INCREMENT", TOKEN_AUTO_INCREMENT},
		{"VACUUM", TOKEN_VACUUM},
		{"MIGRATION", TOKEN_MIGRATION},
		{"POLICY", TOKEN_POLICY},
		{"ENABLE", TOKEN_ENABLE},
		{"RLS", TOKEN_RLS},
		{"INDEX", TOKEN_INDEX},
		{"INDEXES", TOKEN_INDEXES},
		{"BEGIN", TOKEN_BEGIN},
		{"COMMIT", TOKEN_COMMIT},
		{"ROLLBACK", TOKEN_ROLLBACK},
		{"PREPARE", TOKEN_PREPARE},
		{"EXECUTE", TOKEN_EXECUTE},
		{"DEALLOCATE", TOKEN_DEALLOCATE},
		{"APPLY", TOKEN_APPLY},
		{"PREVIEW", TOKEN_PREVIEW},
		{"SUBSTRING", TOKEN_SUBSTRING},
		{"WITH", TOKEN_WITH},
		{"CONFLICT", TOKEN_CONFLICT},
		{"DO", TOKEN_DO},
		{"NOTHING", TOKEN_NOTHING},
		{"VIEW", TOKEN_VIEW},
		{"ALL", TOKEN_ALL},
		{"ANY", TOKEN_ANY},
		{"SOME", TOKEN_SOME},
		{"REFERENCES", TOKEN_REFERENCES},
		{"LATERAL", TOKEN_LATERAL},
		{"TRIGGER", TOKEN_TRIGGER},
		{"BEFORE", TOKEN_BEFORE},
		{"AFTER", TOKEN_AFTER},
		{"EACH", TOKEN_EACH},
		{"FUNCTION", TOKEN_FUNCTION},
		{"RETURNS", TOKEN_RETURNS},
		{"UUID", TOKEN_UUID},
		{"INTERVAL", TOKEN_INTERVAL},
		{"PROCEDURE", TOKEN_PROCEDURE},
		{"CALL", TOKEN_CALL},
		{"JSONB", TOKEN_JSONB},
		{"NOTHING", TOKEN_NOTHING},
		{"RETURNING", TOKEN_RETURNING},
		{"MERGE", TOKEN_MERGE},
		{"MATCHED", TOKEN_MATCHED},
		{"TRUNCATE", TOKEN_TRUNCATE},
		{"SAVEPOINT", TOKEN_SAVEPOINT},
		{"RELEASE", TOKEN_RELEASE},
		{"EXISTS", TOKEN_EXISTS},
	}
	for _, tt := range kwTests {
		t.Run(tt.input, func(t *testing.T) {
			tok := New(tt.input).NextToken()
			if tok.Type != tt.want {
				t.Errorf("keyword %q: got %s, want %s", tt.input, tok.Type, tt.want)
			}
			if tok.Literal != tt.input {
				t.Errorf("keyword %q: literal is %q", tt.input, tok.Literal)
			}
		})
	}
}

func TestKeywordCaseInsensitive(t *testing.T) {
	cases := []struct{ input, want string }{
		{"select", "SELECT"},
		{"Select", "SELECT"},
		{"sElEcT", "SELECT"},
		{"from", "FROM"},
		{"WHERE", "WHERE"},
		{"Create", "CREATE"},
		{"TABLE", "TABLE"},
	}
	for _, tt := range cases {
		t.Run(tt.input, func(t *testing.T) {
			tok := New(tt.input).NextToken()
			if tok.Type == TOKEN_IDENT {
				t.Errorf("%q should be keyword %s, got IDENT", tt.input, tt.want)
			}
		})
	}
}

func TestKeywordPartialMatch(t *testing.T) {
	// "SEL" is not a keyword, should be IDENT
	tok := New("SEL").NextToken()
	if tok.Type != TOKEN_IDENT {
		t.Errorf("SEL: expected IDENT, got %s", tok.Type)
	}
	// "SELECTX" is not a keyword
	tok = New("SELECTX").NextToken()
	if tok.Type != TOKEN_IDENT {
		t.Errorf("SELECTX: expected IDENT, got %s", tok.Type)
	}
}

func TestKeywordInContext(t *testing.T) {
	input := "SELECT id FROM users WHERE name IS NOT NULL ORDER BY id ASC LIMIT 10"
	tokens := lexNonEOF(input)
	expected := []TokenType{
		TOKEN_SELECT, TOKEN_IDENT, TOKEN_FROM, TOKEN_IDENT,
		TOKEN_WHERE, TOKEN_IDENT, TOKEN_IS, TOKEN_NOT, TOKEN_NULL,
		TOKEN_ORDER, TOKEN_BY, TOKEN_IDENT, TOKEN_ASC,
		TOKEN_LIMIT, TOKEN_INT_LIT,
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d] %q: got %s, want %s", i, tokens[i].Literal, tokens[i].Type, want)
		}
	}
}

// --- Error handling ---

func TestIllegalCharacter(t *testing.T) {
	// # is not a valid character
	tok := New("#").NextToken()
	if tok.Type != TOKEN_ILLEGAL {
		t.Errorf("#: expected ILLEGAL, got %s", tok.Type)
	}
}

func TestIllegalCharacters(t *testing.T) {
	tests := []string{"#", "^", "~", "`", "\\"}
	for _, ch := range tests {
		t.Run(ch, func(t *testing.T) {
			tok := New(ch).NextToken()
			if tok.Type != TOKEN_ILLEGAL {
				t.Errorf("%q: expected ILLEGAL, got %s", ch, tok.Type)
			}
		})
	}
}

func TestBangAlone(t *testing.T) {
	// ! alone (without =) is illegal
	tok := New("!").NextToken()
	if tok.Type != TOKEN_ILLEGAL {
		t.Errorf("!: expected ILLEGAL, got %s", tok.Type)
	}
}

func TestDollarWithoutNumber(t *testing.T) {
	// $ alone (no digits after) is illegal
	tok := New("$").NextToken()
	if tok.Type != TOKEN_ILLEGAL {
		t.Errorf("$: expected ILLEGAL, got %s", tok.Type)
	}
}

func TestDollarParam(t *testing.T) {
	tokens := lexNonEOF("$1 $12 $100")
	expected := []TokenType{TOKEN_PARAM, TOKEN_PARAM, TOKEN_PARAM}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d]: got %s, want %s", i, tokens[i].Type, want)
		}
	}
	if tokens[0].Literal != "1" {
		t.Errorf("param literal: got %q, want %q", tokens[0].Literal, "1")
	}
	if tokens[1].Literal != "12" {
		t.Errorf("param literal: got %q, want %q", tokens[1].Literal, "12")
	}
}

func TestEmptyInput(t *testing.T) {
	tok := New("").NextToken()
	if tok.Type != TOKEN_EOF {
		t.Errorf("empty input: expected EOF, got %s", tok.Type)
	}
}

func TestWhitespaceOnly(t *testing.T) {
	tok := New("   \t\n\r  ").NextToken()
	if tok.Type != TOKEN_EOF {
		t.Errorf("whitespace: expected EOF, got %s", tok.Type)
	}
}

func TestMultipleNewlines(t *testing.T) {
	input := "SELECT\n\n\nid\n\n\nFROM\n\n\nusers"
	tokens := lexNonEOF(input)
	expected := []TokenType{
		TOKEN_SELECT, TOKEN_IDENT, TOKEN_FROM, TOKEN_IDENT,
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d]: got %s, want %s", i, tokens[i].Type, want)
		}
	}
}

// --- Line/column tracking ---

func TestLineColumnTracking(t *testing.T) {
	// "SELECT\n  id" — id is on line 2, col 3
	input := "SELECT\n  id"
	tokens := lexNonEOF(input)
	// SELECT is line 1, col 1
	if tokens[0].Line != 1 || tokens[0].Col != 1 {
		t.Errorf("SELECT: line=%d col=%d, want 1:1", tokens[0].Line, tokens[0].Col)
	}
	// id is line 2, col 3
	if tokens[1].Line != 2 || tokens[1].Col != 3 {
		t.Errorf("id: line=%d col=%d, want 2:3", tokens[1].Line, tokens[1].Col)
	}
}

func TestLineColumnMultipleStatements(t *testing.T) {
	input := "SELECT 1;\nSELECT 2;"
	tokens := lexNonEOF(input)
	// Second SELECT should be on line 2
	for _, tok := range tokens {
		if tok.Literal == "SELECT" && tok.Line == 2 {
			return
		}
	}
	t.Error("second SELECT not found on line 2")
}

// --- Identifier edge cases ---

func TestIdentifierWithUnderscore(t *testing.T) {
	tokens := lexNonEOF("_foo bar_")
	expected := []TokenType{TOKEN_IDENT, TOKEN_IDENT}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	if tokens[0].Literal != "_foo" {
		t.Errorf("got %q, want %q", tokens[0].Literal, "_foo")
	}
	if tokens[1].Literal != "bar_" {
		t.Errorf("got %q, want %q", tokens[1].Literal, "bar_")
	}
}

func TestIdentifierWithDigits(t *testing.T) {
	tokens := lexNonEOF("abc123")
	if tokens[0].Type != TOKEN_IDENT {
		t.Errorf("expected IDENT, got %s", tokens[0].Type)
	}
	if tokens[0].Literal != "abc123" {
		t.Errorf("got %q, want %q", tokens[0].Literal, "abc123")
	}
}

func TestIdentifierStartsWithDigit(t *testing.T) {
	// Starts with digit -> should be number, not identifier
	tokens := lexNonEOF("123abc")
	if tokens[0].Type != TOKEN_INT_LIT {
		t.Errorf("expected INT_LIT, got %s", tokens[0].Type)
	}
}

func TestLookupIdent(t *testing.T) {
	if LookupIdent("SELECT") != TOKEN_SELECT {
		t.Error("LookupIdent SELECT failed")
	}
	if LookupIdent("select") != TOKEN_SELECT {
		t.Error("LookupIdent select (lowercase) failed")
	}
	if LookupIdent("foobar") != TOKEN_IDENT {
		t.Error("LookupIdent foobar should be IDENT")
	}
	if LookupIdent("") != TOKEN_IDENT {
		t.Error("LookupIdent empty should be IDENT")
	}
}

// --- Complex queries ---

func TestComplexQueryTokenization(t *testing.T) {
	input := `SELECT u.id, u.name, COUNT(o.id) AS order_count
FROM users u
LEFT JOIN orders o ON u.id = o.user_id
WHERE u.active = TRUE AND o.created_at >= '2024-01-01'
GROUP BY u.id, u.name
HAVING COUNT(o.id) > 5
ORDER BY order_count DESC
LIMIT 10 OFFSET 20;`
	tokens := lexNonEOF(input)
	// Just verify it doesn't crash and produces reasonable tokens
	if len(tokens) < 20 {
		t.Errorf("expected many tokens, got %d", len(tokens))
	}
	// Check first and last meaningful tokens
	if tokens[0].Type != TOKEN_SELECT {
		t.Errorf("first token: got %s", tokens[0].Type)
	}
	if tokens[len(tokens)-1].Type != TOKEN_SEMICOLON {
		t.Errorf("last token: got %s", tokens[len(tokens)-1].Type)
	}
}

func TestCTETokenization(t *testing.T) {
	input := `WITH cte AS (SELECT 1) SELECT * FROM cte`
	tokens := lexNonEOF(input)
	expected := []TokenType{
		TOKEN_WITH, TOKEN_IDENT, TOKEN_AS,
		TOKEN_LPAREN, TOKEN_SELECT, TOKEN_INT_LIT, TOKEN_RPAREN,
		TOKEN_SELECT, TOKEN_STAR, TOKEN_FROM, TOKEN_IDENT,
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d] %q: got %s, want %s", i, tokens[i].Literal, tokens[i].Type, want)
		}
	}
}

func TestSubqueryTokenization(t *testing.T) {
	input := `SELECT * FROM (SELECT id FROM users) sub`
	tokens := lexNonEOF(input)
	expected := []TokenType{
		TOKEN_SELECT, TOKEN_STAR, TOKEN_FROM,
		TOKEN_LPAREN, TOKEN_SELECT, TOKEN_IDENT, TOKEN_FROM, TOKEN_IDENT, TOKEN_RPAREN,
		TOKEN_IDENT,
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d]: got %s, want %s", i, tokens[i].Type, want)
		}
	}
}

func TestWindowFunctionTokenization(t *testing.T) {
	input := `SELECT ROW_NUMBER() OVER (PARTITION BY dept ORDER BY salary DESC) AS rn FROM employees`
	tokens := lexNonEOF(input)
	if tokens[0].Type != TOKEN_SELECT {
		t.Error("expected SELECT")
	}
	// Check WINDOW-specific tokens
	found := map[TokenType]bool{}
	for _, tok := range tokens {
		found[tok.Type] = true
	}
	for _, tt := range []TokenType{TOKEN_OVER, TOKEN_PARTITION, TOKEN_BY, TOKEN_ORDER, TOKEN_DESC} {
		if !found[tt] {
			t.Errorf("missing token %s in window function", tt)
		}
	}
}

// --- TokenType.String() ---

func TestTokenTypeString(t *testing.T) {
	tests := []struct {
		tt   TokenType
		want string
	}{
		{TOKEN_SELECT, "SELECT"},
		{TOKEN_EOF, "EOF"},
		{TOKEN_ILLEGAL, "ILLEGAL"},
		{TOKEN_EQ, "="},
		{TOKEN_IDENT, "IDENT"},
		{TokenType(99999), "TokenType(99999)"},
	}
	for _, tt := range tests {
		if got := tt.tt.String(); got != tt.want {
			t.Errorf("TokenType(%d).String() = %q, want %q", int(tt.tt), got, tt.want)
		}
	}
}

// --- Token struct fields ---

func TestTokenFields(t *testing.T) {
	input := "hello"
	tokens := lexNonEOF(input)
	tok := tokens[0]
	if tok.Literal != "hello" {
		t.Errorf("literal: got %q", tok.Literal)
	}
	if tok.Type != TOKEN_IDENT {
		t.Errorf("type: got %s", tok.Type)
	}
	if tok.Line != 1 {
		t.Errorf("line: got %d", tok.Line)
	}
	if tok.Col != 1 {
		t.Errorf("col: got %d", tok.Col)
	}
}

func TestNewTokenBoundaryClamp(t *testing.T) {
	// newToken with out-of-bounds position should clamp
	l := New("x")
	// Position beyond input length
	tok := l.newToken(TOKEN_IDENT, "test", 99999)
	if tok.Type != TOKEN_IDENT {
		t.Error("clamped token failed")
	}
	// Negative position
	tok = l.newToken(TOKEN_IDENT, "test", -5)
	if tok.Type != TOKEN_IDENT {
		t.Error("negative position token failed")
	}
}

// --- Additional operator edge cases ---

func TestArrowChain(t *testing.T) {
	// ->> should not be parsed as -> then >
	tokens := lexNonEOF("a->>b")
	expected := []TokenType{TOKEN_IDENT, TOKEN_DBL_ARROW, TOKEN_IDENT}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d: %v", len(expected), len(tokens), tokens)
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d]: got %s, want %s", i, tokens[i].Type, want)
		}
	}
}

func TestArrowThenGt(t *testing.T) {
	// -> followed by > should be ARROW then GT
	tokens := lexNonEOF("a->> b")
	// Actually ->> is DBL_ARROW, not ARROW + GT
	expected := []TokenType{TOKEN_IDENT, TOKEN_DBL_ARROW, TOKEN_IDENT}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d: %v", len(expected), len(tokens), tokens)
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d]: got %s, want %s", i, tokens[i].Type, want)
		}
	}
}

func TestLtAtContains(t *testing.T) {
	// <@ should be JSON_CONTAINED_BY
	tokens := lexNonEOF("a<@b")
	expected := []TokenType{TOKEN_IDENT, TOKEN_JSON_CONTAINED_BY, TOKEN_IDENT}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	if tokens[1].Type != TOKEN_JSON_CONTAINED_BY {
		t.Errorf("got %s, want JSON_CONTAINED_BY", tokens[1].Type)
	}
}

func TestDoubleSlashIsIllegal(t *testing.T) {
	// Single / is SLASH, but // should be SLASH SLASH
	tokens := lexNonEOF("//")
	expected := []TokenType{TOKEN_SLASH, TOKEN_SLASH}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d]: got %s, want %s", i, tokens[i].Type, want)
		}
	}
}

func TestSinglePipeIsIllegal(t *testing.T) {
	tok := New("|").NextToken()
	if tok.Type != TOKEN_ILLEGAL {
		t.Errorf("single |: expected ILLEGAL, got %s", tok.Type)
	}
}

// --- Parameter token ---

func TestParamTokenization(t *testing.T) {
	input := "WHERE id = $1 AND name = $2"
	tokens := lexNonEOF(input)
	params := 0
	for _, tok := range tokens {
		if tok.Type == TOKEN_PARAM {
			params++
		}
	}
	if params != 2 {
		t.Errorf("expected 2 PARAM tokens, got %d", params)
	}
}

func TestParamInString(t *testing.T) {
	// $1 inside a string should not become PARAM
	tokens := lexNonEOF("'$1'")
	if tokens[0].Type != TOKEN_STRING_LIT {
		t.Errorf("expected STRING_LIT, got %s", tokens[0].Type)
	}
	if tokens[0].Literal != "$1" {
		t.Errorf("got %q, want %q", tokens[0].Literal, "$1")
	}
}

// --- Semicolons and separators ---

func TestMultipleSemicolons(t *testing.T) {
	tokens := lexNonEOF(";;")
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}
	for _, tok := range tokens {
		if tok.Type != TOKEN_SEMICOLON {
			t.Errorf("expected SEMICOLON, got %s", tok.Type)
		}
	}
}

func TestParentheses(t *testing.T) {
	tokens := lexNonEOF("()")
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}
	if tokens[0].Type != TOKEN_LPAREN {
		t.Errorf("expected LPAREN, got %s", tokens[0].Type)
	}
	if tokens[1].Type != TOKEN_RPAREN {
		t.Errorf("expected RPAREN, got %s", tokens[1].Type)
	}
}

func TestStarInSelect(t *testing.T) {
	tokens := lexNonEOF("SELECT * FROM t")
	if tokens[1].Type != TOKEN_STAR {
		t.Errorf("expected STAR, got %s", tokens[1].Type)
	}
}

// --- Strings with adjacent tokens ---

func TestStringFollowedBySemicolon(t *testing.T) {
	tokens := lexNonEOF("'hello';")
	expected := []TokenType{TOKEN_STRING_LIT, TOKEN_SEMICOLON}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	if tokens[0].Literal != "hello" {
		t.Errorf("string: got %q", tokens[0].Literal)
	}
}

func TestNumberFollowedByComma(t *testing.T) {
	tokens := lexNonEOF("1,2,3")
	expected := []TokenType{TOKEN_INT_LIT, TOKEN_COMMA, TOKEN_INT_LIT, TOKEN_COMMA, TOKEN_INT_LIT}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
}

// --- Negative number in WHERE ---

func TestNegativeNumberInWhere(t *testing.T) {
	tokens := lexNonEOF("WHERE level >= -3")
	expected := []TokenType{TOKEN_WHERE, TOKEN_IDENT, TOKEN_GTE, TOKEN_INT_LIT}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	if tokens[3].Literal != "-3" {
		t.Errorf("got literal %q, want %q", tokens[3].Literal, "-3")
	}
}

// --- CASE WHEN ---

func TestCaseWhenExpression(t *testing.T) {
	input := "CASE WHEN x = 1 THEN 'a' WHEN x = 2 THEN 'b' ELSE 'c' END"
	tokens := lexNonEOF(input)
	expected := []TokenType{
		TOKEN_CASE, TOKEN_WHEN, TOKEN_IDENT, TOKEN_EQ, TOKEN_INT_LIT, TOKEN_THEN, TOKEN_STRING_LIT,
		TOKEN_WHEN, TOKEN_IDENT, TOKEN_EQ, TOKEN_INT_LIT, TOKEN_THEN, TOKEN_STRING_LIT,
		TOKEN_ELSE, TOKEN_STRING_LIT, TOKEN_END,
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d] %q: got %s, want %s", i, tokens[i].Literal, tokens[i].Type, want)
		}
	}
}

// --- COALESCE ---

func TestCoalesceTokenization(t *testing.T) {
	tokens := lexNonEOF("COALESCE(x, 0)")
	expected := []TokenType{TOKEN_COALESCE, TOKEN_LPAREN, TOKEN_IDENT, TOKEN_COMMA, TOKEN_INT_LIT, TOKEN_RPAREN}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
}

// --- INSERT ... ON CONFLICT DO NOTHING ---

func TestInsertOnConflict(t *testing.T) {
	input := "INSERT INTO t (id) VALUES (1) ON CONFLICT DO NOTHING RETURNING *"
	tokens := lexNonEOF(input)
	expected := []TokenType{
		TOKEN_INSERT, TOKEN_INTO, TOKEN_IDENT, TOKEN_LPAREN, TOKEN_IDENT, TOKEN_RPAREN,
		TOKEN_VALUES, TOKEN_LPAREN, TOKEN_INT_LIT, TOKEN_RPAREN,
		TOKEN_ON, TOKEN_CONFLICT, TOKEN_DO, TOKEN_NOTHING,
		TOKEN_RETURNING, TOKEN_STAR,
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d: %v", len(expected), len(tokens), tokens)
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d] %q: got %s, want %s", i, tokens[i].Literal, tokens[i].Type, want)
		}
	}
}

// --- MERGE statement ---

func TestMergeTokenization(t *testing.T) {
	input := "MERGE INTO target USING source ON target.id = source.id WHEN MATCHED THEN UPDATE SET x = 1"
	tokens := lexNonEOF(input)
	if tokens[0].Type != TOKEN_MERGE {
		t.Errorf("expected MERGE, got %s", tokens[0].Type)
	}
	// Check for MATCHED token
	found := false
	for _, tok := range tokens {
		if tok.Type == TOKEN_MATCHED {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected MATCHED token in MERGE statement")
	}
}

// --- VACUUM ---

func TestVacuumTokenization(t *testing.T) {
	tokens := lexNonEOF("VACUUM")
	if tokens[0].Type != TOKEN_VACUUM {
		t.Errorf("expected VACUUM, got %s", tokens[0].Type)
	}
}

// --- BEGIN/COMMIT/ROLLBACK ---

func TestTransactionTokens(t *testing.T) {
	input := "BEGIN; COMMIT; ROLLBACK;"
	tokens := lexNonEOF(input)
	expected := []TokenType{TOKEN_BEGIN, TOKEN_SEMICOLON, TOKEN_COMMIT, TOKEN_SEMICOLON, TOKEN_ROLLBACK, TOKEN_SEMICOLON}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d]: got %s, want %s", i, tokens[i].Type, want)
		}
	}
}

// --- SAVEPOINT/RELEASE ---

func TestSavepointTokens(t *testing.T) {
	input := "SAVEPOINT sp1; RELEASE sp1;"
	tokens := lexNonEOF(input)
	expected := []TokenType{TOKEN_SAVEPOINT, TOKEN_IDENT, TOKEN_SEMICOLON, TOKEN_RELEASE, TOKEN_IDENT, TOKEN_SEMICOLON}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d]: got %s, want %s", i, tokens[i].Type, want)
		}
	}
}

// --- TRUNCATE ---

func TestTruncateTokenization(t *testing.T) {
	tokens := lexNonEOF("TRUNCATE TABLE users")
	expected := []TokenType{TOKEN_TRUNCATE, TOKEN_TABLE, TOKEN_IDENT}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
}

// --- PREPARE/EXECUTE/DEALLOCATE ---

func TestPrepareExecuteDeallocate(t *testing.T) {
	input := "PREPARE q AS SELECT 1; EXECUTE q; DEALLOCATE q;"
	tokens := lexNonEOF(input)
	expected := []TokenType{
		TOKEN_PREPARE, TOKEN_IDENT, TOKEN_AS, TOKEN_SELECT, TOKEN_INT_LIT, TOKEN_SEMICOLON,
		TOKEN_EXECUTE, TOKEN_IDENT, TOKEN_SEMICOLON,
		TOKEN_DEALLOCATE, TOKEN_IDENT, TOKEN_SEMICOLON,
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d]: got %s, want %s", i, tokens[i].Type, want)
		}
	}
}

// --- EXISTS ---

func TestExistsTokenization(t *testing.T) {
	tokens := lexNonEOF("EXISTS (SELECT 1)")
	expected := []TokenType{TOKEN_EXISTS, TOKEN_LPAREN, TOKEN_SELECT, TOKEN_INT_LIT, TOKEN_RPAREN}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
}

// --- APPLY/PREVIEW ---

func TestApplyPreviewTokens(t *testing.T) {
	input := "APPLY MIGRATION; PREVIEW MIGRATION;"
	tokens := lexNonEOF(input)
	expected := []TokenType{TOKEN_APPLY, TOKEN_MIGRATION, TOKEN_SEMICOLON, TOKEN_PREVIEW, TOKEN_MIGRATION, TOKEN_SEMICOLON}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d]: got %s, want %s", i, tokens[i].Type, want)
		}
	}
}

// --- MIGRATION ---

func TestMigrationTokenization(t *testing.T) {
	tokens := lexNonEOF("MIGRATION")
	if tokens[0].Type != TOKEN_MIGRATION {
		t.Errorf("expected MIGRATION, got %s", tokens[0].Type)
	}
}

// --- POLICY/ENABLE/RLS ---

func TestPolicyEnableRls(t *testing.T) {
	input := "ALTER TABLE t ENABLE RLS"
	tokens := lexNonEOF(input)
	expected := []TokenType{TOKEN_ALTER, TOKEN_TABLE, TOKEN_IDENT, TOKEN_ENABLE, TOKEN_RLS}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d]: got %s, want %s", i, tokens[i].Type, want)
		}
	}
}

// --- INDEX/INDEXES ---

func TestIndexTokens(t *testing.T) {
	input := "CREATE INDEX idx ON t(id); SHOW INDEXES FROM t;"
	tokens := lexNonEOF(input)
	expected := []TokenType{
		TOKEN_CREATE, TOKEN_INDEX, TOKEN_IDENT, TOKEN_ON, TOKEN_IDENT, TOKEN_LPAREN, TOKEN_IDENT, TOKEN_RPAREN, TOKEN_SEMICOLON,
		TOKEN_SHOW, TOKEN_INDEXES, TOKEN_FROM, TOKEN_IDENT, TOKEN_SEMICOLON,
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d]: got %s, want %s", i, tokens[i].Type, want)
		}
	}
}

// --- Data type keywords ---

func TestDataTypes(t *testing.T) {
	input := "a INT b FLOAT c BOOL d TEXT e VARCHAR f DATE g TIME h DECIMAL i ENUM j ARRAY k VECTOR l FLEXIBLE"
	tokens := lexNonEOF(input)
	expected := []TokenType{
		TOKEN_IDENT, TOKEN_INT, TOKEN_IDENT, TOKEN_FLOAT_TYPE, TOKEN_IDENT, TOKEN_BOOL,
		TOKEN_IDENT, TOKEN_TEXT, TOKEN_IDENT, TOKEN_VARCHAR, TOKEN_IDENT, TOKEN_DATE,
		TOKEN_IDENT, TOKEN_TIME, TOKEN_IDENT, TOKEN_DECIMAL, TOKEN_IDENT, TOKEN_ENUM,
		TOKEN_IDENT, TOKEN_ARRAY, TOKEN_IDENT, TOKEN_VECTOR, TOKEN_IDENT, TOKEN_FLEXIBLE,
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d]: got %s, want %s", i, tokens[i].Type, want)
		}
	}
}

// --- SCHEMA/AUTO_INCREMENT/GENERATED/ALWAYS ---

func TestSchemaAutoIncrementGenerated(t *testing.T) {
	input := "SCHEMA s AUTO_INCREMENT GENERATED ALWAYS"
	tokens := lexNonEOF(input)
	expected := []TokenType{TOKEN_SCHEMA, TOKEN_IDENT, TOKEN_AUTO_INCREMENT, TOKEN_GENERATED, TOKEN_ALWAYS}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d]: got %s, want %s", i, tokens[i].Type, want)
		}
	}
}

// --- REPLACE ---

func TestReplaceTokenization(t *testing.T) {
	tokens := lexNonEOF("REPLACE")
	if tokens[0].Type != TOKEN_IDENT {
		t.Errorf("expected IDENT (REPLACE is not a keyword), got %s", tokens[0].Type)
	}
}

// --- FULLTEXT_MATCH ---

func TestFulltextMatchTokenization(t *testing.T) {
	tokens := lexNonEOF("a @@ b")
	expected := []TokenType{TOKEN_IDENT, TOKEN_FULLTEXT_MATCH, TOKEN_IDENT}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	if tokens[1].Type != TOKEN_FULLTEXT_MATCH {
		t.Errorf("expected FULLTEXT_MATCH, got %s", tokens[1].Type)
	}
}

// --- INTERVAL ---

func TestIntervalTokenization(t *testing.T) {
	tokens := lexNonEOF("INTERVAL '1 day'")
	expected := []TokenType{TOKEN_INTERVAL, TOKEN_STRING_LIT}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
}

// --- UUID ---

func TestUUIDTokenization(t *testing.T) {
	tokens := lexNonEOF("UUID")
	if tokens[0].Type != TOKEN_UUID {
		t.Errorf("expected UUID, got %s", tokens[0].Type)
	}
}

// --- JSONB ---

func TestJSONBTokenization(t *testing.T) {
	tokens := lexNonEOF("JSONB")
	if tokens[0].Type != TOKEN_JSONB {
		t.Errorf("expected JSONB, got %s", tokens[0].Type)
	}
}

// --- TRIGGER/BEFORE/AFTER/EACH ---

func TestTriggerTokens(t *testing.T) {
	input := "CREATE TRIGGER trg BEFORE INSERT ON t FOR EACH ROW BEGIN END"
	tokens := lexNonEOF(input)
	expected := []TokenType{
		TOKEN_CREATE, TOKEN_TRIGGER, TOKEN_IDENT,
		TOKEN_BEFORE, TOKEN_INSERT, TOKEN_ON, TOKEN_IDENT,
		TOKEN_FOR, TOKEN_EACH, TOKEN_ROW,
		TOKEN_BEGIN, TOKEN_END,
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d] %q: got %s, want %s", i, tokens[i].Literal, tokens[i].Type, want)
		}
	}
}

// --- FUNCTION/RETURNS/CALL ---

func TestFunctionReturnCall(t *testing.T) {
	input := "CREATE FUNCTION f() RETURNS INT AS $$ BEGIN RETURN 1; END; $$ LANGUAGE plpgsql; CALL f();"
	tokens := lexNonEOF(input)
	// Check key tokens exist
	found := map[TokenType]bool{}
	for _, tok := range tokens {
		found[tok.Type] = true
	}
	for _, tt := range []TokenType{TOKEN_FUNCTION, TOKEN_RETURNS, TOKEN_CALL} {
		if !found[tt] {
			t.Errorf("missing token %s", tt)
		}
	}
}

// --- LATERAL ---

func TestLateralTokenization(t *testing.T) {
	tokens := lexNonEOF("LATERAL")
	if tokens[0].Type != TOKEN_LATERAL {
		t.Errorf("expected LATERAL, got %s", tokens[0].Type)
	}
}

// --- References ---

func TestReferencesTokenization(t *testing.T) {
	tokens := lexNonEOF("REFERENCES")
	if tokens[0].Type != TOKEN_REFERENCES {
		t.Errorf("expected REFERENCES, got %s", tokens[0].Type)
	}
}

// --- ANY/SOME ---

func TestAnySomeTokens(t *testing.T) {
	input := "x IN ANY(1,2,3) x IN SOME(1,2,3)"
	tokens := lexNonEOF(input)
	// Should have at least ANY and SOME tokens
	found := map[TokenType]bool{}
	for _, tok := range tokens {
		found[tok.Type] = true
	}
	if !found[TOKEN_ANY] {
		t.Error("missing ANY token")
	}
	if !found[TOKEN_SOME] {
		t.Error("missing SOME token")
	}
}

// --- PROCEDURE ---

func TestProcedureTokenization(t *testing.T) {
	tokens := lexNonEOF("PROCEDURE")
	if tokens[0].Type != TOKEN_PROCEDURE {
		t.Errorf("expected PROCEDURE, got %s", tokens[0].Type)
	}
}

// --- Incomplete operator sequences ---

func TestIncompleteOperators(t *testing.T) {
	// < followed by non-@ and non-= should be just LT
	tokens := lexNonEOF("<a")
	if tokens[0].Type != TOKEN_LT {
		t.Errorf("expected LT, got %s", tokens[0].Type)
	}
	// > followed by non-= should be just GT
	tokens = lexNonEOF(">a")
	if tokens[0].Type != TOKEN_GT {
		t.Errorf("expected GT, got %s", tokens[0].Type)
	}
}

// --- Consecutive strings ---

func TestConsecutiveStrings(t *testing.T) {
	// SQL-style '' is not an escape in this lexer; it produces two separate string tokens
	tokens := lexNonEOF("'a''b'")
	if len(tokens) != 2 {
		t.Fatalf("expected 2 STRING_LIT tokens, got %d: %v", len(tokens), tokens)
	}
	if tokens[0].Type != TOKEN_STRING_LIT || tokens[0].Literal != "a" {
		t.Errorf("token[0]: got %s %q", tokens[0].Type, tokens[0].Literal)
	}
	if tokens[1].Type != TOKEN_STRING_LIT || tokens[1].Literal != "b" {
		t.Errorf("token[1]: got %s %q", tokens[1].Type, tokens[1].Literal)
	}
}

// --- String containing only escape sequences ---

func TestStringAllEscapes(t *testing.T) {
	tokens := lexNonEOF(`'\n\t\r\\\'` + `'`)
	// This is: \n \t \r \\ \' followed by closing '
	if tokens[0].Type != TOKEN_STRING_LIT {
		t.Errorf("expected STRING_LIT, got %s", tokens[0].Type)
	}
	expected := "\n\t\r\\'"
	if tokens[0].Literal != expected {
		t.Errorf("got %q, want %q", tokens[0].Literal, expected)
	}
}

// --- Very long identifier ---

func TestLongIdentifier(t *testing.T) {
	name := strings.Repeat("a", 1000)
	tokens := lexNonEOF(name)
	if len(tokens) != 1 || tokens[0].Type != TOKEN_IDENT {
		t.Fatalf("expected 1 IDENT, got %v", tokens)
	}
	if tokens[0].Literal != name {
		t.Errorf("identifier length mismatch: got %d, want 1000", len(tokens[0].Literal))
	}
}

// --- Very long string ---

func TestLongString(t *testing.T) {
	content := strings.Repeat("x", 5000)
	input := "'" + content + "'"
	tokens := lexNonEOF(input)
	if len(tokens) != 1 || tokens[0].Type != TOKEN_STRING_LIT {
		t.Fatalf("expected 1 STRING_LIT, got %v", tokens)
	}
	if tokens[0].Literal != content {
		t.Errorf("string length mismatch: got %d, want 5000", len(tokens[0].Literal))
	}
}

// --- Mixed tokens in sequence ---

func TestMixedTokenSequence(t *testing.T) {
	input := "SELECT 1, 'hello', TRUE, NULL, -3.14, * FROM t WHERE x != $1"
	tokens := lexNonEOF(input)
	expected := []TokenType{
		TOKEN_SELECT,
		TOKEN_INT_LIT,
		TOKEN_COMMA,
		TOKEN_STRING_LIT,
		TOKEN_COMMA,
		TOKEN_TRUE,
		TOKEN_COMMA,
		TOKEN_NULL,
		TOKEN_COMMA,
		TOKEN_FLOAT_LIT,
		TOKEN_COMMA,
		TOKEN_STAR,
		TOKEN_FROM,
		TOKEN_IDENT,
		TOKEN_WHERE,
		TOKEN_IDENT,
		TOKEN_NEQ,
		TOKEN_PARAM,
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, want := range expected {
		if tokens[i].Type != want {
			t.Errorf("token[%d] %q: got %s, want %s", i, tokens[i].Literal, tokens[i].Type, want)
		}
	}
}
