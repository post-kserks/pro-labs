# VaultDB — ТЗ на исправление замечаний код-ревью

---

| Атрибут | Значение |
|---|---|
| Документ | Code Review Fixes TZ v1.0.0 |
| Источник | Code Review от внешнего ревьюера |
| Приоритет | Критичный — перед публичным релизом |

---

## Стратегия

Замечания разбиты на три волны по принципу "что даёт максимальный эффект быстрее всего":

**Волна 1 (1–2 дня):** быстрые фиксы, не требующие архитектурных изменений.
Каждый пункт — PR отдельно, review — 15 минут.

**Волна 2 (3–5 дней):** серьёзные проблемы безопасности и производительности.
Требуют обдуманных решений, но не переписывания архитектуры.

**Волна 3 (1–2 недели):** системные доработки.
Web UI, подключение готового page-движка, транзакции.

---

## Содержание

1. [Волна 1 — Быстрые фиксы](#волна-1)
2. [Волна 2 — Безопасность и производительность](#волна-2)
3. [Волна 3 — Системные доработки](#волна-3)
4. [Распределение по разработчикам](#распределение)
5. [Чеклист приёмки](#чеклист)

---

## Волна 1 — Быстрые фиксы

### Fix 1.1 — Удалить мусор из репозитория

**Файлы:** `data_test/`, `server/tmp_data/`, `server/internal/parser/tmp_parse.txt`, `task.md`
**Ответственный:** Dev4  
**Время:** 30 минут

```bash
# Удалить из репозитория и добавить в .gitignore
git rm -r --cached data_test/ server/tmp_data/
git rm --cached server/internal/parser/tmp_parse.txt task.md

# Обновить .gitignore
cat >> .gitignore << 'EOF'

# Данные и временные файлы
data_test/
tmp_data/
**/tmp_data/
**/tmp_*.txt
**/tmp_*.json
task.md
*.md.bak
EOF

git commit -m "chore: remove test data and temp files from repository"
```

**Пояснение:** закоммиченные данные и временные файлы засоряют историю и могут содержать чувствительную информацию (пути, токены в тестовых данных).

---

### Fix 1.2 — Единая версия через VERSION файл

**Файлы:** `build.sh`, `Makefile`, `Dockerfile`, `cmd/vaultdb-server/main.go`  
**Ответственный:** Dev4  
**Время:** 1 час

```bash
# Создать единый источник истины
echo "1.2.0" > VERSION
```

```makefile
# Makefile
VERSION := $(shell cat VERSION)
LDFLAGS := -X main.version=$(VERSION) -X main.buildDate=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)

build:
	go build -ldflags="$(LDFLAGS)" -o build/vaultdb-server ./cmd/vaultdb-server

docker-build:
	docker build --build-arg VERSION=$(VERSION) -t vaultdb/vaultdb:$(VERSION) .
```

```dockerfile
# Dockerfile — принимать версию как build arg
ARG VERSION=dev
RUN go build -ldflags="-X main.version=${VERSION}" ...
```

```bash
# build.sh — читать из файла
VERSION=$(cat VERSION)
docker build -t "vaultdb/vaultdb:${VERSION}" .
```

```go
// cmd/vaultdb-server/main.go
var version = "dev" // перезаписывается через ldflags при сборке
```

---

### Fix 1.3 — sendError: не проглатывать ошибки записи в сокет

**Файл:** `server/cmd/vaultdb-server/main.go`  
**Ответственный:** Dev3  
**Время:** 30 минут

```go
// ДО (плохо):
func sendError(conn net.Conn, id, message string) {
    resp := buildErrorResponse(id, message)
    _ = writeResponse(conn, resp)  // ошибка проглочена
}

// ПОСЛЕ (правильно):
func sendError(conn net.Conn, id, message string, logger *slog.Logger) {
    resp := buildErrorResponse(id, message)
    if err := writeResponse(conn, resp); err != nil {
        // Клиент отвалился — логируем и выходим из handleConnection
        logger.Debug("failed to send error response, client disconnected",
            "conn", conn.RemoteAddr(),
            "error", err)
    }
}

// Аналогично исправить sendResult и все другие места с _ = writeResponse
// Найти все места:
// grep -rn "_ = write" server/
```

**Правило:** в Go `_ = err` допустимо только если ошибка **архитектурно** невозможна или действительно не имеет значения. Запись в закрытый сокет — не тот случай.

---

### Fix 1.4 — Увеличить лимит scanner + конфигурируемость

**Файл:** `server/cmd/vaultdb-server/main.go`  
**Ответственный:** Dev3  
**Время:** 30 минут

```go
// ДО:
scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 1 МБ — мало

// ПОСЛЕ:
const defaultMaxRequestSize = 64 * 1024 * 1024 // 64 МБ

maxSize := cfg.Server.MaxRequestSizeBytes
if maxSize <= 0 {
    maxSize = defaultMaxRequestSize
}
scanner.Buffer(make([]byte, 0, 64*1024), maxSize)
```

```yaml
# vaultdb.yaml — добавить параметр
server:
  max_request_size_bytes: 67108864  # 64 МБ
```

---

### Fix 1.5 — IndexManager: O(n) → O(1) поиск по столбцу

**Файл:** `server/internal/index/manager.go`  
**Ответственный:** Dev2  
**Время:** 1 час

```go
// ДО:
type IndexManager struct {
    mu      sync.RWMutex
    indexes map[string]*HashIndex // имя индекса → индекс
}

func (m *IndexManager) FindForColumn(column string) (*HashIndex, bool) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    for _, idx := range m.indexes {  // O(n)
        if idx.column == column {
            return idx, true
        }
    }
    return nil, false
}

// ПОСЛЕ:
type IndexManager struct {
    mu         sync.RWMutex
    indexes    map[string]*HashIndex  // имя индекса → индекс
    byColumn   map[string][]*HashIndex // столбец → список индексов (O(1) lookup)
}

func (m *IndexManager) Add(idx *HashIndex) {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.indexes[idx.name] = idx
    m.byColumn[idx.column] = append(m.byColumn[idx.column], idx)
}

func (m *IndexManager) Remove(name string) {
    m.mu.Lock()
    defer m.mu.Unlock()
    idx, ok := m.indexes[name]
    if !ok { return }
    delete(m.indexes, name)
    // Удалить из byColumn
    col := m.byColumn[idx.column]
    for i, v := range col {
        if v.name == name {
            m.byColumn[idx.column] = append(col[:i], col[i+1:]...)
            break
        }
    }
}

func (m *IndexManager) FindForColumn(column string) (*HashIndex, bool) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    if idxs, ok := m.byColumn[column]; ok && len(idxs) > 0 {
        return idxs[0], true
    }
    return nil, false
}
```

---

### Fix 1.6 — Metrics: sync.Map → atomic counter map

**Файл:** `server/internal/metrics/collector.go`  
**Ответственный:** Dev3  
**Время:** 2 часа

```go
// ДО (медленно на горячем пути):
type Collector struct {
    queryCounts sync.Map // map[string]*atomic.Int64
}

func (c *Collector) RecordQuery(queryType, status string, duration time.Duration) {
    key := queryType + ":" + status
    v, _ := c.queryCounts.LoadOrStore(key, new(atomic.Int64))
    v.(*atomic.Int64).Add(1)  // sync.Map при каждом запросе
}

// ПОСЛЕ (быстро — заранее известные ключи):

// Все возможные комбинации type:status известны заранее
type QueryCounters struct {
    SelectOK      atomic.Int64
    SelectError   atomic.Int64
    InsertOK      atomic.Int64
    InsertError   atomic.Int64
    UpdateOK      atomic.Int64
    UpdateError   atomic.Int64
    DeleteOK      atomic.Int64
    DeleteError   atomic.Int64
    DDLOK         atomic.Int64
    DDLError      atomic.Int64
    ExplainOK     atomic.Int64
    TransactionOK atomic.Int64
    OtherOK       atomic.Int64
    OtherError    atomic.Int64
}

type Collector struct {
    startTime time.Time
    queries   QueryCounters
    // ... остальные поля без изменений
}

func (c *Collector) RecordQuery(queryType, status string, duration time.Duration) {
    // Прямое обращение к atomic — никакого sync.Map
    isError := status == "error"
    switch strings.ToLower(queryType) {
    case "select":
        if isError { c.queries.SelectError.Add(1) } else { c.queries.SelectOK.Add(1) }
    case "insert":
        if isError { c.queries.InsertError.Add(1) } else { c.queries.InsertOK.Add(1) }
    case "update":
        if isError { c.queries.UpdateError.Add(1) } else { c.queries.UpdateOK.Add(1) }
    case "delete":
        if isError { c.queries.DeleteError.Add(1) } else { c.queries.DeleteOK.Add(1) }
    case "explain":
        if isError { c.queries.OtherError.Add(1) } else { c.queries.ExplainOK.Add(1) }
    default:
        if isError { c.queries.OtherError.Add(1) } else { c.queries.OtherOK.Add(1) }
    }

    // Гистограмма и т.д. без изменений
}

func (c *Collector) Render() string {
    var b strings.Builder
    b.WriteString("# HELP vaultdb_queries_total Total SQL queries\n")
    b.WriteString("# TYPE vaultdb_queries_total counter\n")

    // Прямые атомарные чтения — быстро
    fmt.Fprintf(&b, `vaultdb_queries_total{type="select",status="ok"} %d`+"\n",
        c.queries.SelectOK.Load())
    fmt.Fprintf(&b, `vaultdb_queries_total{type="select",status="error"} %d`+"\n",
        c.queries.SelectError.Load())
    // ...
    return b.String()
}
```

---

## Волна 2 — Безопасность и производительность

### Fix 2.1 — evalLike: кэш скомпилированных regexp

**Файл:** `server/internal/executor/eval.go`  
**Ответственный:** Dev3  
**Время:** 2 часа

```go
// ДО (1M компиляций для 1M строк):
func evalLike(left, right interface{}) (bool, error) {
    text, _ := left.(string)
    pattern, _ := right.(string)
    // ...строим regexp из pattern...
    re, err := regexp.Compile(b.String()) // КАЖДЫЙ РАЗ!
    return re.MatchString(text), nil
}

// ПОСЛЕ — два подхода в зависимости от паттерна:

// Подход A: для простых паттернов без метасимволов — ручной проход (в 5-10x быстрее regexp)
// Подход B: для сложных паттернов — LRU-кэш скомпилированных regexp

// likeCache хранит последние 256 скомпилированных паттернов
var likeCache = &lruCache{capacity: 256}

type compiledPattern struct {
    pattern string
    simple  bool           // true = можно без regexp
    prefix  string         // для "abc%"
    suffix  string         // для "%abc"
    infix   string         // для "%abc%"
    re      *regexp.Regexp // для сложных паттернов
}

func evalLike(left, right interface{}) (bool, error) {
    text, ok1 := left.(string)
    pattern, ok2 := right.(string)
    if !ok1 || !ok2 { return false, nil }

    // Получить или скомпилировать паттерн (O(1) для повторного вызова)
    cp := likeCache.getOrCompile(pattern)

    if cp.simple {
        // Быстрый путь: ручная проверка без regexp
        switch {
        case cp.prefix != "" && cp.suffix == "" && cp.infix == "":
            // "abc%" — проверить начало
            return strings.HasPrefix(text, cp.prefix), nil
        case cp.suffix != "" && cp.prefix == "" && cp.infix == "":
            // "%abc" — проверить конец
            return strings.HasSuffix(text, cp.suffix), nil
        case cp.infix != "":
            // "%abc%" — проверить содержимое
            return strings.Contains(text, cp.infix), nil
        case cp.prefix == "" && cp.suffix == "" && cp.infix == "":
            // "%" — всегда true
            return true, nil
        default:
            // "abc%def" — HasPrefix + HasSuffix
            return strings.HasPrefix(text, cp.prefix) &&
                   strings.HasSuffix(text, cp.suffix), nil
        }
    }

    // Медленный путь: regexp (но компилируется один раз)
    return cp.re.MatchString(text), nil
}

// likeToPattern разбирает LIKE-паттерн и определяет тип
func compilePattern(pattern string) *compiledPattern {
    cp := &compiledPattern{pattern: pattern}

    // Проверить: содержит ли паттерн только % и _ без спецсимволов regexp
    onlySimple := true
    for _, r := range pattern {
        if r != '%' && r != '_' && regexp.QuoteMeta(string(r)) != string(r) {
            onlySimple = false
            break
        }
    }

    if !strings.Contains(pattern, "_") && onlySimple {
        // Только % → простой паттерн
        cp.simple = true
        parts := strings.Split(pattern, "%")
        // Определить prefix/suffix/infix
        if len(parts) == 2 {
            cp.prefix = parts[0]
            cp.suffix = parts[1]
        } else if len(parts) == 3 && parts[0] == "" && parts[2] == "" {
            cp.infix = parts[1]
        }
        return cp
    }

    // Сложный паттерн — компилируем regexp один раз
    cp.simple = false
    var b strings.Builder
    b.WriteString("(?i)^") // case-insensitive, начало строки
    for _, r := range pattern {
        switch r {
        case '%': b.WriteString(".*")
        case '_': b.WriteString(".")
        default:  b.WriteString(regexp.QuoteMeta(string(r)))
        }
    }
    b.WriteString("$")
    cp.re, _ = regexp.Compile(b.String())
    return cp
}
```

---

### Fix 2.2 — Auth: SHA-256 хеширование токенов

**Файл:** `server/internal/auth/manager.go`  
**Ответственный:** Dev3  
**Время:** 3 часа

```go
// ДО (plaintext):
type Manager struct {
    tokens map[string]string // token → label, plaintext
}

func (m *Manager) ValidateToken(token string) bool {
    _, ok := m.tokens[token]
    return ok
}

// ПОСЛЕ (SHA-256 хеширование):
import (
    "crypto/sha256"
    "encoding/hex"
)

type TokenEntry struct {
    Hash      string    // SHA-256 hex
    Label     string
    CreatedAt time.Time
}

type Manager struct {
    // Хранилище: hash → entry
    // Никогда не храним сам токен
    tokens map[string]TokenEntry
    mu     sync.RWMutex
}

// hashToken вычисляет SHA-256 токена
func hashToken(token string) string {
    h := sha256.Sum256([]byte(token))
    return hex.EncodeToString(h[:])
}

func (m *Manager) ValidateToken(token string) bool {
    if token == "" { return false }
    hash := hashToken(token)
    m.mu.RLock()
    defer m.mu.RUnlock()
    _, ok := m.tokens[hash]
    return ok
}

func (m *Manager) AddToken(token, label string) {
    hash := hashToken(token)
    m.mu.Lock()
    defer m.mu.Unlock()
    m.tokens[hash] = TokenEntry{
        Hash:      hash,
        Label:     label,
        CreatedAt: time.Now(),
    }
}

// Файл tokens.json — хранить только хеши:
// {
//   "tokens": [
//     { "hash": "a1b2c3...", "label": "admin", "created_at": "..." }
//   ]
// }
```

**Важно:** при чтении старого `tokens.json` с plaintext токенами — мигрировать автоматически: прочитать, захешировать, сохранить новый формат.

```go
// Функция миграции старого формата
func (m *Manager) migrateOldFormat(data []byte) error {
    var old struct {
        Tokens []struct {
            Token string `json:"token"` // старое поле
            Label string `json:"label"`
        } `json:"tokens"`
    }
    if err := json.Unmarshal(data, &old); err != nil { return err }

    for _, t := range old.Tokens {
        if t.Token != "" {
            m.AddToken(t.Token, t.Label)
            slog.Warn("migrated plaintext token to hashed format", "label", t.Label)
        }
    }
    return m.save() // сохранить в новом формате
}
```

---

### Fix 2.3 — Убрать или честно задокументировать SEMANTIC_MATCH

**Файл:** `server/internal/executor/eval.go`  
**Ответственный:** Dev3  
**Время:** 2–4 часа

Это самое репутационно опасное замечание. `mockEmbed` — это обман пользователя.

Есть два честных пути:

**Путь A (рекомендуется): подключить реальные эмбеддинги через HTTP**

```go
// internal/ai/embedder.go

// Embedder генерирует реальные эмбеддинги через внешний API.
type Embedder interface {
    Embed(ctx context.Context, text string) ([]float64, error)
}

// HTTPEmbedder вызывает OpenAI / Ollama / любой совместимый API
type HTTPEmbedder struct {
    endpoint string // "https://api.openai.com/v1/embeddings" или "http://localhost:11434/api/embeddings"
    model    string // "text-embedding-3-small" или "nomic-embed-text"
    apiKey   string
    client   *http.Client
}

func (e *HTTPEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
    body, _ := json.Marshal(map[string]interface{}{
        "model": e.model,
        "input": text,
    })

    req, _ := http.NewRequestWithContext(ctx, "POST", e.endpoint, bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    if e.apiKey != "" {
        req.Header.Set("Authorization", "Bearer "+e.apiKey)
    }

    resp, err := e.client.Do(req)
    if err != nil { return nil, fmt.Errorf("embedding API: %w", err) }
    defer resp.Body.Close()

    // Парсим ответ OpenAI-совместимого API
    var result struct {
        Data []struct {
            Embedding []float64 `json:"embedding"`
        } `json:"data"`
    }
    json.NewDecoder(resp.Body).Decode(&result)

    if len(result.Data) == 0 {
        return nil, fmt.Errorf("empty embedding response")
    }
    return result.Data[0].Embedding, nil
}

// NoopEmbedder — заглушка когда AI не настроен
// Явно возвращает ошибку вместо тихого mock
type NoopEmbedder struct{}

func (e *NoopEmbedder) Embed(_ context.Context, text string) ([]float64, error) {
    return nil, fmt.Errorf(
        "AI embedding is not configured. " +
        "Set VAULTDB_AI_API_KEY and ai.provider in vaultdb.yaml to enable SEMANTIC_MATCH. " +
        "See docs: https://vaultdb.io/docs/ai-features")
}
```

```go
// eval.go — SEMANTIC_MATCH теперь вызывает реальный embedder или возвращает понятную ошибку

func evalSemanticMatch(colValue, queryText string, embedder ai.Embedder) (float64, error) {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    colVec, err := embedder.Embed(ctx, colValue)
    if err != nil { return 0, fmt.Errorf("SEMANTIC_MATCH: embed column value: %w", err) }

    queryVec, err := embedder.Embed(ctx, queryText)
    if err != nil { return 0, fmt.Errorf("SEMANTIC_MATCH: embed query: %w", err) }

    return cosineSimilarity(colVec, queryVec), nil
}
```

**Путь B (если нет времени на API интеграцию):** переименовать в `KEYWORD_MATCH` и честно задокументировать что это keyword-based matching без ML.

```go
// Переименовать TOKEN_TYPE с "SEMANTIC_MATCH" на "KEYWORD_MATCH"
// Обновить README и документацию
// Убрать упоминания "AI-Native" в маркетинговых материалах
```

---

### Fix 2.4 — Broadcaster: не терять обновления Live Queries

**Файл:** `server/internal/executor/broadcaster.go`  
**Ответственный:** Dev3  
**Время:** 2 часа

```go
// ДО (silent drop):
select {
case s.Send <- res:
default:
    // Drop notification if channel is full — ПЛОХО
}

// ПОСЛЕ — три стратегии, выбираемые по конфигурации:

type DropPolicy int
const (
    PolicyDrop     DropPolicy = iota // старое поведение — drop с логированием
    PolicyBlock                      // блокировать до освобождения места
    PolicyEvict                      // удалить старейшее и добавить новое
)

type Subscription struct {
    Send       chan *Result
    DropPolicy DropPolicy
    sessionID  string
    logger     *slog.Logger
}

func (s *Subscription) notify(res *Result) {
    switch s.DropPolicy {
    case PolicyBlock:
        // Блокируем отправителя — подходит для надёжных клиентов
        select {
        case s.Send <- res:
        case <-time.After(5 * time.Second):
            // Клиент слишком медленный — отписываем
            s.logger.Warn("subscription timed out, unsubscribing",
                "session", s.sessionID)
            close(s.Send) // сигнализируем клиенту об отписке
        }

    case PolicyEvict:
        // Вытесняем старое обновление — подходит для real-time дашбордов
        select {
        case s.Send <- res:
        default:
            // Канал полон — убираем старейшее и добавляем новое
            select {
            case <-s.Send: // discard oldest
            default:
            }
            s.Send <- res
        }

    default: // PolicyDrop
        select {
        case s.Send <- res:
        default:
            s.logger.Warn("subscription notification dropped, client too slow",
                "session", s.sessionID)
        }
    }
}
```

```yaml
# vaultdb.yaml — настройка политики
server:
  live_queries:
    buffer_size: 256          # был, вероятно, 8-16
    drop_policy: "evict"      # drop | block | evict
    block_timeout_s: 5        # только для policy=block
```

---

### Fix 2.5 — Транзакции: убрать глобальный lock, добавить conflict detection

**Файл:** `server/internal/txmanager/manager.go`  
**Ответственный:** Dev3  
**Время:** 3–4 часа

```go
// ДО — один глобальный mutex на все коммиты:
type Manager struct {
    mu          sync.Mutex // WithCommitLock
    // ...
}

func (m *Manager) WithCommitLock(fn func() error) error {
    m.mu.Lock()         // БЛОКИРУЕТ ВСЕ КОММИТЫ
    defer m.mu.Unlock()
    return fn()
}

// ПОСЛЕ — lock только на уровне таблицы + реальное обнаружение конфликтов:

type Manager struct {
    mu       sync.Mutex
    counter  atomic.Uint64
    active   map[TransactionID]*Transaction

    // Версионный счётчик для каждой таблицы
    // Инкрементируется при каждой записи в таблицу
    tableVersions   map[string]*atomic.Uint64  // "db/table" → версия
    tableVersionsMu sync.RWMutex
}

func (m *Manager) getTableVersion(db, table string) uint64 {
    key := db + "/" + table
    m.tableVersionsMu.RLock()
    v, ok := m.tableVersions[key]
    m.tableVersionsMu.RUnlock()
    if !ok { return 0 }
    return v.Load()
}

func (m *Manager) bumpTableVersion(db, table string) {
    key := db + "/" + table
    m.tableVersionsMu.Lock()
    if _, ok := m.tableVersions[key]; !ok {
        m.tableVersions[key] = &atomic.Uint64{}
    }
    m.tableVersions[key].Add(1)
    m.tableVersionsMu.Unlock()
}

// Transaction теперь хранит снимок версий затронутых таблиц
type Transaction struct {
    XID          TransactionID
    State        TxState
    // Snapshot: версии таблиц на момент BEGIN
    // ключ = "db/table", значение = версия на момент BEGIN
    TableSnapshots map[string]uint64
    Ops            []PendingOp
    StartedAt      time.Time
}

// Begin — фиксируем версии таблиц
func (m *Manager) Begin() *Transaction {
    m.mu.Lock()
    defer m.mu.Unlock()

    xid := TransactionID(m.counter.Add(1))
    return &Transaction{
        XID:            xid,
        State:          TxStateActive,
        TableSnapshots: make(map[string]uint64),
        StartedAt:      time.Now(),
    }
}

// AddOp — записываем снимок версии таблицы при первом обращении
func (m *Manager) AddOp(tx *Transaction, op PendingOp) {
    key := op.DB + "/" + op.Table
    if _, exists := tx.TableSnapshots[key]; !exists {
        tx.TableSnapshots[key] = m.getTableVersion(op.DB, op.Table)
    }
    tx.Ops = append(tx.Ops, op)
}

// Commit — проверяем конфликты и применяем
func (m *Manager) Commit(tx *Transaction, applyFn func([]PendingOp) error) error {
    // Блокируем только таблицы затронутые этой транзакцией
    // Сортируем ключи чтобы избежать deadlock при параллельных транзакциях
    tables := sortedKeys(tx.TableSnapshots)

    locks := make([]*atomic.Uint64, 0, len(tables))
    m.tableVersionsMu.RLock()
    for _, t := range tables {
        if v, ok := m.tableVersions[t]; ok {
            locks = append(locks, v)
        }
    }
    m.tableVersionsMu.RUnlock()

    // Проверяем конфликты: не изменились ли таблицы с момента BEGIN?
    for i, t := range tables {
        currentVersion := uint64(0)
        if i < len(locks) {
            currentVersion = locks[i].Load()
        }
        snapshotVersion := tx.TableSnapshots[t]

        if currentVersion != snapshotVersion {
            return &VaultDBError{
                Code: ErrTxConflict,
                Message: fmt.Sprintf(
                    "transaction conflict: table %q was modified by another transaction "+
                    "(snapshot version=%d, current=%d). ROLLBACK and retry.",
                    t, snapshotVersion, currentVersion),
            }
        }
    }

    // Нет конфликтов — применяем
    if err := applyFn(tx.Ops); err != nil {
        return fmt.Errorf("commit apply: %w", err)
    }

    // Обновляем версии затронутых таблиц
    for _, t := range tables {
        parts := strings.SplitN(t, "/", 2)
        if len(parts) == 2 {
            m.bumpTableVersion(parts[0], parts[1])
        }
    }

    return nil
}
```

---

## Волна 3 — Системные доработки

### Fix 3.1 — Web UI: реализовать React-компоненты

**Файлы:** `server/internal/httpserver/web/src/`  
**Ответственный:** Dev4  
**Время:** 5–7 дней

Ревьюер прав: `return null` в компонентах — это не MVP, это пустое место.
Минимальный рабочий UI для разблокировки ситуации:

#### Приоритет компонентов (от критичного к желательному):

**P0 — без этого UI бесполезен (день 1–2):**

```tsx
// App.tsx — базовая структура с роутингом
export default function App() {
  const [token, setToken] = useState(localStorage.getItem('vaultdb_token') || '');
  const [connected, setConnected] = useState(false);

  if (!token) return <LoginScreen onLogin={setToken} />;

  return (
    <div className="flex h-screen bg-gray-900 text-gray-100">
      <Sidebar />
      <main className="flex-1 flex flex-col overflow-hidden">
        <Header connected={connected} />
        <Routes>
          <Route path="/" element={<QueryPage />} />
          <Route path="/admin" element={<AdminPage />} />
        </Routes>
      </main>
    </div>
  );
}
```

```tsx
// components/QueryEditor.tsx — рабочий редактор
import CodeMirror from '@uiw/react-codemirror';
import { sql } from '@codemirror/lang-sql';

export function QueryEditor({ onExecute }: { onExecute: (sql: string) => void }) {
  const [query, setQuery] = useState('SELECT * FROM users;');
  const [isRunning, setIsRunning] = useState(false);

  const handleRun = async () => {
    if (!query.trim() || isRunning) return;
    setIsRunning(true);
    try {
      await onExecute(query);
    } finally {
      setIsRunning(false);
    }
  };

  return (
    <div className="flex flex-col gap-2 p-4">
      <CodeMirror
        value={query}
        onChange={setQuery}
        extensions={[sql()]}
        theme="dark"
        height="200px"
        className="rounded border border-gray-700"
      />
      <div className="flex gap-2">
        <button
          onClick={handleRun}
          disabled={isRunning}
          className="px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded text-sm
                     disabled:opacity-50 flex items-center gap-2"
        >
          {isRunning ? '⟳ Running...' : '▶ Run (F5)'}
        </button>
        <button
          onClick={() => setQuery('')}
          className="px-4 py-2 bg-gray-700 hover:bg-gray-600 rounded text-sm"
        >
          Clear
        </button>
      </div>
    </div>
  );
}
```

```tsx
// components/ResultTable.tsx — рабочая таблица результатов
export function ResultTable({ result }: { result: QueryResult | null }) {
  if (!result) return (
    <div className="p-4 text-gray-500 text-sm">
      Run a query to see results.
    </div>
  );

  if (!result.ok) return (
    <div className="p-4 bg-red-900/20 border border-red-700 rounded m-4">
      <p className="text-red-400 font-mono text-sm">
        Error {result.error_code}: {result.message}
      </p>
    </div>
  );

  if (result.type === 'affected') return (
    <div className="p-4 text-green-400">
      ✓ Affected rows: {result.affected} ({result.duration_ms}ms)
    </div>
  );

  if (result.type === 'message') return (
    <div className="p-4 text-blue-400">
      ✓ {result.message}
    </div>
  );

  // type === 'rows'
  return (
    <div className="overflow-auto">
      <div className="text-xs text-gray-500 px-4 py-2">
        {result.rows.length} rows · {result.duration_ms}ms
      </div>
      <table className="w-full text-sm">
        <thead className="bg-gray-800 sticky top-0">
          <tr>
            {result.columns.map(col => (
              <th key={col} className="text-left px-3 py-2 font-medium text-gray-300 border-b border-gray-700">
                {col}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {result.rows.map((row, i) => (
            <tr key={i} className="border-b border-gray-800 hover:bg-gray-800/50">
              {row.map((cell, j) => (
                <td key={j} className={`px-3 py-2 font-mono ${cell === null ? 'text-gray-600 italic' : ''}`}>
                  {cell === null ? 'NULL' : String(cell)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
```

```tsx
// hooks/useQuery.ts — рабочий хук
export function useQuery() {
  const token = localStorage.getItem('vaultdb_token') || '';

  const execute = useCallback(async (sql: string, database?: string): Promise<QueryResult> => {
    const resp = await fetch('/api/query', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${token}`,
      },
      body: JSON.stringify({ query: sql, database }),
    });

    const data = await resp.json();
    return data as QueryResult;
  }, [token]);

  return { execute };
}
```

**P1 — важно (день 3–4):**
- `DatabaseTree.tsx` — дерево баз данных (GET /api/databases)
- `LoginScreen.tsx` — экран входа с вводом токена
- `SchemaView.tsx` — просмотр схемы таблицы

**P2 — желательно (день 5–7):**
- `ExplainView.tsx` — визуализация EXPLAIN ANALYZE
- `AdminPage.tsx` — VACUUM, метрики, WAL статус
- История запросов в localStorage

---

### Fix 3.2 — Подключить готовый page-движок к основному Storage Engine

**Файлы:** `server/internal/storage/`, `server/internal/storage/page/`  
**Ответственный:** Dev2  
**Время:** 5–7 дней

Ревьюер обнаружил что `storage/page` и `storage/heap` уже написаны
но не используются. Это критически важное наблюдение.

**Шаг 1:** Аудит готового кода

```bash
# Понять что реально работает в page/ и heap/
find server/internal/storage/page server/internal/storage/heap \
    -name "*.go" | xargs wc -l

