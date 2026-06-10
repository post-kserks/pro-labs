# ⚡ VaultDB: The Next-Gen Hybrid SQL Engine

VaultDB — это современная гибридная СУБД, сочетающая мощь реляционной алгебры, гибкость документных хранилищ и возможности векторных баз данных для AI.

---

## 🚀 Быстрый старт (Deployment)

### 1. Требования
- **Go 1.21+** (для сервера)
- **CMake & C++ Compiler** (для TUI-клиента)
- **Docker** (опционально, для быстрого запуска)

### 2. Запуск через Docker (Рекомендуется)
```bash
docker-compose up --build
```
После запуска:
- **TCP SQL Port:** 5432
- **REST API & Web UI:** http://localhost:8080
- **Metrics/Health:** http://localhost:5433/metrics

### 3. Ручная сборка и запуск
**Сервер (Go):**
```bash
cd server
go build -o vaultdb-server cmd/vaultdb-server/main.go
./vaultdb-server --data ./data
```

**TUI-Клиент (C++):**
```bash
cd client
mkdir -p build && cd build
cmake ..
make -j
# Запуск из корня проекта:
cd ../..
./client/build/tui/vaultdb-tui --host 127.0.0.1 --port 5432
```

---

## 🛠 Информация для разработчиков

### Зачем существует VaultDB?
Современные приложения перегружены "зоопарком" баз данных: PostgreSQL для метаданных, Redis для кеша, Pinecone для векторов и MongoDB для гибких конфигов. VaultDB создана, чтобы **устранить эту фрагментацию**. 

Это **единый движок**, который одинаково хорошо справляется с транзакциями, семантическим поиском и неструктурированными данными.

### Ключевые отличия от конкурентов

| Особенность | VaultDB | PostgreSQL | MongoDB | Vector DBs |
| :--- | :--- | :--- | :--- | :--- |
| **Схема** | Flexible (INFER SCHEMA) | Rigid (Strict) | Schema-less | Rigid/None |
| **AI/Vector** | SQL-синтаксис + внешний embedder | via Extensions | No | Native |
| **Real-time** | Live Queries (Native) | via Triggers/CDC | Change Streams | No |
| **API** | Auto REST + OpenAPI | Third-party (PostgREST)| Native | Limited |
| **Embeddable** | Yes (Go Library) | No | No | Some |

### Преимущества VaultDB

#### 1. AI-Ready SQL
Семантический поиск встроен в SQL, но сами эмбеддинги VaultDB не генерирует —
для `SEMANTIC_MATCH` и `AI_EMBED` нужен внешний embedding-провайдер
(OpenAI, Ollama или любой OpenAI-совместимый API), настроенный в `vaultdb.yaml`:

```yaml
ai:
  provider: "ollama"
  endpoint: "http://localhost:11434/api/embeddings"
  model: "nomic-embed-text"
  # api_key: "..."  # или переменная окружения VAULTDB_AI_API_KEY
```

```sql
-- Поиск по смыслу, а не по словам
SELECT content FROM docs 
WHERE content SEMANTIC_MATCH 'как настроить бэкап' 
LIMIT 5;
```

Без настроенного провайдера эти операции возвращают понятную ошибку
конфигурации — никаких «фейковых» AI-результатов.

#### 2. Schema-Free Power
Создавайте таблицы без предварительного проектирования. VaultDB сама поймет типы данных при первой вставке.
```sql
CREATE TABLE settings INFER SCHEMA;
INSERT INTO settings (id, val) VALUES (1, '{"theme": "dark", "zoom": 1.2}');
-- Запрос к JSON прямо в SQL
SELECT val->>'theme' FROM settings WHERE id = 1;
```

#### 3. Live Queries
Подписывайтесь на результаты запросов. Клиент получит обновление мгновенно при изменении данных в таблице. Идеально для дашбордов и совместной работы.

#### 4. Встроенные миграции (DevOps First)
Забудьте о внешних утилитах. Миграции — это часть ядра СУБД.
```sql
CREATE MIGRATION add_user_bio ('ALTER TABLE users ADD COLUMN bio TEXT;');
APPLY MIGRATION add_user_bio;
```

#### 5. Автоматический REST API
Как только вы создали таблицу, у неё появляется готовый REST-эндпоинт с поддержкой фильтрации:
`GET /api/databases/mydb/tables/users/data?age=gt.18`

#### 6. Web UI
На `http://localhost:8080` доступен встроенный веб-интерфейс: SQL-редактор
с историей запросов, таблица результатов, дерево баз/таблиц и просмотр схемы.
Исходники — `server/internal/httpserver/web` (React + Vite), собранный bundle
встраивается в бинарник сервера.

#### 7. Движки хранения
По умолчанию данные хранятся в JSON-файлах с версионированием строк
(time travel, `AS OF`). Дополнительно доступен экспериментальный бинарный
страничный движок (slotted pages, 8 КБ, контрольные суммы):

```yaml
storage:
  engine: "page"   # json (по умолчанию) | page (экспериментальный)
```

Ограничения page-движка: вторичные индексы пока не поддерживаются.

---

## 🏗 Архитектура
Подробное описание внутреннего устройства (парсер, executor, storage, WAL, Time Travel) находится в файле [ARCHITECTURE.md](./ARCHITECTURE.md).

## 🧪 Тестирование
Для запуска полного цикла тестов (включая реляционную логику, JOIN, Window functions и AI):
```bash
cd server
go test -v ./internal/executor
```

---
*VaultDB — Database for the AI Era.*
