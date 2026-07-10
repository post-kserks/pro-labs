#!/usr/bin/env python3
"""Translate Russian comments to English in Go source files."""
import re
import os

# Comprehensive translation map for common Russian patterns
TRANSLATIONS = {
    # Common comment patterns
    "Проверяем кэш": "Check cache",
    "Страницы нет в кэше": "Page not in cache",
    "Находим свободный слот": "Find free slot",
    "Добавляем в кэш": "Add to cache",
    "Обновляем каталог": "Update catalog",
    "Останавливаем фоновую горутину": "Stop the background goroutine",
    "Останавливаем предыдущую горутину": "Stop the previous goroutine",
    "Не удалось вытеснить": "Could not evict",
    "Не должно happen": "Should not happen",
    "Быстрый путь": "Fast path",
    "Медленный путь": "Slow path",
    "Записываем checkpoint": "Write checkpoint",
    "Помечаем tuple как удалённый": "Mark tuple as deleted",
    "Восстанавливаем tuple": "Restore tuple",
    "Страница не существует": "Page does not exist",
    "Страница полна": "Page is full",
    "Освобождаем": "Release",
    "Получаем ссылку": "Get reference",
    "Проверка конфликтов": "Conflict checking",
    "Применение выполняется": "Application is performed",
    "Проверяем существование": "Check existence",
    "Ищем существующую запись": "Find existing record",
    "Удаляем старую запись": "Delete old record",
    "Вставляем новую": "Insert new one",
    "Используем существующий": "Use existing",
    "Получаем схему": "Get schema",
    "Получаем количество строк": "Get row count",
    "Если таблица пуста": "If table is empty",
    "Собираем статистику": "Collect statistics",
    "Записываем в WAL": "Write to WAL",
    "Читаем ops": "Read ops",
    "Откатываем": "Roll back",
    "Проверяем порог": "Check threshold",
    "Дописываем в существующий файл": "Append to existing file",
    "Освобождаем RAM": "Free RAM",
    "Проверяем что": "Check that",
    "Проверяем что удалено": "Check what was deleted",
    "Проверяем что остались": "Check what remains",
    "Вставляем значения": "Insert values",
    "Удаляем": "Delete",
    "Rebuild с новыми данными": "Rebuild with new data",
    "Проверяем новые значения": "Check new values",
    "Проверяем что старые значения удалены": "Check old values are removed",
    "Вставляем несколько строк": "Insert multiple rows",
    "Проверяем что все позиции найдены": "Check all positions found",
    "Вставляем 1000 значений": "Insert 1000 values",
    "Проверяем что все значения найдены": "Check all values found",
    "Range query": "Range query",
    "Вставляем значения с нулевым паддингом": "Insert values with zero padding",
    "Вставляем значения": "Insert values",
    "Удаляем старые бэкапы": "Delete old backups",
    "Находим самый старый": "Find the oldest",
    "Удаляем из списка": "Delete from list",
    "Создаём новый файл": "Create new file",
    "Переименовываем текущий файл": "Rename current file",
    "Удаляем самый старый бэкап": "Delete oldest backup",
    "Закрываем текущий файл": "Close current file",
    "Проверяем нужна ли ротация": "Check if rotation is needed",
    "Записываем данные в лог-файл": "Write data to log file",
    "Сессия ещё жива": "Session is still alive",
    "Пул полон": "Pool is full",
    "Закрываем сессию": "Close session",
    "Закрываем все оставшиеся сессии": "Close all remaining sessions",
    "Закрываем пул и все сессии": "Close pool and all sessions",
    "Попытка взять из пула": "Attempt to get from pool",
    "Проверяем лимит": "Check limit",
    "Создаём новую сессию": "Create new session",
    "Попытка вернуть в пул": "Attempt to return to pool",
    "Клиент слишком медленный": "Client too slow",
    "Канал полон": "Channel full",
    "Теперь место точно есть": "Now there is definitely room",
    "Если всё равно не влезло": "If it still didn't fit",
    "Один select: попытка отправить": "Single select: attempt to send",
    "Это O(1), не цикл": "This is O(1), not a loop",
    "Данный тест проверяет": "This test verifies",
    "Нагрузка": "Workload",
    "Симулируем ошибку записи": "Simulate write error",
    "Липкая spillErr": "Sticky spillErr",
    "Спиллим сразу": "Spill immediately",
    "Симулируем": "Simulate",
    "Финальный": "Final",
    "Проверяем что данные откатались": "Verify data was rolled back",
    "Проверяем что транзакция активна": "Verify transaction is active",
    "Close должен откатить": "Close should roll back",
    "После close транзакция не должна быть активна": "After close, transaction should not be active",
    "Проверяем что данные откатались": "Verify data was rolled back",
    "Роллбэк должен откатить": "Rollback should undo",
    "Rollback message should report": "Rollback message should report",
    "Transaction should be cleared": "Transaction should be cleared",
    "Запрос COUNT": "COUNT query",
    "Вставка новой строки": "Insert new row",
    "Повторный запрос": "Repeat query",
    "Benchmark с кэшем": "Benchmark with cache",
    "Benchmark без кэша": "Benchmark without cache",
    "Benchmark": "Benchmark",
    "Очищаем кэш перед каждым запросом": "Clear cache before each query",
    "Первый запрос — MISS": "First query — MISS",
    "Второй запрос — HIT": "Second query — HIT",
    "Проверяем что кэш работает": "Verify cache is working",
    "Другой WHERE": "Different WHERE",
    "Другая функция": "Different function",
    "Запрос 1": "Query 1",
    "Запрос 2": "Query 2",
    "Запрос 3": "Query 3",
    "Immediate": "Immediate",
    "After TTL": "After TTL",
    "Заполняем тысячей разных IP": "Fill with thousands of different IPs",
    "Ждём пока окно истечёт": "Wait for window to expire",
    "Все старые записи": "All old entries",
    "Кто-то успел скомпилировать параллельно": "Someone compiled it in parallel",
    "Компилируем без блокировки": "Compile without holding lock",
    "Паттерн без '%'": "Pattern without '%'",
    "Только '%' и литералы": "Only '%' and literals",
    "В буфере должны остаться": "Buffer should still contain",
    "Буфер полон, читателя нет": "Buffer full, no reader",
    "не должно паниковать": "should not panic",
    "Откатываем уже применённые": "Roll back already applied",
    "Применение упало частично": "Application partially failed",
    "Записать COMMIT в WAL": "Write COMMIT to WAL",
    "Не смогли записать COMMIT": "Could not write COMMIT",
    "Проверка конфликтов и применение": "Conflict checking and application",
    "Вытесняем через clock-sweep": "Evict via clock-sweep",
    "Неизвестный savepoint": "Unknown savepoint",
    "s2 создан позже s1": "s2 was created after s1",
    "До отката оба видны": "Before rollback both visible",
    "После отката": "After rollback",
    "savepoint вне транзакции запрещён": "savepoint outside transaction is forbidden",
    "Заполняем": "Fill",
    "Прогоняем": "Run",
    "Восстанавливаем каждую строку": "Restore each row",
    "Нельзя сливать": "Cannot merge",
    "Идём по убыванию индекса": "Iterate indices in reverse order",
    "Создаём shadow file": "Create shadow file",
    "Записываем начало": "Write start",
    "Записываем завершение": "Write completion",
    "Атомарная замена": "Atomic replacement",
    "Открываем новый heap file": "Open new heap file",
    "Пересобираем страницу": "Rebuild page",
    "Пишем в shadow file": "Write to shadow file",
    "Flush all dirty pages": "Flush all dirty pages",
    "Перед началом и после завершения": "Before start and after completion",
    "Эмитятся WAL-записи": "WAL entries are emitted",
    "Используется безопасный подход": "A safe approach is used",
    "Данные пишутся во временную директорию": "Data is written to a temporary directory",
    "Затем атомарно заменяют": "Then atomically replaces",
}

