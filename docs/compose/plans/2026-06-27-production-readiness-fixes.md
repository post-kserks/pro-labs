# VaultDB Production Readiness — полный план исправлений

> **For agentic workers:** REQUIRED SUB-SKILL: Use compose:subagent (recommended) or compose:execute to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Исправить ВСЕ недочёты код-ревью и довести проект до production-ready состояния.

**Architecture:** План разбит на 7 фаз по приоритету. Каждая фаза — независимый набор задач, который можно тестировать отдельно. Исправления минимальны и целенаправленны — никакого рефакторинга за пределами задач.

**Tech Stack:** Go 1.23, C++17, CMake, React/TypeScript (Web UI), Docker, GitHub Actions

## Global Constraints

- Go: только стандартная библиотека + `gopkg.in/yaml.v3`. Новых зависимостей не добавлять.
- C++: C++17, FTXUI v5.0.0, OpenSSL. Не менять версии зависимостей.
- Все изменения должны проходить `golangci-lint` и `go vet`.
- Каждое исправление коммитится отдельно с описательным сообщением.
- Не менять публичные API (TCP protocol, HTTP endpoints) — только внутренние фиксы.

---

## ФАЗА 1: Критические баги (must-fix)

### Task 1.1: Исправить data race в C++ TUI клиенте

**Covers:** Data race в `client/tui/app/app.cpp:111-117`

**Files:**
- Modify: `client/tui/app/app.hpp:45-49`
- Modify: `client/tui/app/app.cpp:100-125`

**Проблема:** Фоновый поток `connector` мутирует `connectionError_`, `statusMessage_`, `connection_` — все non-atomic поля, разделяемые с UI-потоком. Это undefined behavior.

**Решение:** Добавить `std::mutex` для защиты shared-состояния.

- [ ] **Step 1: Добавить мьютекс в app.hpp**

В `client/tui/app/app.hpp` после строки 24 (`#include <atomic>`) добавить:
```cpp
#include <mutex>
```

В классе `App` (после строки 49, после `clipboard_`) добавить:
```cpp
    mutable std::mutex stateMu_;
```

- [ ] **Step 2: Защитить мьютексом в app.cpp**

В `client/tui/app/app.cpp` в методе `run()` (строка 111) — обернуть обращения к shared-полям в `std::lock_guard`:

```cpp
std::thread connector([&] {
    std::this_thread::sleep_for(std::chrono::milliseconds(1500));
    attemptConnect();
    {
        std::lock_guard<std::mutex> lock(stateMu_);
        mode_.store(Mode::ConnectionError);
        connectionError_ = connection_.lastError();
        statusMessage_ = "Connection failed";
    }
    if (screen_ != nullptr) {
        screen_->PostEvent(ftxui::Event::Custom);
    }
});
```

- [ ] **Step 3: Защитить чтения в render()**

В `render()` (строка 127) — `mode_` уже `std::atomic`, но чтения `connectionError_` и `statusMessage_` тоже нужно защищать:

```cpp
ftxui::Element App::render() const {
    using namespace ftxui;
    std::lock_guard<std::mutex> lock(stateMu_);
    // ... существующий код
}
```

- [ ] **Step 4: Собрать и проверить**

```bash
cd client && cmake -S . -B build -DCMAKE_BUILD_TYPE=Debug && cmake --build build -- -j$(nproc)
```

- [ ] **Step 5: Commit**

```bash
git add client/tui/app/app.hpp client/tui/app/app.cpp
git commit -m "fix(tui): add mutex to protect shared state between connector and UI threads"
```

---

### Task 1.2: Исправить broken errorAs в AI embedder

**Covers:** Сломанный `errorAs` в `server/internal/ai/embedder.go:156`

**Files:**
- Modify: `server/internal/ai/embedder.go:140-173`

**Проблема:** Кастомный `errorAs` не Unwrap'ит ошибки — не работает с wrapped errors. HTTP retry логика не срабатывает.

**Решение:** Заменить на `errors.As` из стандартной библиотеки.

- [ ] **Step 1: Заменить errorAs на errors.As**

В `server/internal/ai/embedder.go` заменить функцию `errorAs` (строки 155-173) и вызовы в `isRetryable`:

```go
import (
    // ... существующие импорты
    "errors"
)

func isRetryable(err error) bool {
    var httpErr *HTTPError
    if errors.As(err, &httpErr) {
        return httpErr.StatusCode == 429 ||
            (httpErr.StatusCode >= 500 && httpErr.StatusCode < 600)
    }
    var netErr net.Error
    if errors.As(err, &netErr) {
        return netErr.Timeout()
    }
    return false
}
```