# Запустить существующие тесты
cd server && go test ./internal/storage/page/... -v
cd server && go test ./internal/storage/heap/... -v
```

**Шаг 2:** Написать адаптер `PageStorageEngine`

Создать новую реализацию интерфейса `storage.Engine` поверх
готового page-движка, заменяя `FileStorageEngine` (JSON):

```go
// internal/storage/page_engine.go

// PageStorageEngine — реализация Engine поверх бинарного страничного хранилища.
// Заменяет FileStorageEngine с JSON.
type PageStorageEngine struct {
    rootDir  string
    catalog  *catalog.Catalog      // метаданные таблиц
    bufPool  *bufpool.BufferPool   // буферный пул
    walWriter *wal.WALWriter        // WAL
    txMgr    *txmanager.Manager    // менеджер транзакций
    indexes  map[uint32]*IndexManager // tableID → индексы
    mu       sync.RWMutex
}

// Реализация интерфейса Engine:
func (e *PageStorageEngine) ReadCurrentRows(ctx context.Context, db, table string) ([]Row, error) {
    tableID := e.catalog.GetTableID(db, table)
    schema, _ := e.catalog.GetSchema(db, table)
    hf := e.getHeapFile(tableID)

    var rows []Row
    snap := e.txMgr.CurrentSnapshot() // для MVCC

    // Итерация по всем страницам
    for pageNo := uint32(0); pageNo < hf.PageCount(); pageNo++ {
        pid := page.PageID{PageNo: pageNo}
        pg, bufIdx, err := e.bufPool.FetchPage(pid, hf)
        if err != nil { continue }

        h := pg.Header()
        for slot := uint16(0); slot < h.NItems; slot++ {
            tupleBytes := pg.GetTuple(slot)
            if tupleBytes == nil { continue }

            header, data := parseTupleWithHeader(tupleBytes)

            // MVCC: проверить видимость
            if !txn.IsTupleVisible(header, snap, e.txMgr) {
                continue
            }

            row := deserializeRow(data, schema)
            rows = append(rows, row)
        }

        e.bufPool.Unpin(bufIdx, false)
    }

    return rows, nil
}
```

**Шаг 3:** Feature flag для переключения движков

```yaml
# vaultdb.yaml
storage:
  engine: "json"    # json (старый) или page (новый)
  data_dir: "./data"
