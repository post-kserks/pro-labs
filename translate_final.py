#!/usr/bin/env python3
"""Final comprehensive pass: translate ALL remaining Russian to English."""
import re
import os

def has_russian(text):
    return bool(re.search(r'[а-яА-ЯёЁ]', text))

def translate_file(filepath):
    with open(filepath, 'r', encoding='utf-8') as f:
        lines = f.readlines()
    
    if not any(has_russian(line) for line in lines):
        return False
    
    translated = []
    for line in lines:
        if has_russian(line):
            # Translate common standalone comment patterns
            replacements = {
                # Very common patterns across many files
                "состояние транзакции": "transaction state",
                "конфликт транзакций": "transaction conflict",
                "настройки optimistic concurrency control: retry и backoff": "optimistic concurrency control settings: retry and backoff",
                "одна буферизованная операция внутри транзакции": "a single buffered operation within a transaction",
                "активная транзакция одной сессии": "active transaction of a single session",
                "управляет транзакциями всех сессий": "manages transactions across all sessions",
                "Добавляет операцию в буфер транзакции": "Adds an operation to the transaction buffer",
                "При превышении SpillThreshold сериализует буфер во временный файл": "When SpillThreshold is exceeded, serializes the buffer to a temporary file",
                "точки расширения для сериализации операций": "extension points for operation serialization",
                "сериализует операции построчно": "serializes operations line by line",
                "возвращает операции: из памяти или из файла": "returns operations: from memory or from file",
                "фиксирует текущую позицию буфера": "records the current buffer position",
                "удаляет маркер savepoint'а": "removes the savepoint marker",
                "усекает буфер до позиции savepoint'а": "truncates the buffer to the savepoint position",
                "проверяет конфликты и применяет операции транзакции": "checks conflicts and applies transaction operations",
                "очищает буфер и удаляет spill файл": "clears the buffer and deletes the spill file",
                "возвращает true, если транзакция": "returns true if the transaction",
                "гарантирует, что счётчик txid не меньше": "guarantees that txid counter is at least",
                "удаляет старые spill файлы": "removes old spill files",
                "вычисляет арифметику с интервалами дат": "computes date interval arithmetic",
                "пытается распарсить строку как timestamp": "attempts to parse a string as timestamp",
                "преобразует SQL формат даты в Go layout": "converts SQL date format to Go layout",
                "проверяет является ли строка SQL интервалом": "checks if a string is a SQL interval",
                "округляет timestamp до указанной части": "truncates timestamp to the specified part",
                "извлекает часть из timestamp": "extracts a part from a timestamp",
                "вычисляет разницу во времени": "computes time difference",
                "преобразует строку в дату": "converts string to date",
                "форматирует timestamp в строку": "formats timestamp to string",
                "прибавляет интервал к дате": "adds an interval to a date",
                "вычитает интервал из даты": "subtracts an interval from a date",
                "вычисляет разницу между двумя датами": "computes difference between two dates",
                "кэшированный результат SELECT запроса": "cached SELECT query result",
                "LRU кэш результатов SELECT запросов": "LRU cache for SELECT query results",
                "Automatically инвалидируется при INSERT/UPDATE/DELETE": "Automatically invalidated on INSERT/UPDATE/DELETE",
                "Поддерживает TTL для устаревших записей": "Supports TTL for stale entries",
                "создаёт новый кэш результатов": "creates a new result cache",
                "возвращает кэшированный результат или nil": "returns cached result or nil",
                "сохраняет результат в кэш": "stores result in cache",
                "удаляет все записи, затронутые указанной таблицей": "removes all entries affected by the given table",
                "очищает весь кэш": "clears the entire cache",
                "возвращает статистику кэша": "returns cache statistics",
                "строит ключ кэша для SELECT запроса": "builds cache key for SELECT query",
                "категория запроса для счётчиков": "query category for counters",
                "счётчики ok/error по каждой категории запросов": "ok/error counters for each query category",
                "сводит StatementType к категории счётчика": "maps StatementType to counter category",
                "хранит все метрики сервера": "stores all server metrics",
                "Счётчики запросов по категориям": "Query counters by category",
                "Гистограмма времени выполнения": "Execution time histogram",
                "Gauge метрики": "Gauge metrics",
                "WAL метрики": "WAL metrics",
                "Индексные метрики": "Index metrics",
                "Метрики хранилища": "Storage metrics",
                "Rate limiter метрики": "Rate limiter metrics",
                "Auth rate limiter метрики": "Auth rate limiter metrics",
                "обновляет статистику хранилища": "updates storage statistics",
                "удаляет метрики таблиц, которые больше не существуют": "removes metrics for tables that no longer exist",
                "вычисляет селективность предиката": "estimates predicate selectivity",
                "оценка стоимости плана": "plan cost estimate",
                "оптимизатор запросов": "query optimizer",
                "выбирает лучший метод доступа для каждой таблицы": "chooses the best access method for each table",
                "оптимизирует SELECT запрос": "optimizes SELECT query",
                "выбирает метод соединения": "chooses join method",
                "оценивает стоимость": "estimates cost",
                "интерфейс для всех типов индексов": "interface for all index types",
                "создаёт индекс по типу": "creates an index by type",
                "хранит все индексы одной таблицы": "stores all indexes for a single table",
                "обновляет статистику хранилища": "updates storage statistics",
                "удаляет метрики таблиц": "removes table metrics",
                "удалённая версия не индексируется": "deleted versions are not indexed",
                "roduct type for DDL objects": "object type for DDL objects",
                "виртуальная таблица для хранения DDL-объектов": "virtual table for storing DDL objects",
                "сохраняет DDL-объект": "stores a DDL object",
                "загружает DDL-объект по имени и типу": "loads a DDL object by name and type",
                "удаляет DDL-объект по имени и типу": "deletes a DDL object by name and type",
                "загружает все объекты указанного типа": "loads all objects of the given type",
                "область видимости CTE для конкретного запроса": "CTE scope for a specific query",
                "определение CTE": "CTE definition",
                "создаёт новую область видимости CTE": "creates a new CTE scope",
                "добавляет вложенную область видимости": "adds a nested scope",
                "регистрирует CTE в текущей области видимости": "registers a CTE in the current scope",
                "ищет CTE по имени в цепочке областей видимости": "looks up a CTE by name in the scope chain",
                "выполняет CTE и кэширует результат": "executes a CTE and caches the result",
                "выполняет CTEStatement": "executes a CTEStatement",
                "разобранный LIKE-паттерн": "compiled LIKE pattern",
                "проверяет текст против паттерна": "matches text against pattern",
                "LRU-кэш скомпилированных паттернов": "LRU cache of compiled patterns",
                "хэш-индекс на один столбец таблицы": "hash index for a single table column",
                "B-tree индекс для range queries и ordering": "B-tree index for range queries and ordering",
                "составной индекс по нескольким столбцам": "composite index on multiple columns",
                "генерирует эмбеддинги для SEMANTIC_MATCH и AI_EMBED": "generates embeddings for SEMANTIC_MATCH and AI_EMBED",
                "генерирует векторное представление текста": "generates a vector representation of text",
                "вызывает OpenAI / Ollama / любой совместимый embeddings API": "calls OpenAI / Ollama / any compatible embeddings API",
                "создаёт embedder для OpenAI-совместимого API": "creates an embedder for OpenAI-compatible API",
                "заглушка, когда AI не настроен": "stub when AI is not configured",
                "детерминированный keyword-based embedder для тестов": "deterministic keyword-based embedder for tests",
                "не является ML-моделью": "not an ML model",
                "не должен использоваться в продакшене": "should not be used in production",
                "ротатор лог-файлов": "log file rotator",
                "создаёт новый ротатор": "creates a new rotator",
                "записывает данные в лог-файл": "writes data to log file",
                "выполняет ротацию лог-файла": "performs log file rotation",
                "удаляет самый старый бэкап": "removes the oldest backup",
                "закрывает ротатор": "closes the rotator",
                "синхронизирует файл на диск": "syncs file to disk",
                "возвращает io.Writer для использования с log/slog": "returns io.Writer for use with log/slog",
                "управляет блокировками на уровне страниц": "manages page-level locks",
                "позволяет конкурентные записи в разные страницы одной таблицы": "allows concurrent writes to different pages of the same table",
                "создаёт новый менеджер блокировок": "creates a new lock manager",
                "блокирует страницу для чтения": "acquires read lock on page",
                "снимает блокировку чтения со страницы": "releases read lock on page",
                "блокирует страницу для записи": "acquires write lock on page",
                "снимает блокировку записи со страницы": "releases write lock on page",
                "блокирует все страницы таблицы для записи": "acquires write lock on all pages of a table",
                "снимает блокировки со всех страниц таблицы": "releases locks on all pages of a table",
                "массив кортежей таблицы в порядке страниц/слотов": "table tuples in page/slot order",
                "создаёт составной индекс": "creates a composite index",
            }
            
            for ru, en in replacements.items():
                if ru in line:
                    line = line.replace(ru, en)
            
            # Handle common comment prefix patterns
            line = re.sub(r'//\s*([А-Я])', lambda m: '// ' + m.group(1), line)
        
        translated.append(line)
    
    content = ''.join(translated)
    if content != ''.join(lines):
        with open(filepath, 'w', encoding='utf-8') as f:
            f.write(content)
        return True
    return False

def main():
    base = "/home/golem/g_proj/pro-labs/server"
    count = 0
    for root, dirs, files in os.walk(base):
        for f in files:
            if f.endswith('.go'):
                path = os.path.join(root, f)
                if translate_file(path):
                    count += 1
    
    print(f"Total files translated: {count}")
    
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
