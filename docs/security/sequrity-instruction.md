# VaultDB Security Assurance — Техническое задание
## Автоматизация проверки безопасности + алгоритмы самостоятельного аудита

---

| Атрибут | Значение |
|---|---|
| Документ | VaultDB Security Assurance TZ v1.0.0 |
| Связь с другими документами | Реализует Фазу 0 и Фазу 3 из VaultDB Strategic Roadmap |
| Статус | Активен |
| Важная оговорка | Этот документ — внутренняя гигиена, НЕ замена независимого внешнего security-аудита |

---

## 1. Область действия и модель угроз

### 1.1 Что покрывает документ

Два параллельных трека:

**Трек A — Автоматизация.** Инструменты, встроенные в CI/CD, которые
проверяют код и работающий сервер без участия человека: статический
анализ, fuzzing, сканирование зависимостей, динамическое тестирование
запущенного сервера.

**Трек B — Ручные алгоритмы.** Пошаговые процедуры для инженера,
которые нельзя (пока) полностью автоматизировать: анализ архитектурных
решений, проверка криптографических примитивов на предмет логических
ошибок, review privilege escalation сценариев.

### 1.2 Модель угроз VaultDB (адаптация STRIDE под СУБД)

| Категория угрозы | Применительно к VaultDB | Приоритет |
|---|---|---|
| Injection | SQL injection через PREPARE/EXECUTE/CREATE FUNCTION, protocol injection через кастомный протокол | Критический |
| Broken Authentication | Обход токена, подделка HMAC, timing-атаки на сравнение токенов | Критический |
| Broken Access Control | Обход RLS-политик, privilege escalation между ролями | Критический |
| Cryptographic Failures | Слабый DEK, утечка ключа в логи/дампы памяти, неправильный nonce reuse в AES-GCM | Критический |
| Data Integrity | Подмена страниц heap, подмена WAL-записей, обход hash-chain audit log | Высокий |
| Denial of Service | Исчерпание памяти через большие запросы, zip-bomb в COPY, connection exhaustion | Высокий |
| Insecure Deserialization | Некорректный парсинг протокольных сообщений приводящий к RCE/panic | Высокий |
| Supply Chain | Уязвимые зависимости (Go modules, npm для Web UI), скомпрометированный Docker base image | Средний |
| Security Misconfiguration | TLS отключён по умолчанию, auth отключён в dev-конфиге попавшем в prod | Средний |
| Information Disclosure | Утечка деталей через сообщения об ошибках, timing side-channel в сравнении паролей/токенов | Средний |

---

## 2. Содержание

3. Трек A — Автоматизированный security-конвейер
4. SAST — статический анализ
5. Сканирование зависимостей и supply chain
6. Secret Scanning
7. Fuzzing — расширенный набор
8. DAST — динамическое тестирование запущенного сервера
9. Сканирование Docker-образа
10. Трек B — Алгоритмы ручного самоаудита
11. Классификация серьёзности и SLA
12. Расписание проверок
13. Отчётность
14. Распределение задач
15. Чеклист приёмки

---

## 3. Трек A — Автоматизированный security-конвейер

### 3.1 Общая схема конвейера

```
pre-commit hook (локально, до коммита)
  - gitleaks (secrets)
  - gofmt + go vet
        |
        v
PR gate (обязателен для мержа)
  - gosec (SAST)
  - govulncheck (известные CVE в зависимостях)
  - go test -race (concurrency)
  - semgrep custom rules (VaultDB-специфичные паттерны)
        |
        v
Nightly (полный прогон, не блокирует разработку)
  - FuzzParse, FuzzProtocol, FuzzEncryption (2 часа каждый)
  - DAST против тестового инстанса (SQLi, auth bypass попытки)
  - Trivy сканирование Docker-образа
        |
        v
Weekly
  - Ручной алгоритм из Трека B (по ротации подсистем)
  - Regression benchmark полный прогон
        |
        v
Перед релизом (major/minor версия)
  - Полный прогон всех ручных алгоритмов Трека B
  - testssl.sh против TLS-конфигурации
  - Обновление security-отчёта в docs/security/
```

---

## 4. SAST — статический анализ

### 4.1 gosec — базовый статический анализ Go

```yaml
# .github/workflows/security.yml

sast-gosec:
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - name: Run gosec
      run: |
        go install github.com/securego/gosec/v2/cmd/gosec@latest
        gosec -fmt=sarif -out=gosec-results.sarif -severity=medium ./...
    - name: Upload SARIF
      uses: github/codeql-action/upload-sarif@v3
      with:
        sarif_file: gosec-results.sarif
```

