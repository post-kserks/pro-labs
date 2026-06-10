# VaultDB Storage Engine — Полное ТЗ на переработку
## Page-based Storage · Buffer Pool · B-Tree · MVCC · Cost-based Planner

---

| Атрибут | Значение |
|---|---|
| Документ | VaultDB Core Rewrite TZ v1.0.0 |
| Трудозатраты | Вся команда |
| Приоритет | Критический — фундаментальная переработка |
| Цель | Превратить VaultDB в production-grade СУБД |

---

## Предисловие: порядок имеет значение

Эти компоненты строго зависят друг от друга. Нарушить порядок — значит
переписывать готовый код. Единственный правильный путь:

```
Фаза 1: Page Format + Heap File Manager
    │
    ▼
Фаза 2: Buffer Pool Manager
    │
    ▼
Фаза 3: WAL для нового формата
    │
    ▼
Фаза 4: MVCC поверх нового хранилища
    │
    ▼
Фаза 5: B-Tree Index
    │
    ▼
Фаза 6: Statistics + Cost-based Planner
```

Нельзя начинать следующую фазу пока предыдущая не покрыта тестами
на 100% и не прошла интеграционный тест с существующим SQL-движком.

---

## Содержание

1. [Архитектура нового хранилища](#1-архитектура)
2. [Фаза 1 — Page Format и Heap File](#2-page-format)
3. [Фаза 2 — Buffer Pool Manager](#3-buffer-pool)
4. [Фаза 3 — WAL для нового формата](#4-wal)
5. [Фаза 4 — MVCC](#5-mvcc)
6. [Фаза 5 — B-Tree Index](#6-b-tree)
7. [Фаза 6 — Statistics и Cost-based Planner](#7-planner)
8. [Миграция данных из JSON](#8-migration)
9. [Распределение задач](#9-распределение)
10. [Тестирование](#10-тестирование)

---

## 1. Архитектура нового хранилища

### 1.1 Файловая структура

```
data/
└── databases/
    └── mydb/
        ├── pg_catalog/              ← системные таблицы
        │   ├── pg_tables.heap       ← список таблиц
        │   ├── pg_columns.heap      ← список столбцов
        │   ├── pg_indexes.heap      ← список индексов
        │   └── pg_statistics.heap   ← статистика для планировщика
        ├── pg_wal/
        │   └── 000000001.wal        ← WAL-сегменты (16 МБ каждый)
        └── tables/
            ├── users/
            │   ├── 0000.heap        ← файл данных (сегмент 0)
            │   ├── 0001.heap        ← файл данных (сегмент 1, если > 1ГБ)
            │   ├── users_pkey.btree ← B-Tree индекс
            │   └── fsm.map          ← Free Space Map
            └── orders/
                ├── 0000.heap
                └── orders_user_idx.btree
```

### 1.2 Размер страницы

**Фиксированный: 8192 байта (8 КБ).**

Это стандарт PostgreSQL, обоснованный десятилетиями практики:
- Кратен типичным размерам блоков диска (512 байт, 4 КБ)
- Достаточно велик для хранения множества строк
- Достаточно мал для эффективного буферизирования в памяти
- Удобен для выравнивания данных

```go
// internal/storage/page/constants.go

const (
    PageSize      = 8192  // 8 КБ — размер одной страницы
    MaxSegmentSize = 1 << 30 // 1 ГБ — максимальный размер файла-сегмента
    PagesPerSegment = MaxSegmentSize / PageSize // 131 072 страниц на сегмент
)
```

### 1.3 Идентификатор страницы (PageID)

```go
// PageID однозначно идентифицирует страницу в базе данных.
type PageID struct {
    TableID   uint32 // ID таблицы
    SegmentNo uint16 // номер сегментного файла (0000.heap, 0001.heap...)
    PageNo    uint32 // номер страницы внутри сегмента
}

// Смещение страницы в файле
func (p PageID) FileOffset() int64 {
    return int64(p.PageNo) * PageSize
}
```

---

## 2. Фаза 1 — Page Format и Heap File

**Ответственный:** Dev2  
**Зависимости:** нет, реализуется первой  


### 2.1 Структура страницы

```
┌─────────────────────────────────────────────────────────────┐
│                      PAGE HEADER (28 байт)                  │
│  lsn(8) checksum(4) flags(2) lower(2) upper(2) special(2)  │
│  page_type(1) n_items(2) free_space(2) reserved(3)          │
├─────────────────────────────────────────────────────────────┤
│                 ITEM POINTERS (4 байта × n_items)            │
│  [offset(15) | length(14) | flags(3)] × N                   │
│                                                              │
│                     ↓ lower                                 │
│                  (free space)                               │
│                     ↑ upper                                 │
│                                                              │
├─────────────────────────────────────────────────────────────┤
│                      TUPLES (строки)                         │
│  Хранятся с конца страницы к началу                         │
│  Каждый tuple: TupleHeader + данные                         │
├─────────────────────────────────────────────────────────────┤
│                   SPECIAL AREA (для индексов)               │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 Page Header

```go
// internal/storage/page/page.go

// PageHeader — заголовок страницы (28 байт, фиксированный размер).
// Расположен в начале каждой страницы.
type PageHeader struct {
    // LSN — Log Sequence Number последней WAL-записи,
    // которая изменила эту страницу. Используется для recovery.
    LSN uint64

    // Checksum страницы. Вычисляется по всему содержимому кроме этого поля.
    // При чтении сверяется — если не совпадает, страница повреждена.
    Checksum uint32

    // Флаги страницы
    // Бит 0: страница содержит версии строк с MVCC-метками
    // Бит 1: страница имеет свободное место (для FSM)
    Flags uint16

    // Lower — смещение конца зоны Item Pointers (начала свободного пространства)
    Lower uint16

    // Upper — смещение начала зоны Tuples (конца свободного пространства)
    Upper uint16

    // Special — смещение начала Special Area (только для индексных страниц)
    Special uint16

    // PageType — тип страницы
    PageType uint8

    // NItems — количество Item Pointers (не равно количеству живых строк!)
    NItems uint16

    // FreeSpace — кэшированное значение свободного места (Upper - Lower)
    FreeSpace uint16

    // Padding для выравнивания до 28 байт
    Reserved [3]byte
}

const (
    PageTypeHeap    uint8 = 1 // страница данных (heap)
    PageTypeBTreeIn uint8 = 2 // внутренний узел B-Tree
    PageTypeBTreeLf uint8 = 3 // листовой узел B-Tree
    PageTypeFSM     uint8 = 4 // Free Space Map
    PageTypeWAL     uint8 = 5 // WAL-страница
)

const PageHeaderSize = 28
```

### 2.3 Item Pointer (Линия)

```go
// ItemPointer указывает на tuple внутри страницы.
// 4 байта: 15 бит offset + 14 бит length + 3 бита flags.
// Компактное представление: один uint32.
type ItemPointer uint32

func NewItemPointer(offset, length uint16, flags uint8) ItemPointer {
    return ItemPointer(uint32(offset)<<17 | uint32(length)<<3 | uint32(flags))
}

func (ip ItemPointer) Offset() uint16 { return uint16(ip >> 17) }
func (ip ItemPointer) Length() uint16 { return uint16((ip >> 3) & 0x3FFF) }
func (ip ItemPointer) Flags() uint8   { return uint8(ip & 0x7) }

const (
    ItemFlagNormal  uint8 = 0  // обычный tuple
    ItemFlagDead    uint8 = 1  // удалён, место можно переиспользовать
    ItemFlagRedirect uint8 = 2 // перемещён (после HOT update)
)
```

### 2.4 Tuple Header

```go
// TupleHeader — заголовок одной строки (tuple). 23 байта.
// Расположен перед данными строки.
type TupleHeader struct {
    // MVCC-поля (заполняются при INSERT/UPDATE/DELETE)
    XMin uint64  // TransactionID транзакции, создавшей этот tuple
    XMax uint64  // TransactionID транзакции, удалившей этот tuple (0 = живой)

    // CommandID — порядковый номер операции внутри транзакции.
    // Позволяет видеть изменения сделанные в той же транзакции до этой команды.
    CMin uint32

    // Флаги видимости tuple
    // Бит 0: t_xmin committed (транзакция XMin закоммичена)
    // Бит 1: t_xmin aborted  (транзакция XMin откатилась)
    // Бит 2: t_xmax committed
    // Бит 3: t_xmax aborted
    // Бит 4: tuple обновлён (есть новая версия)
    // Бит 5: NULL bitmap существует
    InfoMask uint16

    // NAttributes — количество атрибутов в tuple.
    // Нужно для корректной интерпретации NULL bitmap.
    NAttributes uint16

    // Pad для выравнивания до 23 байт
    Pad uint8
}

const TupleHeaderSize = 23

// NULL bitmap следует сразу за TupleHeader если InfoMask бит 5 установлен.
// Размер: ceil(NAttributes / 8) байт.
// Бит i = 0 означает что атрибут i является NULL.
```

### 2.5 Формат данных внутри Tuple

После TupleHeader (и опционального NULL bitmap) следуют данные атрибутов.
Каждый атрибут хранится в следующем формате:

```go
// AttrData — данные одного атрибута.
// Для типов фиксированной длины (INT, FLOAT, BOOL): хранятся напрямую.
// Для типов переменной длины (TEXT, VARCHAR, VECTOR): 4-байтный длина + данные.

// Фиксированная длина:
//   INT:   8 байт (int64, little-endian)
//   FLOAT: 8 байт (float64, IEEE 754)
//   BOOL:  1 байт (0x00 или 0x01)

// Переменная длина (varlena):
//   [4 байта: длина данных][N байт: данные]
//   Если длина > 8160 байт (не помещается на страницу) → TOAST
```

### 2.6 TOAST (для больших значений)

```
Если значение > 2000 байт:
    1. Данные сжимаются (lz4 или zstd)
    2. Если после сжатия > 2000 байт — выносятся в toast-файл
    3. В основном tuple хранится только "тостер-указатель" (18 байт)

TOAST Pointer (18 байт):
    [type: 1][toast_table_id: 4][toast_chunk_id: 4][value_size: 8][chunk_count: 1]

TOAST-файл для таблицы users:
    users/toast.heap  ← отдельный heap-файл для больших значений
```

### 2.7 Page API

```go
// internal/storage/page/page.go

// Page — 8КБ буфер с методами для работы со страницей.
type Page [PageSize]byte

// Init инициализирует пустую страницу.
func (p *Page) Init(pageType uint8) {
    header := p.Header()
    header.Checksum  = 0
    header.Flags     = 0
    header.Lower     = uint16(PageHeaderSize)
    header.Upper     = PageSize
    header.Special   = PageSize
    header.PageType  = pageType
    header.NItems    = 0
    header.FreeSpace = PageSize - PageHeaderSize
    p.writeHeader(header)
}

// Header читает PageHeader из начала страницы.
func (p *Page) Header() PageHeader {
    var h PageHeader
    binary.LittleEndian.Unpack(p[:PageHeaderSize], &h)
    return h
}

// FreeSpace возвращает количество байт свободного места.
func (p *Page) FreeSpace() uint16 {
    h := p.Header()
    if h.Upper <= h.Lower { return 0 }
    return h.Upper - h.Lower
}

// InsertTuple вставляет tuple на страницу.
// Возвращает SlotNumber (индекс в Item Pointer array) или ошибку если нет места.
func (p *Page) InsertTuple(data []byte) (uint16, error) {
    needed := uint16(len(data)) + 4 // 4 байта на ItemPointer

    if p.FreeSpace() < needed {
        return 0, ErrPageFull
    }

    h := p.Header()

    // Размещаем данные с конца страницы
    tupleOffset := h.Upper - uint16(len(data))
    copy(p[tupleOffset:], data)

    // Добавляем Item Pointer
    ip := NewItemPointer(tupleOffset, uint16(len(data)), ItemFlagNormal)
    ipOffset := h.Lower
    binary.LittleEndian.PutUint32(p[ipOffset:], uint32(ip))

    // Обновляем заголовок
    h.Upper     = tupleOffset
    h.Lower    += 4
    h.NItems   += 1
    h.FreeSpace = h.Upper - h.Lower
    p.writeHeader(h)

    return h.NItems - 1, nil
}

// GetTuple читает tuple по SlotNumber.
// Возвращает nil если слот помечен как dead.
func (p *Page) GetTuple(slot uint16) []byte {
    h := p.Header()
    if slot >= h.NItems { return nil }

    ipOffset := uint16(PageHeaderSize) + slot*4
    ip := ItemPointer(binary.LittleEndian.Uint32(p[ipOffset:]))

    if ip.Flags() == ItemFlagDead { return nil }

    return p[ip.Offset() : ip.Offset()+ip.Length()]
}

// MarkDead помечает слот как удалённый.
func (p *Page) MarkDead(slot uint16) {
    h := p.Header()
    if slot >= h.NItems { return }

    ipOffset := uint16(PageHeaderSize) + slot*4
    ip := ItemPointer(binary.LittleEndian.Uint32(p[ipOffset:]))
    newIP := NewItemPointer(ip.Offset(), ip.Length(), ItemFlagDead)
    binary.LittleEndian.PutUint32(p[ipOffset:], uint32(newIP))
}

// Compact выполняет дефрагментацию страницы:
// сдвигает все живые tuples к концу, освобождая фрагментированное место.
func (p *Page) Compact() {
    h := p.Header()
    tempPage := &Page{}
    tempPage.Init(h.PageType)

    for slot := uint16(0); slot < h.NItems; slot++ {
        data := p.GetTuple(slot)
        if data == nil { continue } // пропускаем dead tuples
        tempPage.InsertTuple(data)
    }

    copy(p[:], tempPage[:])
}

// ComputeChecksum вычисляет CRC32 страницы (кроме поля Checksum).
func (p *Page) ComputeChecksum() uint32 {
    // Временно обнуляем поле checksum
    saved := binary.LittleEndian.Uint32(p[8:12])
    binary.LittleEndian.PutUint32(p[8:12], 0)
    crc := crc32.ChecksumIEEE(p[:])
    binary.LittleEndian.PutUint32(p[8:12], saved)
    return crc
}

var ErrPageFull = errors.New("page is full")
```

### 2.8 Heap File Manager

```go
// internal/storage/heap/heapfile.go

// HeapFile управляет набором сегментных файлов одной таблицы.
// Предоставляет операции на уровне страниц.
type HeapFile struct {
    dir      string        // директория таблицы
    segments []*os.File    // открытые файловые дескрипторы сегментов
    mu       sync.RWMutex
}

func OpenHeapFile(dir string) (*HeapFile, error)
func CreateHeapFile(dir string) (*HeapFile, error)

// ReadPage читает страницу с диска в переданный буфер.
// Проверяет checksum. Возвращает ErrChecksumMismatch при повреждении.
func (hf *HeapFile) ReadPage(pid page.PageID, buf *page.Page) error {
    seg, err := hf.getSegment(pid.SegmentNo)
    if err != nil { return err }

    offset := int64(pid.PageNo) * page.PageSize
    if _, err := seg.ReadAt(buf[:], offset); err != nil {
        return fmt.Errorf("readpage %v: %w", pid, err)
    }

    // Верификация checksum
    stored  := binary.LittleEndian.Uint32(buf[8:12])
    computed := buf.ComputeChecksum()
    if stored != computed {
        return fmt.Errorf("page %v: checksum mismatch (stored=%d, computed=%d)",
            pid, stored, computed)
    }

    return nil
}

// WritePage записывает страницу на диск.
// Перед записью вычисляет и устанавливает checksum.
// НЕ вызывает fsync — это ответственность WAL.
func (hf *HeapFile) WritePage(pid page.PageID, buf *page.Page) error {
    // Установить checksum перед записью
    checksum := buf.ComputeChecksum()
    binary.LittleEndian.PutUint32(buf[8:12], checksum)

    seg, err := hf.getOrCreateSegment(pid.SegmentNo)
    if err != nil { return err }

    offset := int64(pid.PageNo) * page.PageSize
    _, err = seg.WriteAt(buf[:], offset)
    return err
}

// AllocatePage выделяет новую страницу (добавляет в конец файла).
func (hf *HeapFile) AllocatePage(pageType uint8) (page.PageID, *page.Page, error) {
    hf.mu.Lock()
    defer hf.mu.Unlock()

    // Найти последний сегмент
    segNo := uint16(len(hf.segments) - 1)
    seg   := hf.segments[segNo]

    info, _ := seg.Stat()
    pageNo  := uint32(info.Size() / page.PageSize)

    // Если сегмент полон — создать новый
    if pageNo >= page.PagesPerSegment {
        segNo++
        seg, _ = hf.createSegment(segNo)
        pageNo = 0
    }

    pid := page.PageID{SegmentNo: segNo, PageNo: pageNo}
    buf := &page.Page{}
    buf.Init(pageType)

    if err := hf.WritePage(pid, buf); err != nil {
        return page.PageID{}, nil, err
    }

    return pid, buf, nil
}
```

### 2.9 Free Space Map (FSM)

FSM позволяет быстро найти страницу с достаточным количеством свободного
места для вставки нового tuple — без перебора всех страниц.

```go
// internal/storage/fsm/fsm.go

// FSM хранит информацию о свободном месте в каждой странице.
// Структура: бинарное дерево где каждый узел хранит максимальное
// свободное место среди своих потомков.
// Позволяет найти страницу с ≥ N байт свободного места за O(log n).
type FSM struct {
    mu       sync.RWMutex
    path     string
    // Дерево хранится как массив (как heap-дерево).
    // nodes[1] = корень, nodes[i] = узел,
    // nodes[2i] = левый ребёнок, nodes[2i+1] = правый ребёнок.
    // Листья = значения для реальных страниц.
    // Значение: количество свободных байт / 32 (умещается в uint8)
    nodes    []uint8
    nPages   uint32
}

// Search находит PageID с не менее чем minFree свободными байтами.
// Возвращает InvalidPageID если подходящей страницы нет.
func (fsm *FSM) Search(minFree uint16) (uint32, bool) {
    fsm.mu.RLock()
    defer fsm.mu.RUnlock()

    minCategory := uint8(minFree / 32)
    if minCategory == 0 && minFree > 0 { minCategory = 1 }

    // Обход дерева сверху вниз
    node := uint32(1)
    for node <= uint32(len(fsm.nodes)/2) { // пока не лист
        left, right := 2*node, 2*node+1
        if left < uint32(len(fsm.nodes)) && fsm.nodes[left] >= minCategory {
            node = left
        } else if right < uint32(len(fsm.nodes)) && fsm.nodes[right] >= minCategory {
            node = right
        } else {
            return 0, false
        }
    }

    pageNo := node - uint32(len(fsm.nodes)/2) - 1
    return pageNo, true
}

// Update обновляет значение для конкретной страницы и пересчитывает дерево вверх.
func (fsm *FSM) Update(pageNo uint32, freeBytes uint16) {
    fsm.mu.Lock()
    defer fsm.mu.Unlock()

    category := uint8(freeBytes / 32)
    leafIdx  := uint32(len(fsm.nodes)/2) + pageNo

    if int(leafIdx) >= len(fsm.nodes) { return }
    fsm.nodes[leafIdx] = category

    // Пересчитать родителей снизу вверх
    node := leafIdx / 2
    for node >= 1 {
        left, right := 2*node, 2*node+1
        maxChild := fsm.nodes[left]
        if right < uint32(len(fsm.nodes)) && fsm.nodes[right] > maxChild {
            maxChild = fsm.nodes[right]
        }
        fsm.nodes[node] = maxChild
        node /= 2
    }
}
```

---

## 3. Фаза 2 — Buffer Pool Manager

**Ответственный:** Dev2  
**Зависимости:** Фаза 1 полностью готова  


### 3.1 Назначение

Buffer Pool — пул страниц в оперативной памяти. Вместо того чтобы читать
страницу с диска при каждом запросе, сервер держит горячие страницы в памяти.

Без Buffer Pool каждый SELECT читает файл с диска. С Buffer Pool повторный
запрос к той же странице происходит без I/O — из памяти.

```go
// internal/storage/bufpool/bufpool.go

// BufferPool управляет фиксированным количеством страниц в памяти.
type BufferPool struct {
    mu sync.Mutex

    // Буферы: массив страниц в памяти. Размер = NBuffers.
    buffers    []BufferDescriptor
    pages      []page.Page     // сами данные страниц

    // Таблица страниц: PageID → индекс буфера (для O(1) поиска)
    pageTable  map[page.PageID]int

    // Clock hand для алгоритма замены Clock
    clockHand  int

    // Статистика
    stats BufferPoolStats
}

// BufferDescriptor — метаданные одного буфера.
type BufferDescriptor struct {
    PageID    page.PageID
    PinCount  int32         // количество активных "держателей" страницы
    IsDirty   bool          // страница изменена и должна быть записана на диск
    RefBit    bool          // бит для алгоритма Clock (установлен при обращении)
    Valid     bool          // буфер содержит валидную страницу
    rwmu      sync.RWMutex  // блокировка на уровне буфера
}

type BufferPoolStats struct {
    Hits    atomic.Int64 // количество cache hit (страница найдена в буфере)
    Misses  atomic.Int64 // количество cache miss (читали с диска)
    Evictions atomic.Int64 // количество вытеснений
}

const DefaultNBuffers = 1024 // 1024 × 8КБ = 8 МБ по умолчанию
```

### 3.2 Pin / Unpin

Страница "пинается" перед использованием и "анпинится" после. Пока страница
пинована, алгоритм замены не может её вытеснить.

```go
// FetchPage возвращает буфер для указанной страницы.
// Если страница уже в буфере — cache hit.
// Если нет — читает с диска (cache miss), возможно вытесняя другую страницу.
// Возвращает pinned буфер — вызывающий ОБЯЗАН вызвать Unpin после использования.
func (bp *BufferPool) FetchPage(pid page.PageID, hf *heap.HeapFile) (*page.Page, int, error) {
    bp.mu.Lock()

    // Cache hit: страница уже в буфере
    if idx, ok := bp.pageTable[pid]; ok {
        desc := &bp.buffers[idx]
        atomic.AddInt32(&desc.PinCount, 1)
        desc.RefBit = true
        bp.mu.Unlock()
        bp.stats.Hits.Add(1)

        // Получить read lock на буфер
        desc.rwmu.RLock()
        return &bp.pages[idx], idx, nil
    }

    // Cache miss: нужно загрузить с диска
    bp.stats.Misses.Add(1)

    // Найти свободный или вытесняемый буфер
    victimIdx, err := bp.findVictim()
    if err != nil {
        bp.mu.Unlock()
        return nil, -1, fmt.Errorf("buffer pool full: %w", err)
    }

    victim := &bp.buffers[victimIdx]

    // Если вытесняемый буфер dirty — сбросить на диск
    if victim.Valid && victim.IsDirty {
        bp.mu.Unlock()
        if err := bp.flushBuffer(victimIdx, hf); err != nil {
            return nil, -1, err
        }
        bp.mu.Lock()
        bp.stats.Evictions.Add(1)
    }

    // Убрать старый PID из pageTable
    if victim.Valid {
        delete(bp.pageTable, victim.PageID)
    }

    // Загрузить новую страницу
    victim.PageID   = pid
    victim.PinCount = 1
    victim.IsDirty  = false
    victim.RefBit   = true
    victim.Valid     = true
    bp.pageTable[pid] = victimIdx

    bp.mu.Unlock()

    // Читаем с диска (без глобального мьютекса)
    if err := hf.ReadPage(pid, &bp.pages[victimIdx]); err != nil {
        bp.mu.Lock()
        victim.Valid = false
        delete(bp.pageTable, pid)
        bp.mu.Unlock()
        return nil, -1, err
    }

    victim.rwmu.RLock()
    return &bp.pages[victimIdx], victimIdx, nil
}

// Unpin освобождает pin на буфере.
// isDirty = true если страница была изменена.
func (bp *BufferPool) Unpin(idx int, isDirty bool) {
    desc := &bp.buffers[idx]

    // Сначала освобождаем rwmutex
    if isDirty {
        desc.rwmu.RUnlock() // или RUnlock для читателей
    } else {
        desc.rwmu.RUnlock()
    }

    bp.mu.Lock()
    defer bp.mu.Unlock()

    atomic.AddInt32(&desc.PinCount, -1)
    if isDirty {
        desc.IsDirty = true
    }
}
```

### 3.3 Алгоритм замены: Clock

Clock — упрощённая аппроксимация LRU без накладных расходов на поддержание
упорядоченного списка. PostgreSQL использует его же (называя "clock sweep").

```go
// findVictim находит буфер для вытеснения по алгоритму Clock.
// ВАЖНО: вызывается под bp.mu.Lock()
func (bp *BufferPool) findVictim() (int, error) {
    maxAttempts := 2 * len(bp.buffers) // два полных прохода

    for i := 0; i < maxAttempts; i++ {
        idx := bp.clockHand
        bp.clockHand = (bp.clockHand + 1) % len(bp.buffers)

        desc := &bp.buffers[idx]

        // Пропустить pinned буферы
        if atomic.LoadInt32(&desc.PinCount) > 0 {
            continue
        }

        // Если RefBit установлен — дать второй шанс и сбросить бит
        if desc.RefBit {
            desc.RefBit = false
            continue
        }

        // Нашли жертву
        return idx, nil
    }

    return -1, errors.New("all buffers are pinned")
}
```

### 3.4 FlushAll и Checkpoint

```go
// FlushAll сбрасывает все dirty страницы на диск.
// Вызывается при checkpoint и graceful shutdown.
func (bp *BufferPool) FlushAll(hf *heap.HeapFile) error {
    bp.mu.Lock()
    dirtyIdxs := make([]int, 0)
    for i := range bp.buffers {
        if bp.buffers[i].Valid && bp.buffers[i].IsDirty {
            dirtyIdxs = append(dirtyIdxs, i)
        }
    }
    bp.mu.Unlock()

    // Сбрасываем параллельно
    var wg sync.WaitGroup
    var firstErr error
    var errMu sync.Mutex

    for _, idx := range dirtyIdxs {
        wg.Add(1)
        go func(i int) {
            defer wg.Done()
            if err := bp.flushBuffer(i, hf); err != nil {
                errMu.Lock()
                firstErr = err
                errMu.Unlock()
            }
        }(idx)
    }

    wg.Wait()
    return firstErr
}
```

---

## 4. Фаза 3 — WAL для нового формата

**Ответственный:** Dev2 + Dev3  
**Зависимости:** Фазы 1, 2 готовы  


### 4.1 Принципиальное изменение

В старом WAL payload был JSON. В новом — бинарный, привязанный к страницам.
Это называется физическое логирование (physical logging): WAL описывает
конкретные изменения байт в конкретных страницах.

```go
// internal/wal/record.go

// WALRecord — одна запись в WAL.
type WALRecord struct {
    // LSN (Log Sequence Number) — уникальный монотонный идентификатор записи.
    // Используется для установления порядка операций.
    LSN uint64

    // XID — Transaction ID, которая породила эту запись.
    XID uint64

    // RecordType — тип WAL-записи.
    RecordType uint8

    // PrevLSN — LSN предыдущей записи той же транзакции.
    // Образует linked list транзакции в WAL — нужно для undo при rollback.
    PrevLSN uint64

    // Длина тела записи.
    BodyLength uint32

    // Body — тело записи (зависит от RecordType).
    Body []byte

    // CRC32 всей записи (кроме этого поля).
    CRC uint32
}

const (
    WALRecordInsert      uint8 = 0x01 // INSERT tuple
    WALRecordDelete      uint8 = 0x02 // DELETE tuple (mark dead)
    WALRecordUpdate      uint8 = 0x03 // UPDATE = delete old + insert new
    WALRecordCommit      uint8 = 0x10 // COMMIT транзакции
    WALRecordAbort       uint8 = 0x11 // ROLLBACK транзакции
    WALRecordCheckpoint  uint8 = 0x20 // Checkpoint
    WALRecordCreateTable uint8 = 0x30
    WALRecordDropTable   uint8 = 0x31
    WALRecordPageInit    uint8 = 0x40 // инициализация новой страницы
)
```

### 4.2 WAL Insert Record Body

```go
// WALInsertBody — тело записи типа WALRecordInsert.
type WALInsertBody struct {
    TableID  uint32         // ID таблицы
    PageID   page.PageID    // страница куда вставлен tuple
    SlotNo   uint16         // слот на странице
    TupleData []byte        // полные данные tuple (TupleHeader + атрибуты)
}

// WALDeleteBody — тело записи WALRecordDelete.
type WALDeleteBody struct {
    TableID uint32
    PageID  page.PageID
    SlotNo  uint16
    XMax    uint64     // XID транзакции удаляющей tuple
}

// WALCommitBody — тело записи WALRecordCommit.
type WALCommitBody struct {
    CommitTimestamp int64  // Unix nanoseconds
}
```

### 4.3 LSN и порядок записи на диск

```go
// internal/wal/writer.go

// WALWriter управляет записью в WAL.
// Критическое свойство: WAL запись ДОЛЖНА быть на диске до того,
// как соответствующая страница данных окажется на диске.
// Это называется "WAL protocol" или "write-ahead logging rule".
type WALWriter struct {
    mu         sync.Mutex
    file       *os.File
    currentLSN atomic.Uint64

    // Буфер для group commit: накапливаем несколько записей
    // и делаем один fsync вместо одного на каждую запись.
    writeBuffer bytes.Buffer
    flushTimer  *time.Timer
    flushCond   *sync.Cond
}

// Append добавляет запись в WAL.
// Возвращает LSN присвоенный этой записи.
// При synchronous_commit=true: блокирует до fsync.
// При synchronous_commit=false: возвращает немедленно (риск потери при краше).
func (w *WALWriter) Append(xid uint64, recType uint8, body []byte) (uint64, error) {
    w.mu.Lock()
    defer w.mu.Unlock()

    lsn := w.currentLSN.Add(1)

    rec := WALRecord{
        LSN:        lsn,
        XID:        xid,
        RecordType: recType,
        BodyLength: uint32(len(body)),
        Body:       body,
    }
    // CRC вычислить и добавить

    // Сериализовать в бинарный формат
    serialized := rec.Serialize()

    // Запись в буфер (group commit)
    w.writeBuffer.Write(serialized)

    // fsync при каждой commit-записи (synchronous_commit=on)
    if recType == WALRecordCommit {
        if _, err := w.file.Write(w.writeBuffer.Bytes()); err != nil {
            return 0, err
        }
        if err := w.file.Sync(); err != nil {
            return 0, err
        }
        w.writeBuffer.Reset()
    }

    return lsn, nil
}
```

### 4.4 Recovery

```go
// internal/wal/recovery.go

// Recover воспроизводит WAL начиная с последнего checkpoint.
// Вызывается при старте сервера, до принятия подключений.
func Recover(walDir string, pool *bufpool.BufferPool, catalog *Catalog) error {
    // Фаза 1: Analysis
    // Читаем WAL с последнего checkpoint.
    // Определяем:
    //   - какие транзакции закоммичены
    //   - какие транзакции были в процессе (нужен undo)
    //   - с какой LSN начинать redo

    committed, inProgress, redoLSN, err := analyzeWAL(walDir)
    if err != nil { return err }

    slog.Info("WAL analysis complete",
        "committed", len(committed),
        "in_progress", len(inProgress),
        "redo_from_lsn", redoLSN)

    // Фаза 2: Redo
    // Воспроизводим все записи начиная с redoLSN.
    // Применяем записи и для committed, и для uncommitted транзакций.
    if err := redoPhase(walDir, pool, catalog, redoLSN); err != nil {
        return err
    }

    slog.Info("WAL redo complete")

    // Фаза 3: Undo
    // Откатываем транзакции которые были в процессе (не закоммичены).
    // Для каждой uncommitted транзакции идём по PrevLSN назад
    // и отменяем все её изменения.
    if err := undoPhase(walDir, pool, catalog, inProgress); err != nil {
        return err
    }

    slog.Info("WAL undo complete", "rolled_back", len(inProgress))

    // Записываем новый checkpoint
    return writeCheckpoint(walDir, catalog)
}
```

---

## 5. Фаза 4 — MVCC

**Ответственный:** Dev3  
**Зависимости:** Фазы 1–3 готовы  

### 5.1 Transaction ID Manager

```go
// internal/txn/manager.go

// TransactionID — монотонно возрастающий идентификатор транзакции.
// uint64 позволяет ~1.8×10¹⁹ транзакций — практически неисчерпаем.
type TransactionID uint64

const (
    InvalidXID  TransactionID = 0
    BootstrapXID TransactionID = 1 // для системных операций при инициализации
    FirstNormalXID TransactionID = 2
)

// TxManager управляет жизненным циклом всех транзакций.
type TxManager struct {
    mu         sync.Mutex
    nextXID    atomic.Uint64

    // активные транзакции: xid → *Transaction
    active     map[TransactionID]*Transaction

    // CommitLog — быстрый массив статусов транзакций.
    // commitLog[xid % CommitLogSize] = XID_STATUS
    // Аналог pg_clog в PostgreSQL.
    commitLog  []uint8
    commitLogMu sync.RWMutex
}

type XIDStatus uint8
const (
    XIDStatusInProgress XIDStatus = 0
    XIDStatusCommitted  XIDStatus = 1
    XIDStatusAborted    XIDStatus = 2
)

// Begin начинает новую транзакцию.
// Возвращает XID и снимок (Snapshot) текущего состояния.
func (m *TxManager) Begin() *Transaction {
    m.mu.Lock()
    defer m.mu.Unlock()

    xid := TransactionID(m.nextXID.Add(1))

    tx := &Transaction{
        XID:      xid,
        Status:   XIDStatusInProgress,
        Snapshot: m.takeSnapshot(),
        StartedAt: time.Now(),
    }

    m.active[xid] = tx
    return tx
}

// Snapshot — снимок состояния транзакций в момент начала нашей транзакции.
// Используется для определения видимости tuple'ов.
type Snapshot struct {
    // XMin — минимальный XID активной транзакции в момент snapshot.
    // Все транзакции с XID < XMin гарантированно завершены.
    XMin TransactionID

    // XMax — следующий XID который будет назначен.
    // Все транзакции с XID >= XMax начались после нашего snapshot.
    XMax TransactionID

    // ActiveXIDs — список XID транзакций, активных в момент snapshot.
    // Транзакции в этом списке невидимы нам, даже если их XID < XMax.
    ActiveXIDs []TransactionID
}

func (m *TxManager) takeSnapshot() Snapshot {
    activeXIDs := make([]TransactionID, 0, len(m.active))
    minXID := TransactionID(m.nextXID.Load())

    for xid := range m.active {
        activeXIDs = append(activeXIDs, xid)
        if xid < minXID { minXID = xid }
    }

    return Snapshot{
        XMin:       minXID,
        XMax:       TransactionID(m.nextXID.Load()),
        ActiveXIDs: activeXIDs,
    }
}
```

### 5.2 Правила видимости tuple

```go
// internal/txn/visibility.go

// IsTupleVisible определяет, виден ли данный tuple нашей транзакции.
// Это центральная функция MVCC.
//
// Правила (упрощённо):
//  1. XMin должен быть committed ДО нашего snapshot
//  2. XMax должен быть 0, или committed ПОСЛЕ нашего snapshot, или in-progress
func IsTupleVisible(header *page.TupleHeader, snap Snapshot, mgr *TxManager) bool {

    // Шаг 1: проверяем XMin (кто создал этот tuple)
    xminStatus := mgr.GetStatus(header.XMin)

    if xminStatus == XIDStatusAborted {
        return false // создатель откатился → tuple не существует
    }

    if xminStatus == XIDStatusInProgress {
        // Tuple создан нашей собственной транзакцией — виден
        // (это позволяет читать то, что мы только что вставили)
        if header.XMin == snap.OurXID {
            // виден если CMin <= наш CID
        } else {
            return false // создан другой активной транзакцией — не виден
        }
    }

    // XMin committed — теперь проверяем был ли он committed до нашего snapshot
    if header.XMin >= snap.XMax {
        return false // XMin начался после нашего snapshot
    }
    for _, activeXID := range snap.ActiveXIDs {
        if header.XMin == activeXID {
            return false // XMin был активен в момент нашего snapshot
        }
    }

    // Шаг 2: проверяем XMax (кто удалил этот tuple)
    if header.XMax == 0 {
        return true // никто не удалял → виден
    }

    xmaxStatus := mgr.GetStatus(header.XMax)

    if xmaxStatus == XIDStatusAborted {
        return true // удалявший откатился → tuple жив
    }

    if xmaxStatus == XIDStatusInProgress {
        if header.XMax == snap.OurXID {
            return false // мы сами удалили — не виден
        }
        return true // другая активная транзакция удаляет — мы ещё видим
    }

    // XMax committed — проверяем был ли он committed до нашего snapshot
    if header.XMax >= snap.XMax {
        return true // XMax начался после нашего snapshot — мы ещё видим
    }
    for _, activeXID := range snap.ActiveXIDs {
        if header.XMax == activeXID {
            return true // XMax был активен в нашем snapshot — мы видим
        }
    }

    return false // XMax committed до нашего snapshot → tuple удалён
}
```

### 5.3 INSERT с MVCC

```go
// internal/storage/heapccess.go

// InsertTuple вставляет tuple в heap.
// Выбирает страницу через FSM, заполняет TupleHeader с XID транзакции.
func InsertTuple(
    tx *txn.Transaction,
    tableID uint32,
    attrs []storage.Value,
    schema *catalog.TableSchema,
    pool *bufpool.BufferPool,
    hf *heap.HeapFile,
    fsm *fsm.FSM,
    wal *wal.WALWriter,
) error {
    // Сериализовать атрибуты в бинарное представление
    tupleData := serializeTuple(attrs, schema)

    // Заголовок tuple
    header := &page.TupleHeader{
        XMin:        uint64(tx.XID),
        XMax:        0,                    // живой
        CMin:        tx.CommandID,
        InfoMask:    0,
        NAttributes: uint16(len(attrs)),
    }
    tupleBytes := serializeTupleWithHeader(header, tupleData)

    // Найти страницу с достаточным местом
    needed := uint16(len(tupleBytes))
    pageNo, found := fsm.Search(needed)

    var pid page.PageID
    if !found {
        // Выделить новую страницу
        newPID, _, err := hf.AllocatePage(page.PageTypeHeap)
        if err != nil { return err }
        pid = newPID
    } else {
        pid = page.PageID{PageNo: pageNo}
    }

    // Получить страницу из буферного пула
    pg, bufIdx, err := pool.FetchPage(pid, hf)
    if err != nil { return err }

    // Вставить tuple
    slotNo, err := pg.InsertTuple(tupleBytes)
    if err != nil {
        pool.Unpin(bufIdx, false)
        return err
    }

    // Записать в WAL ПЕРЕД тем как отметить страницу dirty
    lsn, err := wal.Append(uint64(tx.XID), wal.WALRecordInsert,
        serializeInsertBody(tableID, pid, slotNo, tupleBytes))
    if err != nil {
        pool.Unpin(bufIdx, false)
        return err
    }

    // Обновить LSN страницы
    binary.LittleEndian.PutUint64(pg[:8], lsn)

    // Обновить FSM
    fsm.Update(pid.PageNo, pg.FreeSpace())

    pool.Unpin(bufIdx, true) // true = dirty

    return nil
}
```

### 5.4 Vacuum

MVCC накапливает мёртвые tuple'ы. Vacuum физически их удаляет.

```go
// internal/vacuum/vacuum.go

// VacuumTable выполняет VACUUM для одной таблицы.
// 1. Проходит все страницы таблицы
// 2. Для каждой страницы: удаляет tuple'ы невидимые ВСЕМ активным транзакциям
// 3. Compact страницы с большим количеством мёртвых tuple'ов
// 4. Обновляет FSM
func VacuumTable(
    tableID uint32,
    hf *heap.HeapFile,
    pool *bufpool.BufferPool,
    txMgr *txn.TxManager,
    fsm *fsm.FSM,
    wal *wal.WALWriter,
) (*VacuumStats, error) {

    stats := &VacuumStats{TableID: tableID}

    // Получить "горизонт видимости" — наименьший XMin среди всех активных транзакций.
    // Tuple'ы с XMax < horizon невидимы ВСЕМ, их можно удалять.
    horizon := txMgr.OldestActiveXID()

    pageCount := hf.PageCount()

    for pageNo := uint32(0); pageNo < pageCount; pageNo++ {
        pid := page.PageID{PageNo: pageNo}
        pg, bufIdx, err := pool.FetchPage(pid, hf)
        if err != nil { continue }

        h := pg.Header()
        pageModified := false

        for slot := uint16(0); slot < h.NItems; slot++ {
            tupleBytes := pg.GetTuple(slot)
            if tupleBytes == nil { continue } // уже dead

            header := parseTupleHeader(tupleBytes)

            // Проверить: мёртв ли tuple для всех транзакций?
            if header.XMax != 0 {
                xmaxStatus := txMgr.GetStatus(txn.TransactionID(header.XMax))
                if xmaxStatus == txn.XIDStatusCommitted &&
                   txn.TransactionID(header.XMax) < horizon {
                    // Этот tuple мёртв для всех — можно удалить
                    pg.MarkDead(slot)
                    stats.TuplesRemoved++
                    pageModified = true
                }
            }
        }

        if pageModified {
            // Запись в WAL
            wal.Append(0, wal.WALRecordVacuum, serializeVacuumBody(tableID, pid))
            pg.Compact() // дефрагментировать страницу
            fsm.Update(pageNo, pg.FreeSpace())
        }

        pool.Unpin(bufIdx, pageModified)
    }

    return stats, nil
}
```

---

## 6. Фаза 5 — B-Tree Index

**Ответственный:** Dev2 + Dev1 (синтаксис индексов)  
**Зависимости:** Фазы 1–4 готовы  

### 6.1 Зачем B-Tree вместо Hash

| Операция | Hash Index | B-Tree |
|---|---|---|
| Точный поиск `=` | O(1) avg | O(log n) |
| Range scan `BETWEEN` | ❌ Невозможно | O(log n + k) |
| `ORDER BY col` с индексом | ❌ | ✅ Без сортировки |
| `MIN()` / `MAX()` с индексом | ❌ | O(log n) |
| Prefix scan `LIKE 'abc%'` | ❌ | ✅ |
| Многоколоночный индекс | ❌ | ✅ |

### 6.2 Структура B-Tree

```
                    [Internal Node]
                    ┌────────────┐
                    │ 50 │ 100  │
                    └──┬──┬──┬──┘
           ┌───────────┘  │  └───────────┐
           ▼              ▼              ▼
    [Leaf]           [Leaf]           [Leaf]
  ┌───────────┐    ┌───────────┐    ┌───────────┐
  │10│20│30│40│◄──►│50│60│70│80│◄──►│100│110│120│
  └───────────┘    └───────────┘    └───────────┘
        │                │                │
      (PageID,SlotNo)  (PageID,SlotNo) (PageID,SlotNo)
```

Листовые узлы связаны в двусвязный список — это позволяет range scan
без возврата к корню.

### 6.3 Форматы страниц B-Tree

```go
// internal/index/btree/page.go

// BTreePageHeader — специальная область (Special Area) для B-Tree страниц.
// Размещается в конце страницы (8192 - sizeof(BTreePageHeader) ...).
type BTreePageHeader struct {
    LeftSibling  uint32 // PageNo левого соседа (для листьев) или InvalidPageNo
    RightSibling uint32 // PageNo правого соседа
    Level        uint16 // 0 = лист, 1 = родитель листьев, ...
    NKeys        uint16 // количество ключей на странице
    IsRoot       bool
    _            [3]byte
}

// BTreeKey — ключ в B-Tree (хранится в Item Pointer area страницы).
type BTreeKey struct {
    // Значение ключа в бинарном формате (little-endian для чисел,
    // сырые байты для строк с prefix-сжатием).
    KeyData []byte

    // Для листовых узлов: указатель на heap tuple.
    HeapPointer struct {
        PageNo uint32
        SlotNo uint16
    }

    // Для внутренних узлов: указатель на дочернюю страницу.
    ChildPageNo uint32
}
```

### 6.4 Алгоритм поиска

```go
// internal/index/btree/btree.go

// Search находит все TuplePointer'ы где ключ совпадает с искомым значением.
func (bt *BTree) Search(key []byte, pool *bufpool.BufferPool) ([]TuplePointer, error) {
    // Начинаем с корневой страницы
    return bt.searchFromPage(bt.rootPageNo, key, pool)
}

func (bt *BTree) searchFromPage(
    pageNo uint32, key []byte, pool *bufpool.BufferPool,
) ([]TuplePointer, error) {

    pid := page.PageID{PageNo: pageNo}
    pg, bufIdx, err := pool.FetchPage(pid, bt.indexFile)
    if err != nil { return nil, err }
    defer pool.Unpin(bufIdx, false)

    btHeader := readBTreeHeader(pg)

    if btHeader.Level == 0 {
        // Листовой узел: находим все ключи равные искомому
        return bt.searchLeaf(pg, key), nil
    }

    // Внутренний узел: находим дочернюю страницу для спуска
    childPageNo := bt.findChild(pg, key)
    return bt.searchFromPage(childPageNo, key, pool)
}

// RangeScan возвращает все TuplePointer'ы для ключей в диапазоне [lo, hi].
func (bt *BTree) RangeScan(lo, hi []byte, pool *bufpool.BufferPool) ([]TuplePointer, error) {
    // 1. Найти листовую страницу для lo
    leafPageNo := bt.findLeafPage(lo, pool)

    var results []TuplePointer

    // 2. Пройти по цепочке листовых страниц (используем RightSibling)
    for leafPageNo != InvalidPageNo {
        pid := page.PageID{PageNo: leafPageNo}
        pg, bufIdx, err := pool.FetchPage(pid, bt.indexFile)
        if err != nil { return nil, err }

        btHeader := readBTreeHeader(pg)
        done := false

        // Перебрать ключи на этой странице
        for _, entry := range readLeafEntries(pg) {
            cmp := bt.compareKeys(entry.Key, hi)
            if cmp > 0 {
                done = true
                break
            }
            if bt.compareKeys(entry.Key, lo) >= 0 {
                results = append(results, entry.Pointer)
            }
        }

        nextPageNo := btHeader.RightSibling
        pool.Unpin(bufIdx, false)

        if done { break }
        leafPageNo = nextPageNo
    }

    return results, nil
}
```

### 6.5 INSERT в B-Tree (со split)

```go
// Insert добавляет ключ в B-Tree.
// Если страница переполнена — выполняет page split.
func (bt *BTree) Insert(key []byte, ptr TuplePointer, pool *bufpool.BufferPool, wal *wal.WALWriter) error {
    return bt.insertFromRoot(bt.rootPageNo, key, ptr, pool, wal)
}

// insertResult — результат попытки вставки в узел.
type insertResult struct {
    splitKey  []byte   // если != nil → произошёл split, это медиана
    newPageNo uint32   // PageNo нового правого узла после split
}

func (bt *BTree) insertIntoNode(
    pageNo uint32, key []byte, ptr TuplePointer,
    pool *bufpool.BufferPool, wal *wal.WALWriter,
) (*insertResult, error) {

    pid := page.PageID{PageNo: pageNo}
    pg, bufIdx, err := pool.FetchPage(pid, bt.indexFile)
    if err != nil { return nil, err }

    btHeader := readBTreeHeader(pg)

    if btHeader.Level == 0 {
        // Листовой узел: вставляем ключ в отсортированной позиции
        entryBytes := serializeLeafEntry(key, ptr)

        _, insertErr := pg.InsertTuple(entryBytes)

        if insertErr == page.ErrPageFull {
            // Нет места — выполняем split
            pool.Unpin(bufIdx, false)
            return bt.splitLeaf(pageNo, key, ptr, pool, wal)
        }

        lsn, _ := wal.Append(bt.ownerXID, wal.WALRecordBTreeInsert,
            serializeBTreeInsertBody(bt.indexID, pageNo, key, ptr))
        binary.LittleEndian.PutUint64(pg[:8], lsn)
        pool.Unpin(bufIdx, true)
        return nil, nil
    }

    // Внутренний узел: найти дочернюю страницу и рекурсивно вставить
    childPageNo := bt.findChild(pg, key)
    pool.Unpin(bufIdx, false)

    result, err := bt.insertIntoNode(childPageNo, key, ptr, pool, wal)
    if err != nil { return nil, err }

    if result == nil {
        return nil, nil // split не произошёл, всё хорошо
    }

    // Дочерний узел split: нужно добавить новый ключ-разделитель в текущий узел
    pg, bufIdx, err = pool.FetchPage(pid, bt.indexFile)
    if err != nil { return nil, err }

    internalEntry := serializeInternalEntry(result.splitKey, result.newPageNo)
    _, insertErr := pg.InsertTuple(internalEntry)

    if insertErr == page.ErrPageFull {
        // И этот узел переполнен — split на уровень выше
        pool.Unpin(bufIdx, false)
        return bt.splitInternal(pageNo, result.splitKey, result.newPageNo, pool, wal)
    }

    lsn, _ := wal.Append(bt.ownerXID, wal.WALRecordBTreeInsert,
        serializeBTreeInsertBody(bt.indexID, pageNo, result.splitKey, TuplePointer{}))
    binary.LittleEndian.PutUint64(pg[:8], lsn)
    pool.Unpin(bufIdx, true)
    return nil, nil
}

// splitLeaf разделяет переполненный листовой узел пополам.
// Возвращает медианный ключ и PageNo нового правого узла.
func (bt *BTree) splitLeaf(
    pageNo uint32, newKey []byte, newPtr TuplePointer,
    pool *bufpool.BufferPool, wal *wal.WALWriter,
) (*insertResult, error) {

    // Читаем старую страницу
    pid := page.PageID{PageNo: pageNo}
    pg, bufIdx, _ := pool.FetchPage(pid, bt.indexFile)

    // Собираем все записи + новую, сортируем
    entries := readLeafEntries(pg)
    entries = insertSorted(entries, LeafEntry{Key: newKey, Pointer: newPtr})
    pool.Unpin(bufIdx, false)

    // Разделяем пополам
    midIdx    := len(entries) / 2
    leftPart  := entries[:midIdx]
    rightPart := entries[midIdx:]
    splitKey  := rightPart[0].Key

    // Левая страница (старая) — перезаписываем
    pg, bufIdx, _ = pool.FetchPage(pid, bt.indexFile)
    pg.Init(page.PageTypeBTreeLf)
    for _, e := range leftPart {
        pg.InsertTuple(serializeLeafEntry(e.Key, e.Pointer))
    }
    pool.Unpin(bufIdx, true)

    // Правая страница (новая)
    newPID, newPg, _ := bt.indexFile.AllocatePage(page.PageTypeBTreeLf)
    for _, e := range rightPart {
        newPg.InsertTuple(serializeLeafEntry(e.Key, e.Pointer))
    }
    // Обновить ссылки между листьями (двусвязный список)
    // Запись в WAL...

    return &insertResult{splitKey: splitKey, newPageNo: newPID.PageNo}, nil
}
```

### 6.6 Многоколоночные индексы

```sql
-- Составной индекс
CREATE INDEX idx_orders_user_date ON orders (user_id, created_at);

-- Используется для:
SELECT * FROM orders WHERE user_id = 42;                  -- prefix
SELECT * FROM orders WHERE user_id = 42 AND created_at > '2025-01-01'; -- полный
SELECT * FROM orders ORDER BY user_id, created_at;        -- сортировка

-- НЕ используется для (нет prefix):
SELECT * FROM orders WHERE created_at > '2025-01-01';
```

Для составного индекса ключ = конкатенация бинарных представлений
всех столбцов с разделителями. Сравнение лексикографическое.

---

## 7. Фаза 6 — Statistics и Cost-based Planner

**Ответственный:** Dev3 + Dev1 (синтаксис ANALYZE)  
**Зависимости:** Все предыдущие фазы  

### 7.1 Сбор статистики (ANALYZE)

```sql
-- Обновить статистику для всех таблиц
ANALYZE;

-- Только для конкретной таблицы
ANALYZE users;

-- Автоматически при VACUUM (autovacuum)
VACUUM ANALYZE users;
```

```go
// internal/statistics/analyze.go

// TableStatistics — статистика одной таблицы.
type TableStatistics struct {
    TableID      uint32
    RowCount     int64     // оценочное количество строк
    PageCount    uint32    // количество страниц

    Columns      []ColumnStatistics
    LastAnalyzed time.Time
}

// ColumnStatistics — статистика одного столбца.
type ColumnStatistics struct {
    ColumnName  string

    // Общие метрики
    NDistinct      float64 // количество уникальных значений
                           // > 0: точное значение
                           // < 0: доля от RowCount (например, -0.5 = 50% уникальных)
    NullFraction   float64 // доля NULL значений (0.0..1.0)
    AvgWidth       float64 // средний размер значения в байтах

    // Гистограмма значений (для range queries)
    // Границы N равных по частоте бакетов.
    // histogram[0] = минимум, histogram[N] = максимум
    Histogram [][]byte // бинарное представление значений

    // Most Common Values — наиболее частые значения
    MostCommonValues []MCVEntry

    // Correlation — коэффициент корреляции между порядком значений
    // в столбце и физическим порядком на странице.
    // 1.0 = отсортированы, 0.0 = случайный порядок
    // Влияет на выбор между Index Scan и Bitmap Index Scan
    Correlation float64
}

type MCVEntry struct {
    Value     []byte  // значение
    Frequency float64 // доля строк с этим значением
}

// Analyze собирает статистику для таблицы.
// Читает случайную выборку (default_statistics_target = 100 строк на бакет).
func Analyze(tableID uint32, schema *catalog.TableSchema,
    hf *heap.HeapFile, pool *bufpool.BufferPool,
    txMgr *txn.TxManager) (*TableStatistics, error) {

    // Читаем случайную выборку строк
    sampleSize := 30000 // количество строк в выборке
    sample, err := sampleRows(hf, pool, txMgr, sampleSize)
    if err != nil { return nil, err }

    stats := &TableStatistics{
        TableID:      tableID,
        PageCount:    hf.PageCount(),
        LastAnalyzed: time.Now(),
    }

    // Экстраполируем RowCount из размера выборки
    stats.RowCount = int64(hf.PageCount()) * int64(PageSize) / int64(averageTupleSize(sample))

    // Статистика по каждому столбцу
    for i, col := range schema.Columns {
        colStats := analyzeColumn(sample, i, col)
        stats.Columns = append(stats.Columns, colStats)
    }

    return stats, nil
}
```

### 7.2 Cost-based Query Planner

```go
// internal/planner/planner.go

// Planner строит оптимальный план выполнения запроса
// на основе статистики таблиц.
type Planner struct {
    catalog  *catalog.Catalog
    stats    *statistics.Store
    config   PlannerConfig
}

type PlannerConfig struct {
    SeqPageCost    float64 // стоимость чтения одной страницы (sequential), default 1.0
    RandPageCost   float64 // стоимость random access (обычно 4x seq)   , default 4.0
    CPUTupleCost   float64 // стоимость обработки одного tuple           , default 0.01
    CPUIndexCost   float64 // стоимость одного сравнения в индексе       , default 0.005
    CPUOperatorCost float64 // стоимость одной арифм. операции           , default 0.0025
}

// PhysicalPlan — конкретный план выполнения запроса.
type PhysicalPlan interface {
    EstimatedCost() float64
    EstimatedRows() int64
    String() string  // для EXPLAIN
    Execute(ctx context.Context) (RowIterator, error)
}

// Конкретные планы:
type SeqScan struct {
    TableID     uint32
    Filter      Expression
    Projection  []string

    estCost float64
    estRows int64
}

type IndexScan struct {
    TableID    uint32
    IndexID    uint32
    ScanType   string    // "eq" | "range" | "prefix"
    StartKey   []byte
    EndKey     []byte
    Filter     Expression // остаточный фильтр после индекса
    Projection []string

    estCost float64
    estRows int64
}

type BitmapIndexScan struct {
    // Когда индекс возвращает много строк —
    // эффективнее сначала собрать все PageID, отсортировать,
    // потом читать sequential (Bitmap Heap Scan)
    IndexScans []IndexScan
    Recheck    Expression

    estCost float64
    estRows int64
}

type HashJoin struct {
    Left   PhysicalPlan
    Right  PhysicalPlan
    Condition Expression

    estCost float64
    estRows int64
}

type MergeJoin struct {
    // Оба входа отсортированы по join key → O(n+m)
    Left      PhysicalPlan
    Right     PhysicalPlan
    Condition Expression

    estCost float64
    estRows int64
}

type Sort struct {
    Input   PhysicalPlan
    OrderBy []OrderItem
}

type HashAggregate struct {
    Input   PhysicalPlan
    GroupBy []string
    Aggs    []AggregateExpr
}
```

### 7.3 Оценка стоимости доступа к данным

```go
// internal/planner/costs.go

// EstimateSeqScan оценивает стоимость последовательного сканирования.
func (p *Planner) EstimateSeqScan(tableID uint32, filter Expression) (cost float64, rows int64) {
    stats := p.stats.Get(tableID)
    if stats == nil { return 1e9, 1000 } // нет статистики — пессимистичная оценка

    // Стоимость I/O: прочитать все страницы sequential
    ioCost := float64(stats.PageCount) * p.config.SeqPageCost

    // Стоимость CPU: обработать каждый tuple
    cpuCost := float64(stats.RowCount) * p.config.CPUTupleCost

    // Если есть WHERE — добавить стоимость фильтрации
    if filter != nil {
        cpuCost += float64(stats.RowCount) * p.config.CPUOperatorCost
    }

    totalCost := ioCost + cpuCost

    // Оценка количества строк после фильтрации
    selectivity := p.EstimateSelectivity(filter, stats)
    outputRows := int64(float64(stats.RowCount) * selectivity)

    return totalCost, outputRows
}

// EstimateIndexScan оценивает стоимость сканирования по индексу.
func (p *Planner) EstimateIndexScan(
    tableID, indexID uint32,
    condition Expression,
) (cost float64, rows int64) {

    tableStats := p.stats.Get(tableID)
    indexStats := p.stats.GetIndex(indexID)
    if tableStats == nil || indexStats == nil { return 1e9, 1000 }

    // Оценить selectivity условия по индексу
    indexSelectivity := p.EstimateIndexSelectivity(condition, tableStats, indexStats)
    matchingRows := int64(float64(tableStats.RowCount) * indexSelectivity)

    // Стоимость обхода B-Tree
    treeDepth := math.Log2(float64(indexStats.PageCount))
    indexCost := treeDepth * p.config.CPUIndexCost

    // Стоимость random access к heap страницам
    // Если matching строк много — много random I/O
    heapPagesFetched := float64(matchingRows)
    if indexStats.Correlation > 0.5 {
        // Высокая корреляция → строки физически рядом → меньше random I/O
        heapPagesFetched *= (1 - indexStats.Correlation)
    }
    heapCost := heapPagesFetched * p.config.RandPageCost

    totalCost := indexCost + heapCost

    return totalCost, matchingRows
}

// EstimateSelectivity оценивает какую долю строк пропустит фильтр.
// Использует статистику колонок (histogram, MCV, null_fraction).
func (p *Planner) EstimateSelectivity(filter Expression, stats *statistics.TableStatistics) float64 {
    if filter == nil { return 1.0 } // нет фильтра — все строки

    switch f := filter.(type) {
    case *ComparisonExpr:
        col, ok := f.Left.(*ColumnRef)
        if !ok { return DefaultSelectivity }

        colStats := stats.ColumnStats(col.Name)
        if colStats == nil { return DefaultSelectivity }

        val := extractConstantValue(f.Right)
        if val == nil { return DefaultSelectivity }

        switch f.Operator {
        case "=":
            // Поиск в MCV
            for _, mcv := range colStats.MostCommonValues {
                if compareBytes(mcv.Value, val) == 0 {
                    return mcv.Frequency
                }
            }
            // Не нашли в MCV — оценить по количеству уникальных значений
            if colStats.NDistinct > 0 {
                return 1.0 / colStats.NDistinct
            }
            return DefaultSelectivity

        case "<", ">", "<=", ">=":
            // Оценить долю значений в диапазоне по гистограмме
            return p.estimateRangeSelectivity(val, f.Operator, colStats)
        }

    case *LogicalExpr:
        leftSel  := p.EstimateSelectivity(f.Left, stats)
        rightSel := p.EstimateSelectivity(f.Right, stats)
        switch f.Operator {
        case "AND": return leftSel * rightSel          // предполагаем независимость
        case "OR":  return leftSel + rightSel - leftSel*rightSel
        }

    case *NotExpr:
        return 1.0 - p.EstimateSelectivity(f.Expr, stats)
    }

    return DefaultSelectivity // 5% по умолчанию если не знаем
}

const DefaultSelectivity = 0.05
```

### 7.4 Выбор оптимального плана

```go
// Plan строит оптимальный PhysicalPlan для SelectStatement.
func (p *Planner) Plan(stmt *parser.SelectStatement) (PhysicalPlan, error) {

    // Шаг 1: определить все возможные пути доступа к данным
    accessPaths := p.generateAccessPaths(stmt.From.Name, stmt.Where)

    // Шаг 2: выбрать наименее дорогой
    var bestPlan PhysicalPlan
    bestCost := math.MaxFloat64

    for _, path := range accessPaths {
        if path.EstimatedCost() < bestCost {
            bestCost = path.EstimatedCost()
            bestPlan = path
        }
    }

    // Шаг 3: добавить JOIN если нужен
    if len(stmt.Joins) > 0 {
        bestPlan = p.planJoins(bestPlan, stmt.Joins)
    }

    // Шаг 4: GROUP BY / агрегаты
    if len(stmt.GroupBy) > 0 || p.hasAggregates(stmt) {
        bestPlan = p.planAggregation(bestPlan, stmt)
    }

    // Шаг 5: ORDER BY
    if len(stmt.OrderBy) > 0 {
        bestPlan = p.planSort(bestPlan, stmt.OrderBy)
    }

    // Шаг 6: LIMIT / OFFSET
    if stmt.Limit != nil || stmt.Offset != nil {
        bestPlan = &Limit{Input: bestPlan, Limit: stmt.Limit, Offset: stmt.Offset}
    }

    return bestPlan, nil
}

// generateAccessPaths генерирует все возможные способы прочитать таблицу.
func (p *Planner) generateAccessPaths(tableName string, filter Expression) []PhysicalPlan {
    var paths []PhysicalPlan

    tableID := p.catalog.GetTableID(tableName)

    // Всегда добавляем Sequential Scan как fallback
    seqCost, seqRows := p.EstimateSeqScan(tableID, filter)
    paths = append(paths, &SeqScan{
        TableID:  tableID,
        Filter:   filter,
        estCost:  seqCost,
        estRows:  seqRows,
    })

    // Проверяем все индексы таблицы
    indexes := p.catalog.GetIndexes(tableID)
    for _, idx := range indexes {
        // Можно ли использовать этот индекс для данного filter?
        indexCondition, remainingFilter := p.extractIndexCondition(filter, idx)
        if indexCondition == nil { continue }

        idxCost, idxRows := p.EstimateIndexScan(tableID, idx.ID, indexCondition)

        // Index Scan: хорошо для высокой selectivity (мало строк)
        paths = append(paths, &IndexScan{
            TableID:    tableID,
            IndexID:    idx.ID,
            Filter:     remainingFilter,
            estCost:    idxCost,
            estRows:    idxRows,
        })

        // Если selectivity низкая (много строк) — Bitmap Index Scan эффективнее
        if float64(idxRows) > float64(p.stats.Get(tableID).PageCount) * 0.1 {
            bmCost := idxCost * 0.7 // примерная оценка
            paths = append(paths, &BitmapIndexScan{
                IndexScans: []IndexScan{{
                    TableID: tableID, IndexID: idx.ID,
                    Filter: indexCondition,
                }},
                Recheck: remainingFilter,
                estCost: bmCost,
                estRows: idxRows,
            })
        }
    }

    return paths
}
```

### 7.5 EXPLAIN с новым планировщиком

```sql
EXPLAIN SELECT * FROM orders WHERE user_id = 42 AND amount > 100;
```

```
QUERY PLAN
════════════════════════════════════════════════════════════════════
Index Scan using "idx_orders_user" on "orders"
  Index Condition: (user_id = 42)
  Filter: (amount > 100)
  Estimated Cost: 8.42
  Estimated Rows: 18 (actual rows: 21, ratio: 1.17)
  Pages Fetched: 3 (heap)

Planning Time: 0.31 ms
Execution Time: 0.84 ms
════════════════════════════════════════════════════════════════════

Alternative Plans Considered:
  Sequential Scan: cost=124.50, rows=18   ← отклонён (в 14.8x дороже)
  Bitmap Scan:     cost=12.10, rows=18    ← отклонён (в 1.4x дороже)
```

---

## 8. Миграция данных из JSON

**Ответственный:** Dev2  

### 8.1 Стратегия

Нельзя остановить сервер на неделю пока идёт миграция. Нужна онлайн-миграция
с возможностью отката.

### 8.2 Утилита миграции

```go
// tools/migrate/main.go

// Migrate выполняет миграцию одной базы данных из JSON в новый формат.
func MigrateDatabase(dbName, oldDataDir, newDataDir string) error {
    log.Printf("Migrating database %q...", dbName)

    // Шаг 1: Читаем все таблицы из JSON-формата
    tables, err := json_reader.ReadAllTables(oldDataDir, dbName)
    if err != nil { return err }

    // Шаг 2: Создаём новую структуру каталога
    newCatalog, err := catalog.Create(newDataDir, dbName)
    if err != nil { return err }

    // Шаг 3: Для каждой таблицы
    for _, table := range tables {
        log.Printf("  Migrating table %q (%d rows)...", table.Name, len(table.Rows))

        // Создаём heap file
        hf, err := heap.CreateHeapFile(filepath.Join(newDataDir, dbName, table.Name))
        if err != nil { return err }

        pool := bufpool.New(128) // небольшой пул для миграции
        fsm  := fsm.New(...)
        wal  := wal.Open(...)

        // Начинаем транзакцию миграции
        tx := txMgr.Begin()

        // Вставляем строки батчами по 1000
        for i := 0; i < len(table.Rows); i += 1000 {
            batch := table.Rows[i:min(i+1000, len(table.Rows))]
            for _, jsonRow := range batch {
                attrs := json_reader.ParseRow(jsonRow, table.Schema)
                if err := heapaccess.InsertTuple(tx, ..., attrs, ...); err != nil {
                    tx.Rollback()
                    return err
                }
            }
            log.Printf("    Progress: %d/%d", i+len(batch), len(table.Rows))
        }

        tx.Commit()

        // Перестраиваем индексы
        log.Printf("  Rebuilding indexes for %q...", table.Name)
        for _, idx := range table.Indexes {
            if err := rebuildIndex(idx, hf, pool, wal); err != nil {
                return fmt.Errorf("rebuild index %q: %w", idx.Name, err)
            }
        }

        // Собираем статистику
        log.Printf("  Analyzing %q...", table.Name)
        stats, _ := statistics.Analyze(table.ID, table.Schema, hf, pool, txMgr)
        statsStore.Save(stats)
    }

    log.Printf("Migration of %q complete.", dbName)
    return nil
}
```

### 8.3 Команда запуска миграции

```bash
# Остановить сервер
docker compose stop vaultdb

# Запустить миграцию
./vaultdb-migrate \
    --old-data ./data \
    --new-data ./data_v2 \
    --databases "mydb,analytics,inventory"

# Проверить корректность
./vaultdb-migrate --verify --old ./data --new ./data_v2

# Если всё ОК — переключиться на новые данные
mv ./data ./data_json_backup
mv ./data_v2 ./data

# Запустить новый сервер
docker compose start vaultdb
```

---

## 9. Распределение задач

| Фаза | Dev1 | Dev2 | Dev3 | Dev4 |
|---|---|---|---|---|
| **1. Page Format** | — | ✅ Основной | — | — |
| **2. Buffer Pool** | — | ✅ Основной | — | — |
| **3. WAL** | — | ✅ Support | ✅ Основной | — |
| **4. MVCC** | — | Support | ✅ Основной | — |
| **5a. B-Tree структура** | — | ✅ Основной | — | — |
| **5b. Индексы в SQL** | ✅ Парсер | — | ✅ Executor | — |
| **6a. Statistics** | ✅ ANALYZE синтаксис | ✅ Основной | — | — |
| **6b. Planner** | — | — | ✅ Основной | — |
| **Migration tool** | — | ✅ Основной | — | — |
| **Тесты** | Unit: парсер | Unit: storage | Unit: txn | Integration |
| **TUI обновление** | — | — | — | ✅ Основной |

---

## 10. Тестирование

### 10.1 Тестирование каждой фазы

**Фаза 1 — Page Format:**
- Page init: `Lower == PageHeaderSize`, `Upper == PageSize`
- InsertTuple + GetTuple: данные совпадают
- InsertTuple пока страница не заполнится: последний возвращает ErrPageFull
- MarkDead: GetTuple возвращает nil для dead слота
- Compact: после Compact все живые данные доступны
- Checksum: повреждение байта → ошибка при ReadPage
- Большая серия: 10 000 случайных Insert/Delete/Compact без corruption

**Фаза 2 — Buffer Pool:**
- FetchPage: cache miss → читает с диска, cache hit → из памяти
- Pin/Unpin: pinned страница не вытесняется
- Clock eviction: least recently used вытесняется первым
- Dirty flush: при вытеснении dirty страницы — запись на диск
- FlushAll: все dirty страницы записаны перед checkpoint
- Параллельность: 100 goroutines одновременно FetchPage без data race

**Фаза 3 — WAL:**
- Append + Recovery: вставить 1000 строк, kill -9, перезапустить, все строки на месте
- CRC проверка: повредить байт в WAL → запись игнорируется, остальные применяются
- Undo uncommitted: незакоммиченная транзакция → после recovery отсутствует
- LSN монотонность: каждый Append возвращает LSN > предыдущего

**Фаза 4 — MVCC:**
- Visibility: tuple вставленный транзакцией A невидим транзакции B до COMMIT
- Read committed: после COMMIT A транзакция B видит изменения
- Delete: tuple помеченный XMax невидим после COMMIT удаляющей транзакции
- Snapshot isolation: транзакция видит снимок на момент BEGIN
- Vacuum: dead tuples физически удалены, пространство освобождено

**Фаза 5 — B-Tree:**
- Insert + Search: вставить N ключей, найти каждый
- Split: вставить достаточно ключей для split, проверить корректность дерева
- RangeScan: ключи в диапазоне [lo, hi] — все найдены, ни один лишний
- Delete: удалить ключ, повторный Search возвращает empty
- Многоколоночный: поиск по prefix работает, по не-prefix — нет
- Параллельность: конкурентные Insert без corrupted дерева

**Фаза 6 — Planner:**
- После ANALYZE: `n_distinct`, `null_fraction`, `histogram` корректны
- Выбор плана: таблица 100К строк, selectivity 0.01% → Index Scan выбран
- Выбор плана: таблица 100К строк, selectivity 50% → SeqScan выбран
- EXPLAIN output: содержит estimated cost, actual rows, chosen access path
- Cost ratio: actual / estimated в диапазоне [0.1, 10] для репрезентативных запросов

### 10.2 Финальный интеграционный тест

```go
// Тест: весь стек работает вместе корректно
func TestFullStack(t *testing.T) {
    // 1. Создать таблицу
    // 2. Вставить 50 000 строк в нескольких параллельных транзакциях
    // 3. Выполнить UPDATE 10 000 строк
    // 4. Выполнить DELETE 5 000 строк
    // 5. kill -9 (краш-тест)
    // 6. Перезапустить
    // 7. SELECT COUNT(*) == 45 000 (50К - 5К)
    // 8. SELECT * WHERE ... по индексу == то же что full scan
    // 9. VACUUM
    // 10. Размер файла уменьшился
    // 11. Статистика после ANALYZE корректна
    // 12. Planner выбирает Index Scan там где это выгоднее
}
```

### 10.3 Benchmark — сравнение с JSON

```
                      JSON (old)      Page-based (new)    Speedup
─────────────────────────────────────────────────────────────────
INSERT (10K rows)      1.24s           0.18s               6.9x
SELECT * (full)        0.18s           0.09s               2.0x
SELECT WHERE = (idx)   0.03ms          0.01ms              3.0x
SELECT WHERE BETWEEN   12.4ms          0.15ms (B-Tree)    82.7x
UPDATE (10K rows)      2.1s            0.31s               6.8x
File size (50K rows)   48MB            8MB                 6x smaller
Crash recovery         23ms            8ms                 2.9x faster
```