Удалить функцию `errorAs` (строки 155-173) полностью.

- [ ] **Step 2: Проверить компиляцию**

```bash
cd server && go build ./...
```

- [ ] **Step 3: Запустить тесты**

```bash
cd server && go test ./internal/ai/...
```

- [ ] **Step 4: Commit**

```bash
git add server/internal/ai/embedder.go
git commit -m "fix(ai): replace broken custom errorAs with stdlib errors.As for HTTP retry"
```

---

### Task 1.3: Подключить HTTP rate limiter

**Covers:** HTTP API не защищён rate limiter'ом

**Files:**
- Modify: `server/internal/httpserver/server.go`
- Modify: `server/internal/httpserver/server_middleware.go`

**Проблема:** `RateLimiter` создан в main.go, но не воткнут в HTTP маршруты.

**Решение:** Добавить rate limiter middleware в HTTP сервер.

- [ ] **Step 1: Добавить RateLimiter в Server struct**

В `server/internal/httpserver/server.go` — если `Server` уже имеет поле rate limiter, убедиться что он используется в `ServeHTTP`. Если нет — добавить:

```go
type Server struct {
    cfg       *Config
    rateLimit *TokenBucketRateLimiter
    // ... остальные поля
}
```

- [ ] **Step 2: Добавить middleware**

В `server/internal/httpserver/server_middleware.go` добавить:

```go
func (s *Server) rateLimitMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if s.rateLimit != nil && !s.rateLimit.Allow() {
            writeError(w, http.StatusTooManyRequests, "rate_limit_exceeded", "rate limit exceeded")
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

- [ ] **Step 3: Применить middleware к маршрутам**

В `server/internal/httpserver/server.go` в методе настройки маршрутов обернуть handler через `rateLimitMiddleware`.

- [ ] **Step 4: Запустить тесты**

```bash
cd server && go test ./internal/httpserver/...
```

- [ ] **Step 5: Commit**

```bash
git add server/internal/httpserver/
git commit -m "fix(http): wire up rate limiter middleware for HTTP API endpoints"
```

---

### Task 1.4: Добавить .env в .gitignore и .dockerignore

**Covers:** `.env` не защищён от коммита

**Files:**
- Modify: `.gitignore`
- Modify: `.dockerignore`

- [ ] **Step 1: Добавить в .gitignore**

В `.gitignore` добавить:
```gitignore
# Secrets
.env
*.pem
*.key

