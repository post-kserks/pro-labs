package executor

import (
	"container/list"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// compiledPattern — compiled LIKE pattern. Для паттернов только с '%'
// используется быстрый путь на strings (в разы быстрее regexp); для паттернов
// с '_' компилируется regexp — один раз, дальше берётся из кэша.
type compiledPattern struct {
	simple   bool
	segments []string       // для простых паттернов: куски между '%'
	re       *regexp.Regexp // для сложных паттернов
}

func (cp *compiledPattern) match(text string) bool {
	if cp.simple {
		return matchSegments(text, cp.segments)
	}
	return cp.re.MatchString(text)
}

// matchSegments проверяет text против паттерна, разбитого по '%':
// первый сегмент — префикс, последний — суффикс, промежуточные должны
// встречаться по порядку между ними.
func matchSegments(text string, segs []string) bool {
	if len(segs) == 1 {
		// Pattern without '%' — точное совпадение
		return text == segs[0]
	}
	if !strings.HasPrefix(text, segs[0]) {
		return false
	}
	text = text[len(segs[0]):]

	last := segs[len(segs)-1]
	if len(text) < len(last) || !strings.HasSuffix(text, last) {
		return false
	}
	text = text[:len(text)-len(last)]

	for _, mid := range segs[1 : len(segs)-1] {
		if mid == "" {
			continue
		}
		i := strings.Index(text, mid)
		if i < 0 {
			return false
		}
		text = text[i+len(mid):]
	}
	return true
}

func compilePattern(pattern string) (*compiledPattern, error) {
	if !strings.Contains(pattern, "_") {
		// Only '%' and literals — быстрый путь без regexp
		return &compiledPattern{
			simple:   true,
			segments: strings.Split(pattern, "%"),
		}, nil
	}

	var b strings.Builder
	b.WriteString("(?s)^")
	for _, r := range pattern {
		switch r {
		case '%':
			b.WriteString(".*")
		case '_':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")

	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, fmt.Errorf("invalid LIKE pattern %q: %w", pattern, err)
	}
	return &compiledPattern{re: re}, nil
}

// likeCache — LRU cache of compiled patterns: запрос вида
// WHERE name LIKE '%x%' по миллиону строк компилирует паттерн один раз,
// а не миллион.
type likePatternCache struct {
	mu       sync.Mutex
	capacity int
	entries  map[string]*list.Element
	order    *list.List // front = недавно использованный
}

type likeCacheEntry struct {
	pattern  string
	compiled *compiledPattern
}

func newLikePatternCache(capacity int) *likePatternCache {
	return &likePatternCache{
		capacity: capacity,
		entries:  make(map[string]*list.Element, capacity),
		order:    list.New(),
	}
}

func (c *likePatternCache) getOrCompile(pattern string) (*compiledPattern, error) {
	c.mu.Lock()
	if el, ok := c.entries[pattern]; ok {
		c.order.MoveToFront(el)
		cp := el.Value.(*likeCacheEntry).compiled
		c.mu.Unlock()
		return cp, nil
	}
	c.mu.Unlock()

	// Compile without holding lock: компиляция может быть дорогой
	cp, err := compilePattern(pattern)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[pattern]; ok {
		// Someone compiled it in parallel
		c.order.MoveToFront(el)
		return el.Value.(*likeCacheEntry).compiled, nil
	}
	el := c.order.PushFront(&likeCacheEntry{pattern: pattern, compiled: cp})
	c.entries[pattern] = el
	if c.order.Len() > c.capacity {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*likeCacheEntry).pattern)
	}
	return cp, nil
}

// likeCache хранит последние 256 скомпилированных паттернов.
var likeCache = newLikePatternCache(256)

// evalLike implements SQL LIKE: % matches any run of characters, _ matches a
// single character. NULL operands never match.
func evalLike(left, right interface{}) (bool, error) {
	if left == nil || right == nil {
		return false, nil
	}
	pattern, ok := right.(string)
	if !ok {
		return false, fmt.Errorf("LIKE pattern must be a string, got %T", right)
	}
	text, ok := left.(string)
	if !ok {
		text = valueToString(left)
	}

	cp, err := likeCache.getOrCompile(pattern)
	if err != nil {
		return false, err
	}
	return cp.match(text), nil
}

// evalILike implements SQL ILIKE: case-insensitive pattern matching.
// Same as evalLike but converts both operands to lowercase before matching.
func evalILike(left, right interface{}) (bool, error) {
	if left == nil || right == nil {
		return false, nil
	}
	pattern, ok := right.(string)
	if !ok {
		return false, fmt.Errorf("ILIKE pattern must be a string, got %T", right)
	}
	text, ok := left.(string)
	if !ok {
		text = valueToString(left)
	}

	cp, err := likeCache.getOrCompile(strings.ToLower(pattern))
	if err != nil {
		return false, err
	}
	return cp.match(strings.ToLower(text)), nil
}