```

```go
// cmd/vaultdb-server/main.go
var store storage.Engine
switch cfg.Storage.Engine {
case "page":
    store, err = storage.NewPageStorageEngine(cfg.Storage.DataDir)
    slog.Info("using page-based storage engine")
default:
    store, err = storage.NewFileStorageEngine(cfg.Storage.DataDir)
    slog.Info("using JSON storage engine (legacy)")
}
```

Это позволяет переключаться между движками без переписывания остального кода.

---

### Fix 3.3 — Честный EXPLAIN с указанием ограничений

**Файл:** `server/internal/executor/plan.go`  
**Ответственный:** Dev3  
**Время:** 2 часа

Ревьюер предложил переименовать в `EXPLAIN (DEBUG)`. Альтернатива —
добавить честный дисклеймер в вывод.

```go
// formatPlan — добавить секцию "Note" в вывод
func formatPlan(plan QueryPlan) *Result {
    var b strings.Builder
    // ... существующий код ...

    // Честный дисклеймер
    b.WriteString("\nNote: VaultDB uses a rule-based planner (not cost-based).\n")
    b.WriteString("      Plans are chosen by heuristics, not statistics.\n")
    b.WriteString("      Run ANALYZE to collect table statistics (when available).\n")

    return &Result{Type: "message", Message: b.String()}
}
```

Это честнее чем молчать, и закрывает замечание ревьюера без большого рефакторинга.

---

## Распределение по разработчикам

| Задача | Dev1 | Dev2 | Dev3 | Dev4 |
|---|---|---|---|---|
| **Волна 1** | | | | |
| Fix 1.1 — Мусор из git | — | — | — | ✅ |
| Fix 1.2 — VERSION файл | — | — | — | ✅ |
| Fix 1.3 — sendError | — | — | ✅ | — |
| Fix 1.4 — Scanner limit | — | — | ✅ | — |
| Fix 1.5 — IndexManager O(1) | — | ✅ | — | — |
| Fix 1.6 — Metrics atomic | — | — | ✅ | — |
| **Волна 2** | | | | |
| Fix 2.1 — evalLike кэш | — | — | ✅ | — |
| Fix 2.2 — Auth SHA-256 | — | — | ✅ | — |
| Fix 2.3 — Real embeddings | — | — | ✅ | — |
| Fix 2.4 — Broadcaster | — | — | ✅ | — |
| Fix 2.5 — Tx conflict detect | — | — | ✅ | — |
| **Волна 3** | | | | |
| Fix 3.1 — Web UI | — | — | — | ✅ |
| Fix 3.2 — Page engine подключить | — | ✅ | — | — |
| Fix 3.3 — EXPLAIN дисклеймер | — | — | ✅ | — |
| **Финал** | | | | |
| Разбить commands.go | ✅ | — | ✅ | — |
| Обновить README | — | — | — | ✅ |
| Тесты на всё новое | ✅ | ✅ | ✅ | ✅ |

---

## Чеклист приёмки

### Волна 1

| # | Критерий | Проверка |
|---|---|---|
| 1.1 | `git ls-files data_test tmp_data` — пустой вывод | `git ls-files data_test` |
| 1.2 | `cat VERSION` совпадает с `docker image ls \| grep vaultdb` | Визуальная проверка |
| 1.3 | Разрыв соединения логируется, сервер не падает | Тест: `nc localhost 5432` → Ctrl+C |
| 1.4 | INSERT с 10 МБ TEXT не обрезается | Функциональный тест |
| 1.5 | `go test ./internal/index/...` — все тесты зелёные | CI |
| 1.6 | Benchmark: `RecordQuery` x 1M — < 500ms (без sync.Map overhead) | benchmark test |

### Волна 2

| # | Критерий | Проверка |
|---|---|---|
| 2.1 | `SELECT WHERE name LIKE '%x%'` на 100K строк — < 100ms | Benchmark |
| 2.1 | `cat tokens.json` не содержит plaintext токенов | Визуальная проверка |
| 2.2 | `ValidateToken(originalToken)` → true после хеширования | Unit-тест |
| 2.3 | `SEMANTIC_MATCH` без настроенного AI → понятная ошибка, не mock-результат | Функциональный тест |
| 2.3 | `SEMANTIC_MATCH` с настроенным Ollama → реальный результат | Интеграционный тест |
| 2.4 | Live Query при медленном клиенте → обновление не теряется (policy=evict) | Функциональный тест |
| 2.5 | Два параллельных UPDATE одной строки → второй получает ERR_TX_CONFLICT | Функциональный тест |

### Волна 3

| # | Критерий | Проверка |
|---|---|---|
| 3.1 | `http://localhost:8080` — отображается рабочий редактор с таблицей результатов | Визуальная проверка |
| 3.1 | SELECT в Web UI возвращает данные | Функциональный тест |
| 3.1 | DatabaseTree загружает список БД | Визуальная проверка |
| 3.2 | `storage.engine: page` — сервер стартует, SELECT работает | Функциональный тест |
| 3.2 | Crash test с page engine: kill -9 → restart → данные целы | Краш-тест |
| 3.3 | EXPLAIN вывод содержит секцию "Note: rule-based planner" | Функциональный тест |

### Финальные проверки перед релизом

| # | Критерий |
|---|---|
| F1 | `go vet ./...` — 0 предупреждений |
| F2 | `go test ./... -race` — 0 data races |
| F3 | README не содержит неработающих фич (Web UI работает, AI честно задокументирован) |
| F4 | `docker compose up && make seed` → всё поднимается, Web UI открывается |
| F5 | Все пункты из ревью закрыты или содержат задокументированное обоснование отказа |