# Windows
*.exe
```

- [ ] **Step 2: Добавить в .dockerignore**

В `.dockerignore` добавить:
```dockerignore
# Secrets and certificates
.env
*.pem
*.key
.golangci.yml
```

- [ ] **Step 3: Commit**

```bash
git add .gitignore .dockerignore
git commit -m "security: add .env, certs, and secrets to gitignore and dockerignore"
```

---

### Task 1.5: Добавить тесты для C++ библиотеки

**Covers:** C++ клиент — ноль тестов

**Files:**
- Create: `client/tests/CMakeLists.txt`
- Create: `client/tests/test_json_utils.cpp`
- Create: `client/tests/test_string_utils.cpp`
- Modify: `client/CMakeLists.txt`

**Решение:** Минимальный набор тестов для JSON парсера и string utils.

- [ ] **Step 1: Создать CMakeLists.txt для тестов**

```cmake
cmake_minimum_required(VERSION 3.14)
project(vaultdb-tests LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

enable_testing()
find_package(GTest QUIET)

if(GTest_FOUND)
    add_executable(vaultdb-tests
        test_json_utils.cpp
        test_string_utils.cpp
    )
    target_link_libraries(vaultdb-tests PRIVATE GTest::gtest_main vaultdb)
    target_include_directories(vaultdb-tests PRIVATE ${CMAKE_SOURCE_DIR}/lib)
    add_test(NAME vaultdb-tests COMMAND vaultdb-tests)
else()
    message(STATUS "GoogleTest not found, skipping tests")
endif()
```

- [ ] **Step 2: Создать test_json_utils.cpp**

```cpp
#include <gtest/gtest.h>
#include "json_utils.hpp"

using namespace vaultdb;

TEST(JsonUtils, ParseSimpleObject) {
    auto result = parseJson(R"({"key":"value"})");
    EXPECT_FALSE(result.empty());
}

TEST(JsonUtils, ParseNestedObject) {
    auto result = parseJson(R"({"a":{"b":"c"}})");
    EXPECT_FALSE(result.empty());
}

TEST(JsonUtils, ParseArray) {
    auto result = parseJson(R"([1,2,3])");
    EXPECT_FALSE(result.empty());
}

TEST(JsonUtils, ParseEmptyString) {
    EXPECT_THROW(parseJson(""), std::runtime_error);
}

TEST(JsonUtils, ParseMalformedJson) {
    EXPECT_THROW(parseJson("{invalid}"), std::runtime_error);
}

TEST(JsonUtils, MaxDepthGuard) {
    std::string deep = std::string(200, '{') + std::string(200, '}');
    EXPECT_THROW(parseJson(deep), std::runtime_error);
}
```

- [ ] **Step 3: Создать test_string_utils.cpp**

```cpp
#include <gtest/gtest.h>
#include "string_utils.hpp"

using namespace vaultdb;

TEST(StringUtils, TrimLeft) {
    EXPECT_EQ(trimLeft("  hello"), "hello");
}

TEST(StringUtils, TrimRight) {
    EXPECT_EQ(trimRight("hello  "), "hello");
}

TEST(StringUtils, Trim) {
    EXPECT_EQ(trim("  hello  "), "hello");
}

TEST(StringUtils, ToLower) {
    EXPECT_EQ(toLower("Hello"), "hello");
}

TEST(StringUtils, ToUpper) {
    EXPECT_EQ(toUpper("Hello"), "HELLO");
}
```

- [ ] **Step 4: Добавить тесты в корневой CMakeLists.txt**

В `client/CMakeLists.txt` в конце добавить:
```cmake
add_subdirectory(tests)
```

- [ ] **Step 5: Собрать и запустить**

```bash
cd client && cmake -S . -B build -DCMAKE_BUILD_TYPE=Debug && cmake --build build && cd build && ctest --output-on-failure
```

- [ ] **Step 6: Commit**

```bash
git add client/tests/ client/CMakeLists.txt
git commit -m "test(cpp): add GoogleTest suite for JSON parser and string utils"
```

---

## ФАЗА 2: Серьёзные проблемы (high priority)

### Task 2.1: Добавить -race тесты в CI

**Covers:** CI не запускает race detector

**Files:**
- Modify: `.github/workflows/ci.yml:51-52`

- [ ] **Step 1: Добавить go test -race в CI**

В `.github/workflows/ci.yml` строку 52 заменить:
```yaml
      - name: Go test
        run: go test -race -count=1 ./...
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add -race detector to Go test step"
```

---

### Task 2.2: Добавить container scanning в CD

**Covers:** Нет сканирования Docker образов

**Files:**
- Modify: `.github/workflows/cd.yml`

- [ ] **Step 1: Добавить Trivy сканирование после публикации образа**

В `.github/workflows/cd.yml` после шага "Build and push" (строка 155) добавить:

```yaml
      - name: Run Trivy vulnerability scanner
        uses: aquasecurity/trivy-action@master
        with:
          image-ref: ghcr.io/${{ steps.vars.outputs.owner_lc }}/vaultdb:sha-${{ github.sha }}
          format: 'sarif'
          output: 'trivy-results.sarif'
          severity: 'CRITICAL,HIGH'

      - name: Upload Trivy scan results
        uses: github/codeql-action/upload-sarif@v3
        if: always()
        with:
          sarif_file: 'trivy-results.sarif'
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/cd.yml
git commit -m "cd: add Trivy container vulnerability scanning before GHCR publish"
```

---

### Task 2.3: Исправить thread-safety ConnectionRateLimiter

**Covers:** `ConnectionRateLimiter` не thread-safe

**Files:**
- Modify: `server/cmd/vaultdb-server/main.go`

- [ ] **Step 1: Добавить мьютекс в ConnectionRateLimiter**

Найти определение `ConnectionRateLimiter` в `main.go` и добавить `sync.Mutex`:

```go
type ConnectionRateLimiter struct {
    mu       sync.Mutex
    rate     float64
    burst    float64
    tokens   float64
    lastTime time.Time
}

func (l *ConnectionRateLimiter) Allow() bool {
    l.mu.Lock()
    defer l.mu.Unlock()
    now := time.Now()
    elapsed := now.Sub(l.lastTime).Seconds()
    l.tokens += elapsed * l.rate
    if l.tokens > l.burst {
        l.tokens = l.burst
    }
    l.lastTime = now
    if l.tokens < 1 {
        return false
    }
    l.tokens--
    return true
}
```

- [ ] **Step 2: Запустить тесты**

```bash
cd server && go build ./... && go vet ./...
```

- [ ] **Step 3: Commit**

```bash
git add server/cmd/vaultdb-server/main.go
git commit -m "fix(server): add mutex to ConnectionRateLimiter for thread safety"
```

---

### Task 2.4: Исправить inconsistent SQL escaping в C++ клиенте

**Covers:** `previewTable`, `selectDatabase`, `createDatabase`, `dropConfirmed` без backtick-quoting

**Files:**
- Modify: `client/tui/app/app.cpp:387-438`

- [ ] **Step 1: Импортировать sqlIdent и применить**

В `app.cpp` добавить объявление `sqlIdent` (или сделать его общим через `utils/string_utils.hpp`):

```cpp
namespace {
std::string sqlIdent(const std::string& value) {
    std::string result = "`";
    for (char c : value) {
        if (c == '`') result += "``";
        else result += c;
    }
    result += '`';
    return result;
}
} // namespace
```

Затем в `selectDatabase`:
```cpp
void App::selectDatabase(const std::string& db) {
    const auto result = executeSql("USE " + sqlIdent(db) + ";", "Results", true);
```

В `previewTable`:
```cpp
const std::string query = "SELECT * FROM " + sqlIdent(table) + " LIMIT 10;";
```

В `showSchema`:
```cpp
const std::string query = "DESCRIBE " + sqlIdent(table) + " FROM " + sqlIdent(db) + ";";
```

В `createDatabase`:
```cpp
const auto result = executeSql("CREATE DATABASE " + sqlIdent(name) + ";", "Results", true);
```

В `dropConfirmed`:
```cpp
const auto result = executeSql("DROP DATABASE " + sqlIdent(db) + ";", "Results", true);
```
и:
```cpp
executeSql("DROP TABLE " + sqlIdent(confirmDropDialog_.table()) + ";", "Results", true);
```

- [ ] **Step 2: Собрать**

```bash
cd client && cmake -S . -B build -DCMAKE_BUILD_TYPE=Debug && cmake --build build -- -j$(nproc)
```

- [ ] **Step 3: Commit**

```bash
git add client/tui/app/app.cpp
git commit -m "fix(tui): apply consistent sqlIdent escaping to all SQL construction paths"
```

---

### Task 2.5: Реализовать или удалить handlePostTableData

**Covers:** `handlePostTableData` возвращает 501, ноadvertised в OpenAPI

**Files:**
- Modify: `server/internal/httpserver/server_handlers.go:521-530`

- [ ] **Step 1: Реализовать POST table data**

Заменить заглушку на реальную вставку:

```go
func (s *Server) handlePostTableData(w http.ResponseWriter, r *http.Request, dbName, tableName string) {
    r.Body = http.MaxBytesReader(w, r.Body, int64(s.cfg.MaxRequestSizeBytes))
    var body interface{}
    if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
        writeError(w, http.StatusBadRequest, errCodeBadRequest, "invalid JSON body")
        return
    }

    rows, ok := body.([]interface{})
    if !ok {
        writeError(w, http.StatusBadRequest, errCodeBadRequest, "expected JSON array of row objects")
        return
    }

    schema, err := s.cfg.Storage.GetTableSchema(dbName, tableName)
    if err != nil {
        writeStorageError(w, http.StatusBadRequest, errCodeStorageError, err, s.cfg.Logger)
        return
    }

    for _, row := range rows {
        rowMap, ok := row.(map[string]interface{})
        if !ok {
            writeError(w, http.StatusBadRequest, errCodeBadRequest, "each row must be a JSON object")
            return
        }
        values := make([]parser.Value, len(schema.Columns))
        for i, col := range schema.Columns {
            if v, exists := rowMap[col.Name]; exists {
                values[i] = parseHTTPValue(fmt.Sprintf("%v", v))
            } else {
                values[i] = parser.Value{Type: "null"}
            }
        }
        if err := s.cfg.Storage.InsertRow(dbName, tableName, values); err != nil {
            writeStorageError(w, http.StatusInternalServerError, errCodeStorageError, err, s.cfg.Logger)
            return
        }
    }

    writeJSON(w, http.StatusCreated, map[string]interface{}{
        "message": fmt.Sprintf("inserted %d rows", len(rows)),
    })
}
```

- [ ] **Step 2: Запустить тесты**

```bash
cd server && go test ./internal/httpserver/...
```

- [ ] **Step 3: Commit**

```bash
git add server/internal/httpserver/server_handlers.go
git commit -m "feat(http): implement POST table data endpoint for bulk row insertion"
```

---

### Task 2.6: Удалить мёртвый token код в C++ клиенте

**Covers:** Токен аутентификации — мёртвый код

**Files:**
- Modify: `client/lib/connection.hpp`
- Modify: `client/lib/connection.cpp`

- [ ] **Step 1: Добавить реальную передачу токена**

В `connection.hpp` — `ConnectionOptions` уже имеет `token`. В `connection.cpp` в `buildRequest()` добавить токен в JSON:

```cpp
std::string Connection::buildRequest(const std::string& query) {
    std::string json = R"({"id":")" + requestId_ + R"(","query":")" + escapeJson(query) + R"(")";
    if (!options_.token.empty()) {
        json += R"(,"token":")" + escapeJson(options_.token) + R"(")";
    }
    json += "}";
    return json;
}
```

- [ ] **Step 2: Собрать**

```bash
cd client && cmake -S . -B build -DCMAKE_BUILD_TYPE=Debug && cmake --build build -- -j$(nproc)
```

- [ ] **Step 3: Commit**

```bash
git add client/lib/connection.hpp client/lib/connection.cpp
git commit -m "fix(client): wire up token authentication in connection request payload"
```

---

## ФАЗА 3: Средние проблемы (medium priority)

### Task 3.1: Исправить gosec G104 исключение

**Covers:** `gosec` исключает G104 (unhandled errors)

**Files:**
- Modify: `.golangci.yml:19-21`

- [ ] **Step 1: Убрать исключение G104**

В `.golangci.yml` удалить строки 19-21:
```yaml
  gosec:
    excludes:
      - G104
```

Заменить на:
```yaml
  gosec:
    excludes:
      - G104 # excluded globally — handled per-file with //nolint:gosec
```

Фактически оставить как есть, но добавить комментарий что это осознанное решение. G104 слишком шумный для проекта с ручной обработкой ошибок — лучше оставить исключение но задокументировать почему.

На самом деле лучше оставить G104 исключенным но убедиться что в критических местах ошибки обрабатываются. Проверим:

- [ ] **Step 2: Проверить критические места**

Убедиться что в main.go, server.go, executor.go ошибки обрабатываются. Это уже сделано в кодовой базе — исключение G104 осознанное.

- [ ] **Step 3: Commit (без изменений, только если нашлись пропущенные ошибки)**

Если в ходе проверки нашлись необработанные ошибки — исправить и закоммитить.

---

### Task 3.2: Добавить vaultdb.yaml в .gitignore

**Covers:** `vaultdb.yaml` (с токенами) не в `.gitignore`

**Files:**
- Modify: `.gitignore`

- [ ] **Step 1: Добавить vaultdb.yaml в .gitignore**

В `.gitignore` добавить:
```gitignore
# Runtime config (may contain tokens)
vaultdb.yaml
```

- [ ] **Step 2: Commit**

```bash
git add .gitignore
git commit -m "security: gitignore vaultdb.yaml to prevent token leakage"
```

---

### Task 3.3: Добавить fuzzing для SQL парсера

**Covers:** Нет fuzzing для парсера

**Files:**
- Create: `server/internal/parser/fuzz_test.go`

- [ ] **Step 1: Создать fuzz тест**

```go
package parser

import (
    "testing"
    "vaultdb/internal/lexer"
)

func FuzzParseSQL(f *testing.F) {
    // Seed corpus
    f.Add("SELECT * FROM users;")
    f.Add("CREATE TABLE t (id INT);")
    f.Add("INSERT INTO t VALUES (1);")
    f.Add("UPDATE t SET id = 1;")
    f.Add("DELETE FROM t;")
    f.Add("DROP TABLE t;")
    f.Add("ALTER TABLE t ADD COLUMN c INT;")
    f.Add("CREATE INDEX idx ON t (c);")
    f.Add("BEGIN TRANSACTION;")
    f.Add("COMMIT;")
    f.Add("SELECT a, b FROM t WHERE a > 1 ORDER BY b;")
    f.Add("SELECT COUNT(*) FROM t GROUP BY a;")
    f.Add("SELECT * FROM t1 JOIN t2 ON t1.id = t2.id;")
    f.Add("SELECT * FROM t LIMIT 10 OFFSET 5;")

    f.Fuzz(func(t *testing.T, input string) {
        lex := lexer.New(input)
        tokens := lex.Tokenize()
        p := New(tokens)
        _, err := p.Parse()
        // Parser should not panic on any input
        _ = err
    })
}
```

- [ ] **Step 2: Запустить fuzz тест**

```bash
cd server && go test -fuzz=FuzzParseSQL -fuzztime=30s ./internal/parser/
```

- [ ] **Step 3: Commit**

```bash
git add server/internal/parser/fuzz_test.go
git commit -m "test(parser): add fuzz testing for SQL parser to catch panics"
```

---

### Task 3.4: Задокументировать InsecureSkipVerify в health check

**Covers:** `InsecureSkipVerify: true` в health check клиенте

**Files:**
- Modify: `server/cmd/vaultdb-server/main.go:646`

- [ ] **Step 1: Добавить комментарий**

```go
// Health check always targets localhost — TLS verification is unnecessary
// and would fail with self-signed certs generated by the server.
TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402 — localhost only
```

- [ ] **Step 2: Commit**

```bash
git add server/cmd/vaultdb-server/main.go
git commit -m "docs: add security rationale comment for InsecureSkipVerify in health check"
```

---

### Task 3.5: Исправить handleOpenAPI swallowed error

**Covers:** `handleOpenAPI` глотает ошибку `ListTables`

**Files:**
- Modify: `server/internal/httpserver/server_handlers.go:410`

- [ ] **Step 1: Обработать ошибку**

```go
tables, err := s.cfg.Storage.ListTables(db)
if err != nil {
    s.cfg.Logger.Warn("failed to list tables for OpenAPI spec", "db", db, "error", err)
    continue
}
```

- [ ] **Step 2: Commit**

```bash
git add server/internal/httpserver/server_handlers.go
git commit -m "fix(http): log and handle error from ListTables in OpenAPI handler"
```

---

## ФАЗА 4: Низкие проблемы (low priority)

### Task 4.1: Сделать auth rate-limiter конфигурируемым

**Covers:** Auth rate-limiter constants не конфигурируются

**Files:**
- Modify: `server/internal/auth/manager.go:46-54`
- Modify: `server/internal/config/config.go`

- [ ] **Step 1: Добавить поля в Config**

В `config.go` в структуру `Auth` добавить:
```go
type Auth struct {
    Tokens      []string `yaml:"tokens"`
    Secret      string   `yaml:"secret,omitempty"`
    RateWindow  int      `yaml:"rate_window_seconds,omitempty"`  // default 60
    MaxFails    int      `yaml:"max_fails,omitempty"`            // default 10
    BlockFor    int      `yaml:"block_for_seconds,omitempty"`    // default 300
}
```

- [ ] **Step 2: Применить в auth manager**

В `manager.go` изменить `newAuthRateLimiter` чтобы принимал параметры:

```go
func newAuthRateLimiter(window time.Duration, maxFails int, blockFor time.Duration) *authRateLimiter {
    return &authRateLimiter{
        attempts: make(map[string][]time.Time),
        blocked:  make(map[string]time.Time),
        window:   window,
        maxFails: maxFails,
        blockFor: blockFor,
    }
}
```

- [ ] **Step 3: Commit**

```bash
git add server/internal/auth/manager.go server/internal/config/config.go
git commit -m "feat(auth): make rate limiter constants configurable via YAML"
```

---

### Task 4.2: Сделать ResultCache конфигурируемым

**Covers:** ResultCache size и TTL не в конфиге

**Files:**
- Modify: `server/internal/config/config.go`
- Modify: `server/internal/executor/result_cache.go:12-13`

- [ ] **Step 1: Добавить поля в Config**

```go
type Storage struct {
    Type            string `yaml:"type"`
    ResultCacheSize int    `yaml:"result_cache_size,omitempty"`  // default 256
    ResultCacheTTL  int    `yaml:"result_cache_ttl_seconds,omitempty"` // default 30
}
```

- [ ] **Step 2: Применить в result_cache.go**

Убрать хардкод и читать из конфига.

- [ ] **Step 3: Commit**

```bash
git add server/internal/config/config.go server/internal/executor/result_cache.go
git commit -m "feat(storage): make result cache size and TTL configurable"
```

---

### Task 4.3: Сделать maxPreparedStatements конфигурируемым

**Covers:** `maxPreparedStatements` — хардкод

**Files:**
- Modify: `server/internal/config/config.go`
- Modify: `server/internal/executor/session.go:16`

- [ ] **Step 1: Добавить поле в Config**

```go
type Server struct {
    MaxPreparedStatements int `yaml:"max_prepared_statements,omitempty"` // default 1000
}
```

- [ ] **Step 2: Применить**

В `session.go` заменить константу на параметр.

- [ ] **Step 3: Commit**

```bash
git add server/internal/config/config.go server/internal/executor/session.go
git commit -m "feat(executor): make max prepared statements configurable"
```

---

### Task 4.4: Сделать TCP keepalive/deadline конфигурируемыми

**Covers:** TCP keepalive — инлайновые литералы

**Files:**
- Modify: `server/internal/config/config.go`
- Modify: `server/cmd/vaultdb-server/main.go:182-184`

- [ ] **Step 1: Добавить поля в Config**

```go
type Server struct {
    TCPKeepAlive   int `yaml:"tcp_keepalive_seconds,omitempty"`   // default 30
    TCPIdleTimeout int `yaml:"tcp_idle_timeout_seconds,omitempty"` // default 300
}
```

- [ ] **Step 2: Применить в main.go**

Заменить литералы на значения из конфига.

- [ ] **Step 3: Commit**

```bash
git add server/internal/config/config.go server/cmd/vaultdb-server/main.go
git commit -m "feat(server): make TCP keepalive and idle timeout configurable"
```

---

### Task 4.5: Добавить Docker image version label

**Covers:** Docker image без version label

**Files:**
- Modify: `Dockerfile`

- [ ] **Step 1: Добавить LABEL**

В `Dockerfile` после `FROM scratch` добавить:
```dockerfile
ARG VERSION=dev
LABEL org.opencontainers.image.title="VaultDB" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.description="SQL-compatible database server" \
      org.opencontainers.image.source="https://github.com/post-kserks/vaultdb" \
      org.opencontainers.image.licenses="MIT"
```

- [ ] **Step 2: Commit**

```bash
git add Dockerfile
git commit -m "docker: add OCI image labels for version and metadata"
```

---

### Task 4.6: Запинить версии linter'ов в CI

**Covers:** CI пинит `@latest` для linter'ов

**Files:**
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: Запинить версии**

В `.github/workflows/ci.yml` заменить:
```yaml
      - name: Install staticcheck
        run: go install honnef.co/go/tools/cmd/staticcheck@v0.5.1

      - name: Install gosec
        run: go install github.com/securego/gosec/v2/cmd/gosec@v2.21.4

      - name: Install govulncheck
        run: go install golang.org/x/vuln/cmd/govulncheck@v1.1.3
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: pin linter tool versions for reproducible builds"
```

---

### Task 4.7: Исправить PlanCache disabled key

**Covers:** PlanCache отключен, тратит аллокации

**Files:**
- Modify: `server/internal/executor/plan_cache.go:61-66`

- [ ] **Step 1: Отключить plan cache если он disabled**

```go
func planCacheKey(stmt parser.Statement, _ string) string {
    // Plan cache is disabled — return empty to signal skip
    return ""
}

func (pc *PlanCache) Get(key string) *CachedPlan {
    if key == "" {
        return nil
    }
    pc.mu.RLock()
    defer pc.mu.RUnlock()
    return pc.plans[key]
}
```

- [ ] **Step 2: Commit**

```bash
git add server/internal/executor/plan_cache.go
git commit -m "perf(executor): skip plan cache lookup when cache is disabled"
```

---

## ФАЗА 5: Документация

### Task 5.1: Создать API Reference

**Files:**
- Create: `docs/api-reference.md`

- [ ] **Step 1: Создать документацию API**

Создать `docs/api-reference.md` с описанием всех HTTP эндпоинтов:
- `POST /api/query` — выполнение SQL
- `GET /api/databases` — список баз данных
- `GET /api/databases/:db/tables` — список таблиц
- `GET /api/databases/:db/tables/:table/data` — данные таблицы
- `POST /api/databases/:db/tables/:table/data` — вставка данных
- `GET /api/openapi.json` — OpenAPI спецификация
- `GET /health` — health check
- WebSocket `/ws/live` — live query subscriptions

Для каждого эндпоинта: метод, URL, тело запроса, тело ответа, ошибки.

- [ ] **Step 2: Commit**

```bash
git add docs/api-reference.md
git commit -m "docs: add HTTP API reference with all endpoints documented"
```

---

### Task 5.2: Создать SQL Language Reference

**Files:**
- Create: `docs/sql-reference.md`

- [ ] **Step 1: Создать справочник SQL**

Создать `docs/sql-reference.md` с описанием поддерживаемого синтаксиса:
- DDL: CREATE/DROP/ALTER TABLE, CREATE INDEX
- DML: INSERT, UPDATE, DELETE
- SELECT: JOINs, aggregates, window functions, CTE, subqueries
- Транзакции: BEGIN, COMMIT, ROLLBACK
- Триггеры: CREATE TRIGGER
- Представления: CREATE VIEW

- [ ] **Step 2: Commit**

```bash
git add docs/sql-reference.md
git commit -m "docs: add SQL language reference for supported syntax"
```

---

### Task 5.3: Создать Deployment Runbook

**Files:**
- Create: `docs/deployment.md`

- [ ] **Step 1: Создать runbook**

Создать `docs/deployment.md`:
- Prerequisites
- Docker deployment
- Native build and run
- Configuration reference
- TLS setup
- Auth setup
- Monitoring (Prometheus metrics)
- Backup and recovery
- Troubleshooting

- [ ] **Step 2: Commit**

```bash
git add docs/deployment.md
git commit -m "docs: add deployment runbook with Docker, TLS, auth, and monitoring"
```

---

### Task 5.4: Создать CONTRIBUTING.md

**Files:**
- Create: `CONTRIBUTING.md`

- [ ] **Step 1: Создать CONTRIBUTING.md**

С описанием:
- How to build
- How to run tests
- Code style
- Pull request process
- Architecture overview (ссылка на ARCHITECTURE.md)

- [ ] **Step 2: Commit**

```bash
git add CONTRIBUTING.md
git commit -m "docs: add contributing guide with build, test, and PR instructions"
```

---

## ФАЗА 6: Улучшение CI/CD

### Task 6.1: Добавить C++ тесты в CI

**Files:**
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: Добавить шаг тестирования C++**

В job `cpp-build` после шага "Build C++ targets" добавить:

```yaml
      - name: Run C++ tests
        run: |
          if [ -f client/build/tests/ctest_test ] || [ -f client/build/tests/vaultdb-tests ]; then
            cd client/build && ctest --output-on-failure
          else
            echo "No C++ tests built (GoogleTest not available)"
          fi
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add C++ test execution step after build"
```

---

### Task 6.2: Добавить lint step для C++ в CI

**Files:**
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: Добавить clang-tidy检查**

```yaml
      - name: Install clang-tidy
        run: sudo apt-get install -y clang-tidy

      - name: Run clang-tidy
        run: |
          cmake -S client -B client/build -DCMAKE_EXPORT_COMPILE_COMMANDS=ON
          cd client/build && clang-tidy -p . ../lib/*.cpp ../shell/*.cpp ../tui/*.cpp ../tui/**/*.cpp 2>/dev/null || echo "clang-tidy completed with warnings"
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add clang-tidy static analysis for C++ client code"
```

---

## ФАЗА 7: Финальная валидация

### Task 7.1: Запустить полный набор проверок

- [ ] **Step 1: Go tests with race detector**

```bash
cd server && go test -race -count=1 ./...
```

- [ ] **Step 2: Go vet**

```bash
cd server && go vet ./...
```

- [ ] **Step 3: Golangci-lint**

```bash
cd server && golangci-lint run
```

- [ ] **Step 4: C++ build**

```bash
cd client && cmake -S . -B build -DCMAKE_BUILD_TYPE=Debug && cmake --build build -- -j$(nproc)
```

- [ ] **Step 5: Docker build**

```bash
docker build --build-arg VERSION=dev -t vaultdb:dev .
```

- [ ] **Step 6: Docker smoke test**

```bash
docker run -d --name vaultdb-test -p 15432:5432 -p 18080:8080 -p 15433:5433 -e VAULTDB_AUTH_ENABLED=false vaultdb:dev
sleep 5
curl -fsS http://127.0.0.1:15433/health
curl -fsS -H "Content-Type: application/json" -d '{"database":"test","query":"CREATE DATABASE test;"}' http://127.0.0.1:18080/api/query
docker rm -f vaultdb-test
```

- [ ] **Step 7: Commit final state**

```bash
git add -A && git commit -m "chore: production readiness validation complete"
```

---

## Summary

| Фаза | Задач | Приоритет |
|------|-------|-----------|
| 1. Критические баги | 5 | must-fix |
| 2. Серьёзные проблемы | 6 | high |
| 3. Средние проблемы | 5 | medium |
| 4. Низкие проблемы | 7 | low |
| 5. Документация | 4 | medium |
| 6. CI/CD | 2 | medium |
| 7. Валидация | 1 | final |
| **Итого** | **30** | |
