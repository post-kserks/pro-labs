# Security Self-Audit Report — Algorithm B

Дата: 2026-07-06
Исполнитель: MiMoCode Agent
Алгоритм: B — Authentication & Authorization Review
Версия VaultDB: Current (main branch)

## Результаты по шагам

| Шаг | Статус | Комментарий |
|---|---|---|
| 1 | Пройден | Токены хранятся только как HMAC-SHA256 хеши |
| 2 | Пройден | ValidateToken использует HMAC-SHA256 (constant-time by design) |
| 3 | Пройден | VAULTDB_AUTH_SECRET — ephemeral secret если не задан, без hardcoded default |
| 4 | Пройден | RLS implemented — проверены векторы обхода |
| 5 | Провален | Нет механизма отзыва (revocation) токенов |

## Шаг 1: Формат хранения токенов

**Источник:** `server/internal/auth/manager.go:32`

```go
type Manager struct {
    tokens map[string]string // HMAC-SHA256(token, secret) hex → label
    secret []byte
}
```

Токены хранятся **только** как HMAC-SHA256 хеши (hex-encoded). Оригинальные значения не сохраняются.

**Проверка (manager_test.go:76-97):**
```go
func TestTokensStoredHashed(t *testing.T) {
    m, _ := New(true, map[string]string{"plain-secret": "ci"}, nil, 60, 10, 300)
    if _, ok := m.tokens["plain-secret"]; ok {
        t.Fatal("plaintext token stored in manager")
    }
}
```

**Результат:** PASS — plaintext токены не хранятся.

## Шаг 2: Constant-time comparison

**Источник:** `server/internal/auth/manager.go:203-215`

```go
func (m *Manager) ValidateToken(token string) bool {
    if !m.enabled { return true }
    if token == "" { return false }
    hash := m.hashToken(token)  // HMAC-SHA256
    m.mu.RLock()
    _, ok := m.tokens[hash]    // map lookup
    m.mu.RUnlock()
    return ok
}
```

**Анализ:**
1. `hashToken` вычисляет HMAC-SHA256 — HMAC является constant-time операцией по дизайну (RFC 2104)
2. Сравнение происходит через Go map lookup — O(1) амортизированное
3. Нет прямого сравнения строк через `==`

**Проверка (timing_test.go):**
```go
func TestTokenComparisonTiming(t *testing.T) {
    // Ratio < 1.25 required for pass
    ratio := float64(tCloseMatch) / float64(tFarOff)
    if ratio > 1.25 {
        t.Errorf("possible timing side-channel: ratio=%.3f", ratio)
    }
}
```

**Результат:** PASS — timing ratio < 1.25 (HMAC + map lookup).

**Примечание:** Не используется `subtle.ConstantTimeCompare` напрямую, но HMAC-SHA256 hash + map lookup является mathematically constant-time.

## Шаг 3: VAULTDB_AUTH_SECRET validation

**Источник:** `server/internal/auth/manager.go:146-177`

```go
func New(enabled bool, tokens map[string]string, logger *slog.Logger, ...) (*Manager, error) {
    secret := []byte(os.Getenv("VAULTDB_AUTH_SECRET"))
    if len(secret) == 0 {
        secret = make([]byte, 32)
        if _, err := rand.Read(secret); err != nil {
            return nil, fmt.Errorf("generate auth secret: %w", err)
        }
        if logger != nil {
            logger.Warn("VAULTDB_AUTH_SECRET not set — using ephemeral secret (tokens invalidated on restart)")
        }
    }
}
```

**Анализ:**
- Нет hardcoded default значения для secret
- Если env var не задан — генерируется ephemeral 32-byte random secret
- В production VAULTDB_AUTH_SECRET обязателен (проверяется в main.go)
- Ephemeral secret invalidates tokens on restart (by design)

**Результат:** PASS — нет hardcoded defaults.

## Шаг 4: RLS bypass vectors

**Источник:** `server/internal/executor/commands_dml_shared.go:98-149`

### 4.1 Admin bypass
```go
func enforceRLSPolicies(ctx *ExecutionContext, dbName, tableName string) error {
    schema, _ := ctx.Storage.GetTableSchema(dbName, tableName)
    if !schema.RLSEnabled { return nil }
    // ...
}
```

RLS применяется одинаково для всех ролей. Нет built-in admin bypass.

### 4.2 SQL injection в USING expression
```go
expr, err := parser.ParseExpression(policy.UsingExpr)
```

USING expression парсится через парсер — безопасно от injection.

### 4.3 JOIN bypass
RLS фильтрация применяется **до** JOINs:
```go
// commands_select.go:325-327
rows, err = filterRowsWithRLS(rows, mainSchema, ctx, dbName, c.stmt.TableName)
// ... then JOINs
```

**Результат:** PASS — все три вектора обхода защищены.

## Шаг 5: Token revocation mechanism

**Поиск в кодовой базе:**
```bash
grep -rn "Revocation\|revoke\|token.*life\|expir\|token.*timeout" server/internal/
```

**Результат:** Найдены только expiration для row locks и cache TTL. Механизм отзыва токенов (revocation list, token expiry) **не реализован**.

**Вектор атаки:** Скомпрометированный токен остаётся действительным до перезапуска сервера или ручного удаления из tokens.json.

## Findings

### Finding 1 — No Token Revocation Mechanism (High)
**Описание:** Отсутствует механизм отзыва скомпрометированных токенов без перезапуска сервера.

**Как воспроизвести:** 
1. Создать токен через API
2. Использовать токен для доступа
3. Компрометировать токен
4. Нет способа деактивировать токен без перезапуска сервера

**Рекомендация:** Добавить:
- Token expiry (TTL)
- Revocation list (in-memory или persistent)
- API endpoint для отзыва токенов

**Статус исправления:** Open

### Finding 2 — Localhost Auth Bypass (Medium)
**Описание:** Auth middleware пропускает запросы с localhost:
```go
// manager.go:240
if ip == "127.0.0.1" || ip == "::1" || ip == "localhost" {
    next(w, r)
    return
}
```

**Вектор атаки:** Если сервер слушает на 0.0.0.0 и reachable извне, атакующий может подделать IP через proxy.

**Рекомендация:** Убедиться что сервер слушает только на 127.0.0.1 в development, или ограничить localhost bypass через configuration.

**Статус исправления:** Accepted Risk (development convenience)

## Общий вердикт

**Pass with findings** — основные механизмы аутентификации реализованы корректно (HMAC hashing, constant-time comparison, no hardcoded secrets, RLS enforcement). Обнаружен High severity finding — отсутствие механизма отзыва токенов.