# Also handle inline Russian in comments (single-line)
INLINE_TRANSLATIONS = {
    "максимальное количество страниц в кэше": "maximum number of pages in cache",
    "PageID → индекс в buffers": "PageID → index in buffers",
    "фиксированный массив буферов": "fixed array of buffers",
    "текущая позиция clock hand": "current clock hand position",
    "текущее количество страниц": "current number of pages",
    "WAL для записи full page images": "WAL for writing full page images",
    "останавливает фоновую горутину": "stops the background goroutine",
    "heap, из которого загружена страница": "heap from which the page was loaded",
    "количество активных пользователей": "number of active users",
    "full page image уже записан в WAL": "full page image already written to WAL",
    "имя БД (для WAL full page image)": "database name (for WAL full page image)",
    "имя таблицы (для WAL full page image)": "table name (for WAL full page image)",
    "страница была изменена и не записана на диск": "page was modified and not yet written to disk",
    "LSN транзакции, последний раз изменившей страницу": "LSN of the transaction that last modified the page",
    "нельзя вытеснить": "cannot be evicted",
    "для write-back": "for write-back",
    "open table": "open table",
    "текст": "text",
    "jsonb": "jsonb",
    "Инвертированный индекс": "Inverted index",
    "Обратный маппинг": "Reverse mapping",
    "позиция строки → токены": "row position → tokens",
    "Второй шанс": "Second chance",
    "Пустой слот — используем": "Empty slot — use it",
    "Запинована — пропускаем": "Pinned — skip",
    "Второй шанс — уменьшаем usage count": "Second chance — decrement usage count",
    "Вытесняем эту страницу": "Evict this page",
    "не зациклиться": "avoid infinite loop",
    "Все страницы запинованы": "All pages are pinned",
    "для предотвращения deadlock": "to prevent deadlock",
    "Сидит между": "Sits between",
    "кэширует прочитанные страницы в памяти": "caches read pages in memory",
    "модификации применяются только в кэше": "modifications are applied only in cache",
    "Страницы записываются на диск при вытеснении": "Pages are written to disk on eviction",
    "вместо LRU используется алгоритм": "instead of LRU, the algorithm",
    "При обращении к странице": "On page access",
    "При вытеснении": "On eviction",
    "clock hand сканирует массив": "clock hand scans the array",
    "страницы с usage > 0 теряют по одному за проход": "pages with usage > 0 lose one per pass",
    "с usage == 0 вытесняются": "pages with usage == 0 are evicted",
    "освобождает e.mu": "releases e.mu",
    "write=true — полный Lock": "write=true — full Lock",
    "write=false — RLock": "write=false — RLock",
    "Caller должен вызвать": "Caller must call",
    "когда закончит": "when done",
    "для записи": "for writes",
    "для чтения": "for reads",
    "это ответственность вызывающего": "that is the caller's responsibility",
    "Либо это best-effort": "This is best-effort",
    "Страницы, уже находящиеся в кэше, пропускаются": "Pages already in cache are skipped",
    "Ошибки чтения логируются": "Read errors are logged",
    "но не прерывают вызывающий код": "but do not interrupt the caller",
    "Вторичные индексы поддерживаются": "Secondary indexes are supported",
    "три фазы": "three phases",
    "как в PostgreSQL ARIES": "as in PostgreSQL ARIES",
    "ВАЖНО: mu не удерживается": "IMPORTANT: mu is not held",
    "нет deadlock": "no deadlock",
    "тот же порядок": "same order",
    "используемые для WAL full page image": "used for WAL full page image",
    "Опциональные db/table параметры": "Optional db/table parameters",
    "pinCnt увеличивается на 1": "pinCnt is incremented by 1",
    "вызов UnpinRequired обязателен": "calling UnpinPage is required",
    "Сидит между page engine и HeapFile": "Sits between page engine and HeapFile",
}