Обязательные правила gosec для VaultDB (не отключать без явного обоснования
в комментарии вида `#nosec G-XXX -- причина`):

| Правило | Проверяет | Критичность для VaultDB |
|---|---|---|
| G101 | Хардкод секретов в коде | Критично — прямое пересечение с TDE |
| G201/G202 | SQL-запросы через конкатенацию строк | Критично — прямое пересечение с injection |
| G401/G402/G403 | Слабая криптография (MD5, DES, малый размер RSA) | Критично — пересечение с encryption TZ |
| G404 | math/rand вместо crypto/rand для security-контекста | Критично — nonce, DEK, токены должны использовать crypto/rand |
| G304 | Path traversal при работе с файлами | Высокий — актуально для COPY FROM/TO, data_dir |

### 4.2 semgrep — кастомные правила под архитектуру VaultDB

Общие правила не ловят специфичные для VaultDB паттерны. Пишем свои.

```yaml
# .semgrep/vaultdb-sql-injection.yml

rules:
  - id: vaultdb-sql-string-concat-reparse
    languages: [go]
    severity: ERROR
    message: >
      Обнаружена конкатенация пользовательского ввода в строку,
      которая затем передаётся в parser.Parse(). Это потенциальная
      SQL injection даже при наличии AST-парсера — если конкатенация
      происходит ДО парсинга, а не после (bind parameters).
    patterns:
      - pattern: |
          $STR := $A + $USERINPUT + $B
          ...
          parser.Parse($STR)
      - pattern-not: |
          parser.Parse($LITERAL_STRING)

  - id: vaultdb-crypto-rand-required
    languages: [go]
    severity: ERROR
    message: >
      math/rand используется в криптографическом контексте (nonce/DEK/token).
      Обязателен crypto/rand.
    patterns:
      - pattern-either:
          - pattern: math/rand.Read(...)
      - pattern-inside: |
          func $FUNC(...) {
            ...
          }
      - metavariable-regex:
          metavariable: $FUNC
          regex: (?i)(nonce|dek|token|key|salt)

  - id: vaultdb-dek-not-zeroized
    languages: [go]
    severity: WARNING
    message: >
      Функция работает с DEK/passphrase, но не вызывает Zeroize()
      перед выходом из области видимости. Ключ может остаться в памяти.
    patterns:
      - pattern-inside: |
          func $FUNC(...) {
            ...
          }
      - metavariable-regex:
          metavariable: $FUNC
          regex: (?i)(decrypt|dek|passphrase)
      - pattern-not-regex: Zeroize\(\)
```

```bash
# Запуск в CI
semgrep --config .semgrep/ --error --json --output semgrep-results.json ./server
```

### 4.3 staticcheck — дополнительный уровень

```bash
staticcheck ./...
# ловит игнорируемые ошибки (_ = err), что напрямую связано
# с найденными в код-ревью проблемами (sendError)
```

---

## 5. Сканирование зависимостей и supply chain

### 5.1 govulncheck — известные CVE в Go-зависимостях

```yaml
supply-chain-go:
  runs-on: ubuntu-latest
  steps:
    - name: Install govulncheck
      run: go install golang.org/x/vuln/cmd/govulncheck@latest
    - name: Scan
      run: govulncheck ./...
      # Обязателен для PR gate — падает при найденной эксплуатируемой уязвимости
      # (govulncheck умеет отличать "уязвимость есть в зависимости" от
      # "уязвимый код реально вызывается" — меньше ложных срабатываний)
```

### 5.2 npm audit для Web UI

```yaml
supply-chain-npm:
  runs-on: ubuntu-latest
  steps:
    - working-directory: server/internal/httpserver/web
      run: |
        npm audit --audit-level=high
        npm audit signatures
```

### 5.3 Dependabot / автоматическое обновление

```yaml
# .github/dependabot.yml

version: 2
updates:
  - package-ecosystem: "gomod"
    directory: "/server"
    schedule:
      interval: "weekly"
    open-pull-requests-limit: 5

  - package-ecosystem: "npm"
    directory: "/server/internal/httpserver/web"
    schedule:
      interval: "weekly"

  - package-ecosystem: "docker"
    directory: "/"
    schedule:
      interval: "weekly"
```

### 5.4 SBOM (Software Bill of Materials)

Enterprise-заказчики часто требуют SBOM для compliance (Executive Order
14028 в США, аналогичные требования в других юрисдикциях).

