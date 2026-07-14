# AI and Semantic Search

VaultDB integrates AI-powered semantic search via pluggable embedding providers.

## Overview

The AI subsystem provides:
- Text-to-vector embedding generation
- Semantic similarity search
- Pluggable provider architecture
- LRU caching for performance

## Configuration

```yaml
ai:
  provider: "openai"        # "openai" or "ollama"
  endpoint: "https://api.openai.com/v1"  # API URL
  model: "text-embedding-3-small"         # Model name
  api_key: "sk-..."         # Or use VAULTDB_AI_API_KEY env var
  cache_enabled: true        # Enable LRU cache
  cache_size: 1000           # Max cached entries
```

## Supported Providers

### OpenAI-Compatible

Works with OpenAI API and any OpenAI-compatible endpoint:

```yaml
ai:
  provider: "openai"
  endpoint: "https://api.openai.com/v1"
  model: "text-embedding-3-small"
  api_key: "sk-..."
```

### Ollama

Works with local Ollama instance:

```yaml
ai:
  provider: "ollama"
  endpoint: "http://localhost:11434"
  model: "nomic-embed-text"
```

## SQL Functions

### AI_EMBED(text)

Generate an embedding vector for the given text.

```sql
SELECT AI_EMBED('VaultDB is a SQL database');
-- Returns: [0.012, -0.034, 0.056, ...]
```

### SEMANTIC_MATCH(column, query)

Find rows semantically similar to the query text.

```sql
SELECT * FROM articles
WHERE SEMANTIC_MATCH(content, 'database indexing techniques');
```

## Creating a Table with Embeddings

```sql
CREATE TABLE articles (
    id INT AUTO_INCREMENT PRIMARY KEY,
    title TEXT,
    content TEXT,
    embedding VECTOR
);

-- Insert with manual embedding
INSERT INTO articles (title, content, embedding)
VALUES ('B-tree Indexes', 'B-tree indexes are...', '[0.1, 0.2, ...]');

-- Insert with AI-generated embedding
INSERT INTO articles (title, content, embedding)
VALUES ('Hash Indexes', 'Hash indexes provide...', AI_EMBED('Hash indexes provide fast equality lookups'));
```

## Semantic Search Example

```sql
-- Find articles about indexing
SELECT id, title
FROM articles
WHERE SEMANTIC_MATCH(content, 'how to speed up queries')
ORDER BY similarity DESC
LIMIT 5;
```

## Embedding Cache

When `cache_enabled: true`, embeddings are cached using SHA-256 hashes of the input text:

- ** eviction**: LRU (Least Recently Used)
- **Capacity**: Configurable (default 1000)
- **Thread-safe**: Uses `sync.RWMutex`
- **TTL**: Optional (0 = no expiry)

## Retry Logic

The HTTP embedder retries failed requests:

| Attempt | Delay | Condition |
|---------|-------|-----------|
| 1 | 500ms | HTTP 429, 5xx, timeout |
| 2 | 1s | Same |
| 3 | 2s | Same |

Non-retryable errors (401, 400) fail immediately.

## NoopEmbedder

When AI is not configured, `AI_EMBED()` and `SEMANTIC_MATCH()` return a descriptive error guiding the user to set up the embedding provider.

## Programmatic Usage

```go
import "vaultdb/internal/core/ai"

// Create embedder
embedder := ai.NewHTTPEmbedder("https://api.openai.com/v1", "text-embedding-3-small", "sk-...")

// Optionally wrap with cache
cached := ai.NewCachedEmbedder(embedder, 1000, 0)

// Generate embedding
vec, err := cached.Embed(ctx, "Hello, world!")
```