def has_russian(text):
    """Check if text contains Cyrillic characters."""
    return bool(re.search(r'[а-яА-ЯёЁ]', text))

def translate_file(filepath):
    """Translate Russian comments in a Go source file."""
    with open(filepath, 'r', encoding='utf-8') as f:
        content = f.read()
    
    if not has_russian(content):
        return False
    
    modified = False
    
    # Apply standalone-line translations
    for ru, en in TRANSLATIONS.items():
        if ru in content:
            content = content.replace(ru, en)
            modified = True
    
    # Apply inline translations
    for ru, en in INLINE_TRANSLATIONS.items():
        if ru in content:
            content = content.replace(ru, en)
            modified = True
    
    if modified:
        with open(filepath, 'w', encoding='utf-8') as f:
            f.write(content)
    
    return modified

def main():
    base = "/home/golem/g_proj/pro-labs/server"
    count = 0
    for root, dirs, files in os.walk(base):
        for f in files:
            if f.endswith('.go'):
                path = os.path.join(root, f)
                if translate_file(path):
                    count += 1
                    print(f"  translated: {os.path.relpath(path, base)}")
    print(f"\nTotal files translated: {count}")
    
    # Check remaining
    remaining = 0
    for root, dirs, files in os.walk(base):
        for f in files:
            if f.endswith('.go'):
                path = os.path.join(root, f)
                with open(path, 'r') as fh:
                    if has_russian(fh.read()):
                        remaining += 1
    print(f"Files still with Russian: {remaining}")

if __name__ == '__main__':
    main()