```bash
# Генерация SBOM в формате CycloneDX при каждом релизе
go install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@latest
cyclonedx-gomod mod -json -output sbom.json ./server

# Прикладывается к каждому GitHub Release как артефакт
```

---

## 6. Secret Scanning

### 6.1 gitleaks — pre-commit + CI

```yaml
# .pre-commit-config.yaml

repos:
  - repo: https://github.com/gitleaks/gitleaks
    rev: v8.18.0
    hooks:
      - id: gitleaks
```

```yaml
# CI — повторная проверка на случай обхода pre-commit
secret-scan:
  runs-on: ubuntu-latest
  steps:
    - uses: gitleaks/gitleaks-action@v2
      env:
        GITLEAKS_LICENSE: ${{ secrets.GITLEAKS_LICENSE }}
```

### 6.2 Кастомные правила под VaultDB-специфичные секреты

```toml
# .gitleaks.toml — дополнительные правила

[[rules]]
id = "vaultdb-api-token"
description = "VaultDB API token"
regex = '''vdb_sk_[a-f0-9]{32}'''
tags = ["vaultdb", "token"]

[[rules]]
id = "vaultdb-encryption-passphrase-env"
description = "Possible hardcoded encryption passphrase"
regex = '''VAULTDB_ENCRYPTION_PASSPHRASE\s*=\s*["'][^"']{4,}["']'''
tags = ["vaultdb", "encryption"]
```

### 6.3 Проверка что секреты не попадают в логи (runtime-проверка)

```go
// tools/security/log_secret_test.go

// TestNoSecretsInLogs запускает сервер, выполняет операции с токенами
// и ключами, перехватывает весь вывод логов и проверяет отсутствие
// известных секретных паттернов.
func TestNoSecretsInLogs(t *testing.T) {
    var logBuf bytes.Buffer
    srv := startTestServer(t, &logBuf)

    passphrase := "test-super-secret-passphrase-12345"
    token := srv.GenerateToken()

    srv.Execute(fmt.Sprintf("CREATE DATABASE secure_test ENCRYPTED WITH KEY '%s';", passphrase))
    srv.ExecuteWithToken(token, "SELECT 1;")

    logs := logBuf.String()

    assert.NotContains(t, logs, passphrase, "passphrase leaked into logs")
    assert.NotContains(t, logs, token, "auth token leaked into logs")
}
```

---

## 7. Fuzzing — расширенный набор

Дополняет FuzzParse из Strategic Roadmap Фазы 0 специфичными для
security fuzz-целями.

### 7.1 FuzzProtocol — протокольный фаззинг

