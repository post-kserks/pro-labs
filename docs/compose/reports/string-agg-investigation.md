# STRING_AGG Parser Investigation

> Дата: 2026-06-28
> Статус: Known limitation — требует решения

## Проблема

`SELECT STRING_AGG(name, ',') FROM heroes;` возвращает "invalid query syntax".

## Что проверено

### Лексер — работает корректно

```
[0] type=0   literal="SELECT"
[1] type=97  literal="STRING_AGG"   ← TOKEN_STRING_AGG
[2] type=159 literal="("            ← TOKEN_LPAREN
[3] type=144 literal="name"         ← TOKEN_IDENT
[4] type=154 literal=","            ← TOKEN_COMMA
[5] type=147 literal=","            ← TOKEN_STRING_LIT (строка ',')
[6] type=160 literal=")"            ← TOKEN_RPAREN
[7] type=4   literal="FROM"
[8] type=144 literal="heroes"
[9] type=158 literal=";"
[10] type=171 literal=""            ← TOKEN_EOF
```

### Парсер — case существует

`parse_utils.go:361-371`:
```go
case lexer.TOKEN_STRING_AGG:
    p.advance()
    if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
        return nil, err
    }
    args, err := p.parseValueListUntilRParen()
    if err != nil {
        return nil, err
    }
    return &AggregateExpr{Name: "STRING_AGG", Args: args}, nil
```

### Агрегатор — поддерживает delimiter

`aggregates.go:322-328`:
```go
case "STRING_AGG":
    delim := ","
    if len(args) > 1 {
        if s, ok := args[1].(string); ok {
            delim = s
        }
    }
    return &stringAgg{delimiter: delim, distinct: distinct}
```

### Тест — skip

`integration_test.go:344`:
```go
t.Skip("STRING_AGG parser limitation — multi-arg aggregates not yet supported")
```

## Что НЕ проверено (нужно исследовать)

1. **Вызывается ли case STRING_AGG в parsePrimary?**
   - Debug показал что parsePrimary вызывается с type=97 (STRING_AGG)
   - Но после этого парсинг падает
   - Возможная причина: ошибка в `parseValueListUntilRParen` или в возврате из case

2. **Как взаимодействует parseMultiplication с AggregateExpr?**
   - `parseMultiplication` вызывает `parsePrimary`, затем проверяет `*` или `/`
   - Если `parsePrimary` возвращает `AggregateExpr`, `parseMultiplication` должен вернуть его как есть
   - Но может быть issue с тем как `AggregateExpr` проходит через всю цепочку

3. **Есть ли конфликт с другим token type?**
   - STRING_AGG = type=97
   - Нет других case в parsePrimary с type=97
   - Но может быть issue в `parseSelect` при обработке колонок

## Гипотезы

### Гипотеза 1: parseValueListUntilRParen не вызывается
- Строка 367: `args, err := p.parseValueListUntilRParen()`
- Если `consume(TOKEN_LPAREN)` падает, args не будет создан
- Но debug показал что parsePrimary вызывается для аргументов, значит consume работает

### Гипотеза 2: Ошибка после возврата AggregateExpr
- case возвращает AggregateExpr
- parseMultiplication проверяет `*` или `/` — нет → возвращает AggregateExpr
- parseAddition проверяет `+` или `-` — нет → возвращает AggregateExpr
- parseComparison проверяет операторы сравнения — нет → возвращает AggregateExpr
- Всё должно работать

### Гипотеза 3: select_columns loop неправильно обрабатывает результат
- parseSelect вызывает parseExpression для каждой колонки
- После возврата AggregateExpr, проверяется alias (FROM ≠ IDENT → нет alias)
- Проверяется COMMA — нет → break
- Должно работать

### Гипотеза 4: Скрытая ошибка в глубине рекурсии
- Go стек рекурсии может переполниться для глубоких表达式
- Но STRING_AGG не является глубоким выражением
- Маловероятно

### Гипотеза 5: Конфликт с другим case в parseStatement
- parseStatement dispatches по token type
- SELECT → parseSelect
- Но что если SELECT не является текущим токеном?

## Следующие шаги

1. Добавить `fmt.Fprintf(os.Stderr, ...)` в case STRING_AGG для подтверждения что case вызывается
2. Добавить `fmt.Fprintf(os.Stderr, ...)` в `parseValueListUntilRParen` для отслеживания
3. Добавить `fmt.Fprintf(os.Stderr, ...)` в `parseSelect` после возврата из parseExpression
4. Если case НЕ вызывается — проверить dispatch в parseStatement
5. Если case вызывается но парсинг падает —追跡овать каждый шаг

## Временное решение

Для生产环境, STRING_AGG можно реализовать через:
1. Добавить STRING_AGG как обычную функцию в executor
2. Обрабатывать двух-аргументный вызов специальным образом
3. Не требовать парсера для multi-arg aggregates
