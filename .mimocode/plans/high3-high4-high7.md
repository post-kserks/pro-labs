# HIGH-3, HIGH-4, HIGH-7 — Анализ и план

## HIGH-3: WAL CRC32 → HMAC-SHA256

**Вердикт: FALSE POSITIVE — не менять**

### Анализ
- CRC32 (wal.go:207, 277) используется для целостности данных, не для безопасности
- WAL на локальном диске — если атакующий имеет FS access, он уже может менять heap-файлы
- Threat model: битовые сбои на диске, неполные записи — CRC32 покрывает это
- SHA-256: ~100x медленнее CRC32, WAL — горячий путь (fsync на каждую запись)
- Проект уже использует crypto/hmac/sha256 в auth — но для других целей

### Действие
- Добавить комментарий в wal.go: buildRecord, объясняющий назначение CRC32
- НЕ менять алгоритм — это не security issue

## HIGH-4: Токен в URL query parameter

**Вердикт: ОСТАВИТЬ с documentation**

### Анализ
- Только `/api/live` (SSE) принимает `?token=` (auth/manager.go:135-136)
- Browser EventSource API не поддерживает кастомные заголовки — limitation спецификации
- C++ клиенты (TUI, Shell) используют HTTP headers — `?token=` для них не нужен
- Web UI (React) использует REST API — токен в заголовках
- Токен короткоживущий (генерируется на один запуск)

### Действие
- Добавить security warning комментарий в tokenFromRequest
- Добавить комментарий в vaultdb.yaml.example о рисках SSE auth
- НЕ удалять `?token=` — это было бы breaking change для SSE клиентов

## HIGH-7: Dockerfile FROM scratch

**Вердикт: FALSE POSITIVE — не менять**

### Анализ
- `FROM scratch` — минимальная атакуемая поверхность (стандарт для Go static binaries)
- CA certs уже скопированы (строка 22)
- Бинарник статический (CGO_ENABLED=0)
- Health check через HTTP `/health` — shell не нужен
- Debugging через логи/метрики/HTTP endpoints

### Действие
- Создать `.dockerignore` для уменьшения build context
- НЕ менять scratch → distroless — это ухудшит безопасность