```go
// internal/server/fuzz_protocol_test.go

// FuzzProtocol проверяет что сервер не падает и не зависает
// на произвольных байтах, отправленных как protocol-сообщение.
func FuzzProtocol(f *testing.F) {
    f.Add([]byte(`{"id":"1","token":"x","query":"SELECT 1;"}` + "\n"))
    f.Add([]byte(`{`))
    f.Add([]byte(strings.Repeat("A", 1<<20)))
    f.Add([]byte("\x00\x01\x02\xff\xfe"))

    f.Fuzz(func(t *testing.T, data []byte) {
        conn := connectToTestServer(t)
        defer conn.Close()

        conn.SetDeadline(time.Now().Add(2 * time.Second))
        conn.Write(data)

        assertServerStillAlive(t)
    })
}
```

### 7.2 FuzzEncryption — фаззинг расшифровки

```go
// internal/crypto/fuzz_decrypt_test.go

// FuzzDecryptPage проверяет что попытка расшифровать произвольные
// (повреждённые/подделанные) байты как зашифрованную страницу
// корректно возвращает ошибку, а не паникует.
func FuzzDecryptPage(f *testing.F) {
    em, _ := crypto.NewEncryptionManager(testDEK, "v1")

    validNonce, validCiphertext, _ := em.EncryptPage([]byte("test data"), testPageID)
    f.Add(validNonce, validCiphertext)
    f.Add(make([]byte, 12), make([]byte, 100))
    f.Add([]byte{}, []byte{})

    f.Fuzz(func(t *testing.T, nonce, ciphertext []byte) {
        defer func() {
            if r := recover(); r != nil {
                t.Fatalf("DecryptPage panicked on malformed input: %v", r)
            }
        }()
        _, _ = em.DecryptPage(nonce, ciphertext, testPageID)
    })
}
```

### 7.3 FuzzWALRecovery — фаззинг recovery на повреждённом WAL

```go
// internal/wal/fuzz_recovery_test.go

func FuzzWALRecovery(f *testing.F) {
    validWAL := generateValidWALFile()
    f.Add(validWAL)
    f.Add(corruptByteAt(validWAL, 50))
    f.Add(truncateAt(validWAL, len(validWAL)/2))

    f.Fuzz(func(t *testing.T, walBytes []byte) {
        tmpDir := t.TempDir()
        os.WriteFile(filepath.Join(tmpDir, "vaultdb.wal"), walBytes, 0644)

        defer func() {
            if r := recover(); r != nil {
                t.Fatalf("WAL recovery panicked: %v", r)
            }
        }()

        engine, err := storage.NewPageStorageEngine(tmpDir)
        _ = err
        if engine != nil {
            engine.Close()
        }
    })
}
```

---

## 8. DAST — динамическое тестирование запущенного сервера

### 8.1 Автоматизированные атаки через собственный протокол

```go
// tools/security/dast/injection_attempts.go

var injectionPayloads = []string{
    `SELECT * FROM users WHERE id = 1; DROP TABLE users;--`,
    `SELECT * FROM users WHERE name = 'x' OR '1'='1'`,
    `SELECT * FROM users WHERE id = (SELECT 1 UNION SELECT password FROM admin)`,
    `PREPARE p AS SELECT * FROM users WHERE id = $1; EXECUTE p('1 OR 1=1')`,
    `CREATE FUNCTION x() RETURNS INT LANGUAGE SQL AS 'SELECT 1; DROP DATABASE mydb;'`,
}

func TestDASTSQLInjection(t *testing.T) {
    client := connectToTestServer(t)

    for _, payload := range injectionPayloads {
        result := client.Execute(payload)
        assertNoUnauthorizedSideEffects(t, client, payload, result)
    }
}
```

### 8.2 Тестирование обхода аутентификации

```go
// tools/security/dast/auth_bypass.go

func TestDASTAuthBypass(t *testing.T) {
    client := connectToTestServerNoAuth(t)

    cases := []struct {
        name  string
        token string
    }{
        {"empty token", ""},
        {"malformed token", "not-a-real-token"},
        {"sql-injection-like token", "' OR '1'='1"},
        {"null byte token", "vdb_sk_\x00\x00\x00"},
        {"extremely long token", strings.Repeat("a", 1<<20)},
        {"valid-looking but wrong token", "vdb_sk_" + strings.Repeat("0", 32)},
    }

    for _, c := range cases {
        t.Run(c.name, func(t *testing.T) {
            result := client.ExecuteWithToken(c.token, "SELECT 1;")
            assert.Equal(t, 2001, result.ErrorCode, "expected ERR_UNAUTHORIZED for: %s", c.name)
        })
    }
}
```

### 8.3 Timing-атака на сравнение токенов

```go
// tools/security/dast/timing_attack_test.go

func TestTokenComparisonTiming(t *testing.T) {
    validToken := generateValidToken()

    measureTime := func(candidate string) time.Duration {
        start := time.Now()
        for i := 0; i < 1000; i++ {
            _ = authManager.ValidateToken(candidate)
        }
        return time.Since(start)
    }

    farOff := "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
    closeMatch := validToken[:30] + "zz"

    tFarOff := measureTime(farOff)
    tCloseMatch := measureTime(closeMatch)

    ratio := float64(tCloseMatch) / float64(tFarOff)
    assert.Less(t, ratio, 1.15,
        "possible timing side-channel in token comparison: ratio=%.3f", ratio)
}
```

Требование к реализации (следствие этого теста): проверка токена
обязана использовать `subtle.ConstantTimeCompare` или эквивалент,
а не прямое сравнение строк/хешей через `==`.

### 8.4 TLS-конфигурация — testssl.sh

```bash
#!/usr/bin/env bash
# tools/security/dast/tls_scan.sh

docker run --rm -it drwetter/testssl.sh \
    --severity HIGH \
    --protocols \
    --vulnerable \
    "${VAULTDB_HOST}:${VAULTDB_TLS_PORT}"

# Обязательные критерии прохождения:
#   - TLS 1.0/1.1 отключены
#   - Слабые cipher suites отсутствуют
#   - Нет уязвимости к Heartbleed/POODLE/BEAST
```

### 8.5 Rate limiting / DoS-устойчивость

```go
// tools/security/dast/dos_test.go

func TestConnectionExhaustion(t *testing.T) {
    const attemptConnections = 10000

    var success, refused atomic.Int64
    var wg sync.WaitGroup

    for i := 0; i < attemptConnections; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            conn, err := net.DialTimeout("tcp", testServerAddr, 2*time.Second)
            if err != nil {
                refused.Add(1)
                return
            }
            defer conn.Close()
            success.Add(1)
        }()
    }
    wg.Wait()

    assertServerStillAlive(t)
    t.Logf("accepted: %d, refused: %d", success.Load(), refused.Load())
}

func TestLargePayloadDoS(t *testing.T) {
    client := connectToTestServer(t)

    hugeString := strings.Repeat("A", 2<<30)
    result := client.Execute(fmt.Sprintf(
        `INSERT INTO t VALUES ('%s');`, hugeString))

    assert.NotNil(t, result)
    assertServerStillAlive(t)
}
```

---

## 9. Сканирование Docker-образа

```yaml
# CI — Trivy сканирование финального образа

docker-security-scan:
  runs-on: ubuntu-latest
  steps:
    - name: Build image
      run: docker build -t vaultdb-scan-target .
    - name: Trivy scan
      uses: aquasecurity/trivy-action@master
      with:
        image-ref: vaultdb-scan-target
        format: sarif
        output: trivy-results.sarif
        severity: CRITICAL,HIGH
        exit-code: 1
    - name: Upload results
      uses: github/codeql-action/upload-sarif@v3
      with:
        sarif_file: trivy-results.sarif
```

```bash
# tools/security/verify_minimal_image.sh
docker run --rm vaultdb-scan-target sh -c "echo test" 2>&1 | grep -q "executable file not found" \
    && echo "OK: no shell in image" \
    || (echo "FAIL: shell present in production image" && exit 1)
```

---

## 10. Трек B — Алгоритмы ручного самоаудита

Каждый алгоритм имеет фиксированную структуру: Предусловие -> Шаги ->
Ожидаемый результат -> Критерий провала. Выполняется инженером по
расписанию (раздел 12), результат фиксируется в отчёте (раздел 13).

---

### Алгоритм A — SQL Injection Manual Review

**Предусловие:** доступ к исходному коду internal/parser, internal/executor.

**Шаги:**

1. Построить список всех мест, где строка проходит через parser.Parse()
   более одного раза за жизненный цикл запроса (миграции, CREATE FUNCTION,
   EXECUTE с параметрами, CALL).
   ```bash
   grep -rn "parser.Parse(" server/internal/ | grep -v "_test.go"
   ```
2. Для каждого найденного места — проверить: строка, передаваемая в
   Parse(), построена из константы кода ИЛИ из значения, прошедшего
   через типизированный Value (bind parameter), а не через прямую
   конкатенацию с пользовательским string.
3. Явно протестировать PREPARE/EXECUTE с payload, содержащим SQL
   мета-символы в значении параметра (не в самом запросе):
   ```sql
   PREPARE p AS SELECT * FROM users WHERE name = $1;
   EXECUTE p('''; DROP TABLE users; --');
   ```
   Ожидаемо: строка ищется буквально как значение, DROP TABLE не
   выполняется.
4. Проверить CREATE FUNCTION ... LANGUAGE SQL AS '...' — тело функции
   не должно позволять выполнение дополнительных statements сверх
   объявленных при вызове.
5. Проверить обработку идентификаторов (имена таблиц/столбцов) отдельно
   от значений — идентификаторы не параметризуются как Value, поэтому
   требуют отдельной проверки на инъекцию через information_schema-подобные
   системные запросы, если такие есть.

**Ожидаемый результат:** ни один тест-кейс из шага 3 не приводит к
выполнению незаявленной операции.

**Критерий провала:** любое непреднамеренное выполнение DDL/DML сверх
исходно заявленного оператора — Critical severity, блокирует релиз.

---

### Алгоритм B — Authentication & Authorization Review

**Предусловие:** запущенный тестовый инстанс с настроенной аутентификацией.

**Шаги:**

1. Проверить формат хранения токенов — убедиться что в tokens.json
   (или его аналоге для page engine) нет plaintext-значений:
   ```bash
   grep -E "vdb_sk_[a-f0-9]{32}" data/auth/tokens.json && echo "FAIL: plaintext token found"
   ```
2. Проверить что ValidateToken использует constant-time comparison:
   ```bash
   grep -A5 "func.*ValidateToken" server/internal/auth/manager.go | grep -q "subtle.ConstantTimeCompare\|hmac.Equal" \
       || echo "FAIL: possible non-constant-time comparison"
   ```
3. Проверить, что VAULTDB_AUTH_SECRET (HMAC-секрет) обязателен в
   production-режиме и не имеет дефолтного захардкоженного значения.
4. Если реализован RLS (CREATE POLICY) — проверить обход политики через:
   - прямой доступ администратора без роли
   - SQL-инъекцию в USING (...) выражение политики
   - обход через JOIN с таблицей без RLS, раскрывающий данные
     защищённой таблицы косвенно
5. Проверить срок жизни токенов — есть ли механизм отзыва
   (revocation) скомпрометированного токена без перезапуска сервера.

**Ожидаемый результат:** все токены в хранилище — только хеши; сравнение
constant-time; RLS не обходится ни одним из трёх векторов шага 4.

**Критерий провала:** обнаружение plaintext-токена, timing-разница
более 15% между совпадающим и несовпадающим кандидатом, либо
любой рабочий обход RLS — Critical severity.

---

### Алгоритм C — Encryption at Rest Review

**Предусловие:** БД с включённым TDE (ENCRYPTED), доступ к сырым
файлам heap на диске.

**Шаги:**

1. Создать БД с шифрованием, вставить заведомо узнаваемую строку
   ("UNIQUE_MARKER_STRING_12345"), выполнить checkpoint.
2. Сделать hexdump/strings по файлу .heap на диске:
   ```bash
   strings data/databases/secure_test/tables/*/0000.heap | grep "UNIQUE_MARKER"
   ```
   Ожидаемо: ничего не найдено — строка не видна в открытом виде.
3. Аналогично для WAL-файла:
   ```bash
   strings data/wal/vaultdb.wal | grep "UNIQUE_MARKER"
   ```
4. Проверить что при изменении одного байта в середине зашифрованной
   страницы сервер обнаруживает нарушение целостности (GCM auth tag
   fail), а не молча возвращает повреждённые данные.
5. Проверить что DEK не остаётся в файле подкачки/core dump —
   получить core dump и поискать байты DEK:
   ```bash
   gcore <pid>
   strings core.<pid> | grep -f <(echo -n "$KNOWN_DEK_HEX")
   ```
6. Проверить ротацию KEK — убедиться что после rotate-kek старый
   KEK физически не требуется для чтения данных.

**Ожидаемый результат:** шаги 2, 3 — маркер не найден нигде на диске.
Шаг 4 — явная ошибка аутентификации. Шаг 5 — DEK не найден в дампе
после явного вызова Zeroize().

**Критерий провала:** обнаружение читаемых данных на диске в любом виде
(heap, WAL, core dump) при включённом шифровании — Critical severity,
блокирует любые заявления о TDE.

---

### Алгоритм D — Network / Transport Review

**Предусловие:** сервер с TLS включённым и выключенным (два прогона).

**Шаги:**

1. С TLS выключенным — снять трафик через tcpdump/Wireshark между
   клиентом и сервером, убедиться что видны данные в открытом виде.
2. С TLS включённым — повторить перехват, убедиться что данные
   нечитаемы в захваченном трафике.
3. Прогнать testssl.sh (раздел 8.4), проверить отсутствие TLS 1.0/1.1,
   небезопасных cipher suites, самоподписанных сертификатов в production.
4. Проверить обработку некорректных/просроченных клиентских
   сертификатов при включённом mTLS.

**Ожидаемый результат:** TLS 1.3 (или минимум 1.2 с сильными cipher
suites), корректная работа mTLS при его включении.

**Критерий провала:** активный TLS 1.0/1.1, слабые cipher suites в
production-профиле — High severity.

---

### Алгоритм E — WAL / Recovery Tamper Review

**Предусловие:** сервер с WAL, доступ к остановке процесса в произвольный момент.

**Шаги:**

1. Запустить транзакцию из N операций, остановить сервер (kill -9)
   после операции N/2, до COMMIT.
2. Перезапустить, проверить что ни одна из N/2 операций не применена.
3. Повторить с остановкой ПОСЛЕ записи COMMIT в WAL, но до применения
   к heap — проверить что recovery доводит транзакцию до конца (redo).
4. Вручную подменить байт в середине WAL-записи (после checksum),
   перезапустить — recovery должен обнаружить повреждение через CRC32.
5. Проверить поведение при повреждении в зашифрованном WAL —
   расшифровка с неверным GCM tag должна давать чёткую ошибку.

**Ожидаемый результат:** во всех сценариях — либо полный rollback,
либо полный redo, никогда не частичное/неопределённое состояние.

**Критерий провала:** любое частичное применение транзакции после
краша — Critical severity, прямое нарушение ACID-гарантий.

---

### Алгоритм F — Privilege Escalation / RLS Bypass Review

**Предусловие:** минимум две роли настроены (admin, user) с разными RLS-политиками.

**Шаги:**

1. Подключиться токеном роли user, попытаться выполнить операции,
   зарезервированные за admin (DROP DATABASE, VACUUM, CREATE INDEX
   на чужой таблице) — все должны быть отклонены.
2. Проверить обход через CREATE FUNCTION ... LANGUAGE SQL — функция,
   созданная пользователем user, не должна выполняться с правами
   создателя выше собственных прав вызывающего (явно решить, какая
   модель принята — DEFINER или INVOKER).
3. Проверить обход RLS через составной запрос (JOIN, подзапрос,
   агрегатную функцию).
4. Проверить обход через EXPLAIN — план выполнения не должен
   раскрывать данные защищённых строк через оценки статистики.

**Ожидаемый результат:** ни один из векторов не даёт доступ за пределы
разрешённого политикой.

**Критерий провала:** любой рабочий обход — Critical severity.

---

### Алгоритм G — Denial of Service / Resource Exhaustion Review

**Предусловие:** тестовый инстанс с настроенными лимитами.

**Шаги:**

1. Прогнать автоматизированные тесты 8.5 (connection exhaustion, large payload).
2. Проверить EXPLAIN ANALYZE на заведомо дорогой запрос — сервер должен
   иметь возможность прервать долгий запрос по таймауту.
3. Проверить поведение COPY FROM с некорректным/бесконечным потоком данных.
4. Проверить лимиты на глубину рекурсии в парсере.

**Ожидаемый результат:** все сценарии дают контролируемый отказ.

**Критерий провала:** OOM-kill сервера или зависание дольше заданного
таймаута — High severity.

---

### Алгоритм H — Audit Log Tamper Review

**Предусловие:** audit log с hash-chain включён.

**Шаги:**

1. Выполнить серию DDL-операций, зафиксировать состояние audit log.
2. Напрямую (в обход SQL-интерфейса) изменить значение одной записи в
   середине лога.
3. Выполнить VERIFY AUDIT LOG — убедиться что подмена обнаружена.
4. Проверить что audit log недоступен на запись через обычный
   INSERT/UPDATE/DELETE от любой роли.
5. Проверить ротацию/архивирование audit log — хеш-цепочка должна
   продолжаться через границу архивных файлов.

**Ожидаемый результат:** любая подмена обнаруживается.

**Критерий провала:** необнаруженная подмена записи — Critical
severity.

---

## 11. Классификация серьёзности и SLA

| Severity | Определение | SLA на исправление | Блокирует релиз |
|---|---|---|---|
| Critical | Обход аутентификации, SQL injection, утечка ключей шифрования, обход RLS, порча данных при краше | Немедленно | Да |
| High | DoS без аутентификации, слабый TLS, отсутствие rate limiting, timing-атака | В течение текущей итерации | Да, для minor/major |
| Medium | Отсутствие best-practice, избыточная информация в ошибках | В течение месяца | Нет |
| Low | Стилистические замечания | По возможности | Нет |

---

## 12. Расписание проверок

| Проверка | Частота | Блокирует |
|---|---|---|
| gitleaks (pre-commit) | При каждом коммите | Локальный коммит |
| gosec, govulncheck, race-tests | Каждый PR | Мерж PR |
| semgrep custom rules | Каждый PR | Мерж PR |
| FuzzParse, FuzzProtocol, FuzzEncryption, FuzzWALRecovery | Nightly, 2 часа каждый | Алертит |
| DAST (injection, auth bypass, timing) | Nightly | Алертит |
| Trivy Docker scan | Nightly + релиз | Релиз при Critical/High |
| Ручные алгоритмы A-H (ротация) | Еженедельно | Фиксируется в отчёте |
| Полный прогон всех алгоритмов A-H | Перед major/minor релизом | Да |
| testssl.sh | Перед релизом | Да, при слабом TLS |
| SBOM генерация | При каждом релизе | Нет, обязательный артефакт |
| Независимый внешний security-аудит | Раз в год / перед крупной сделкой | Отдельное решение |

---

## 13. Отчётность

### 13.1 Формат отчёта по ручному алгоритму

```markdown
# Security Self-Audit Report — Algorithm [A-H]

Дата: 2025-XX-XX
Исполнитель: [имя]
Алгоритм: [название]
Версия VaultDB: X.Y.Z

## Результаты по шагам

| Шаг | Статус | Комментарий |
|---|---|---|
| 1 | Пройден | |
| 2 | Пройден | |
| 3 | Частично | Обнаружено X, детали в Findings |
| 4 | Провален | Критично — см. Findings |

## Findings

### Finding 1 — [Severity]
Описание: ...
Как воспроизвести: ...
Рекомендация: ...
Статус исправления: Open / In Progress / Fixed / Accepted Risk

## Общий вердикт
[Pass / Pass with findings / Fail]
```

Отчёты накапливаются в docs/security/self-audits/YYYY-MM-DD-algorithm-X.md.

### 13.2 Дашборд статуса безопасности

```json
GET /admin/security-status

{
  "last_full_audit": "2025-06-15",
  "open_findings": {
    "critical": 0,
    "high": 1,
    "medium": 3,
    "low": 7
  },
  "automated_checks": {
    "last_gosec_run": "2025-08-15T02:00:00Z",
    "last_fuzz_run": "2025-08-15T02:00:00Z",
    "last_trivy_scan": "2025-08-15T02:00:00Z",
    "all_passing": true
  },
  "manual_algorithms_coverage": {
    "A_sql_injection": "2025-08-01",
    "B_auth": "2025-08-08",
    "C_encryption": "2025-08-15",
    "D_network": "2025-07-25",
    "E_wal_recovery": "2025-07-18",
    "F_rls_bypass": "2025-07-11",
    "G_dos": "2025-07-04",
    "H_audit_log": "2025-06-27"
  }
}
```

---

## 14. Распределение задач

| Задача | Ответственный | Приоритет |
|---|---|---|
| CI-интеграция gosec + govulncheck + semgrep | Dev3 | Немедленно |
| gitleaks pre-commit + CI | Dev4 | Немедленно |
| FuzzProtocol, FuzzEncryption, FuzzWALRecovery | Dev2 + Dev3 | Немедленно |
| DAST-скрипты (injection, auth bypass, timing) | Dev3 | Высокий |
| testssl.sh интеграция + TLS review | Dev3 | Высокий |
| Trivy Docker scan в CI | Dev4 | Высокий |
| SBOM генерация | Dev4 | Средний |
| Constant-time token comparison (если ещё не сделано) | Dev3 | Критично, до релиза |
| Ручные алгоритмы A-H — первый полный прогон | Вся команда | Высокий |
| Security dashboard endpoint | Dev3 | Средний |
| Документация процесса + шаблоны отчётов | Dev1 | Средний |

---

## 15. Чеклист приёмки

### Автоматизация (Трек A)

| # | Критерий | Проверка |
|---|---|---|
| 1 | gosec встроен в PR gate, падает на Critical/High находках | Тестовый PR с намеренной уязвимостью |
| 2 | semgrep custom rules ловят SQL string concat паттерн | Тестовый коммит с уязвимым кодом |
| 3 | govulncheck блокирует PR при известной эксплуатируемой CVE | Тест с уязвимой зависимостью |
| 4 | gitleaks блокирует коммит с тестовым секретом | Тестовый коммит |
| 5 | FuzzProtocol работает 2+ часа nightly без падения сервера | CI лог |
| 6 | FuzzEncryption не находит паник на произвольных байтах | CI лог |
| 7 | Trivy scan падает на Critical уязвимости в образе | Тестовый образ с уязвимой зависимостью |
| 8 | DAST timing-attack тест проходит (ratio менее 1.15) | CI лог |
| 9 | testssl.sh не находит TLS 1.0/1.1/слабых cipher suites | Отчёт testssl.sh |

### Ручные алгоритмы (Трек B)

| # | Критерий | Проверка |
|---|---|---|
| 10 | Алгоритм A выполнен, ни один injection payload не сработал | Отчёт |
| 11 | Алгоритм B выполнен, токены только в виде хешей, timing constant | Отчёт |
| 12 | Алгоритм C выполнен, данные не найдены в открытом виде на диске | Отчёт |
| 13 | Алгоритм D выполнен, TLS 1.3 подтверждён | Отчёт |
| 14 | Алгоритм E выполнен, транзакции атомарны при любой точке краша | Отчёт |
| 15 | Алгоритм F выполнен, RLS не обходится ни одним вектором | Отчёт |
| 16 | Алгоритм G выполнен, DoS-сценарии дают контролируемый отказ | Отчёт |
| 17 | Алгоритм H выполнен, подмена audit log обнаруживается | Отчёт |

### Процесс

| # | Критерий |
|---|---|
| 18 | Все 8 отчётов ручных алгоритмов сохранены в docs/security/self-audits/ |
| 19 | Security dashboard endpoint отдаёт актуальные данные |
| 20 | Ни одного открытого Critical finding перед объявлением релиза |