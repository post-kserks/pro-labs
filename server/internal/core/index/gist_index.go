package index

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"sync"
)

const rtreeCapacity = 4

type MBR struct {
	Min, Max float64
}

func mbrUnion(a, b MBR) MBR {
	return MBR{
		Min: math.Min(a.Min, b.Min),
		Max: math.Max(a.Max, b.Max),
	}
}

func mbrArea(m MBR) float64 {
	return m.Max - m.Min
}

func mbrContains(m MBR, qmin, qmax float64) bool {
	return m.Min <= qmin && m.Max >= qmax
}

func mbrOverlaps(m MBR, qmin, qmax float64) bool {
	return m.Min <= qmax && m.Max >= qmin
}

type gistEntry struct {
	Key string
	Pos int
	Min float64
	Max float64
}

type rtreeNode struct {
	MBR      MBR
	Children []*rtreeNode
	Entries  []gistEntry
	IsLeaf   bool
}

func (n *rtreeNode) updateMBR() {
	if n.IsLeaf {
		if len(n.Entries) > 0 {
			n.MBR = MBR{Min: n.Entries[0].Min, Max: n.Entries[0].Max}
			for _, e := range n.Entries[1:] {
				n.MBR = mbrUnion(n.MBR, MBR{Min: e.Min, Max: e.Max})
			}
		}
	} else {
		if len(n.Children) > 0 {
			n.MBR = n.Children[0].MBR
			for _, c := range n.Children[1:] {
				n.MBR = mbrUnion(n.MBR, c.MBR)
			}
		}
	}
}

type GiSTIndex struct {
	mu       sync.RWMutex
	name     string
	column   string
	colIndex int
	unique   bool
	root     *rtreeNode
}

func NewGiSTIndex(name, column string, colIndex int) *GiSTIndex {
	return &GiSTIndex{
		name:     name,
		column:   column,
		colIndex: colIndex,
		root:     &rtreeNode{IsLeaf: true},
	}
}

func (g *GiSTIndex) Type() string     { return "gist" }
func (g *GiSTIndex) Name() string     { return g.name }
func (g *GiSTIndex) Column() string   { return g.column }
func (g *GiSTIndex) ColIndex() int    { return g.colIndex }
func (g *GiSTIndex) IsUnique() bool   { return g.unique }
func (g *GiSTIndex) SetUnique(u bool) { g.unique = u }

// Columns returns nil — GiST index does not support index-only scan.
func (g *GiSTIndex) Columns() []string { return nil }

// HasStoredColumns returns false — GiST index does not store columns.
func (g *GiSTIndex) HasStoredColumns() bool { return false }

// GetStoredColumns returns nil — GiST index does not store columns.
func (g *GiSTIndex) GetStoredColumns(rowPos int) (map[string]interface{}, bool) {
	return nil, false
}

func (g *GiSTIndex) RenameColumn(old, new string) {
	g.mu.Lock()
	g.column = new
	g.mu.Unlock()
}

func (g *GiSTIndex) Lookup(value string) ([]int, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var result []int
	g.lookupNode(g.root, value, &result)
	return result, len(result) > 0
}

func (g *GiSTIndex) lookupNode(n *rtreeNode, value string, result *[]int) {
	if n.IsLeaf {
		for _, e := range n.Entries {
			if e.Key == value {
				*result = append(*result, e.Pos)
			}
		}
		return
	}
	for _, child := range n.Children {
		g.lookupNode(child, value, result)
	}
}

func (g *GiSTIndex) Insert(value string, rowPos int) {
	g.Add(rowPos, value)
}

func (g *GiSTIndex) Delete(rowPos int) {
	g.Remove(rowPos)
}

func (g *GiSTIndex) Rebuild(rows []IndexableRow) {
	g.mu.Lock()
	defer g.mu.Unlock()

	var entries []gistEntry
	for i, row := range rows {
		if row.DeletedTx == 0 {
			s := gistValueToString(row.Data[g.colIndex])
			min, max := gistParseRange(s)
			entries = append(entries, gistEntry{Key: s, Pos: i, Min: min, Max: max})
		}
	}
	if len(entries) == 0 {
		g.root = &rtreeNode{IsLeaf: true}
		return
	}
	g.root = g.bulkLoad(entries)
}

func (g *GiSTIndex) bulkLoad(entries []gistEntry) *rtreeNode {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Min < entries[j].Min
	})

	if len(entries) <= rtreeCapacity {
		node := &rtreeNode{IsLeaf: true, Entries: entries}
		node.updateMBR()
		return node
	}

	chunkSize := int(math.Ceil(float64(len(entries)) / float64(rtreeCapacity)))
	if chunkSize > rtreeCapacity {
		chunkSize = rtreeCapacity
	}

	var children []*rtreeNode
	for i := 0; i < len(entries); i += chunkSize {
		end := i + chunkSize
		if end > len(entries) {
			end = len(entries)
		}
		chunk := entries[i:end]
		if len(chunk) <= rtreeCapacity {
			child := &rtreeNode{IsLeaf: true, Entries: chunk}
			child.updateMBR()
			children = append(children, child)
		} else {
			children = append(children, g.bulkLoad(chunk))
		}
	}

	if len(children) <= rtreeCapacity {
		node := &rtreeNode{IsLeaf: false, Children: children}
		node.updateMBR()
		return node
	}

	return g.buildInternal(children)
}

func (g *GiSTIndex) buildInternal(children []*rtreeNode) *rtreeNode {
	sort.Slice(children, func(i, j int) bool {
		return children[i].MBR.Min < children[j].MBR.Min
	})

	if len(children) <= rtreeCapacity {
		node := &rtreeNode{IsLeaf: false, Children: children}
		node.updateMBR()
		return node
	}

	chunkSize := int(math.Ceil(float64(len(children)) / float64(rtreeCapacity)))
	var parents []*rtreeNode
	for i := 0; i < len(children); i += chunkSize {
		end := i + chunkSize
		if end > len(children) {
			end = len(children)
		}
		chunk := children[i:end]
		p := &rtreeNode{IsLeaf: false, Children: chunk}
		p.updateMBR()
		parents = append(parents, p)
	}

	if len(parents) <= rtreeCapacity {
		node := &rtreeNode{IsLeaf: false, Children: parents}
		node.updateMBR()
		return node
	}
	return g.buildInternal(parents)
}

func (g *GiSTIndex) Add(rowID int, value interface{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.addLocked(rowID, value)
}

func (g *GiSTIndex) addLocked(rowID int, value interface{}) {
	s := gistValueToString(value)
	min, max := gistParseRange(s)
	entry := gistEntry{Key: s, Pos: rowID, Min: min, Max: max}
	g.insertEntry(g.root, entry)
}

func (g *GiSTIndex) insertEntry(n *rtreeNode, e gistEntry) {
	if n.IsLeaf {
		n.Entries = append(n.Entries, e)
		n.updateMBR()
		if len(n.Entries) > rtreeCapacity {
			g.splitLeaf(n)
		}
		return
	}

	best := g.chooseSubtree(n, e)
	g.insertEntry(best, e)
	n.updateMBR()
	if len(n.Children) > rtreeCapacity {
		g.splitInternal(n)
	}
}

func (g *GiSTIndex) chooseSubtree(n *rtreeNode, e gistEntry) *rtreeNode {
	best := n.Children[0]
	bestEnl := g.enlargement(best, e)
	for _, child := range n.Children[1:] {
		enl := g.enlargement(child, e)
		if enl < bestEnl || (enl == bestEnl && mbrArea(child.MBR) < mbrArea(best.MBR)) {
			best = child
			bestEnl = enl
		}
	}
	return best
}

func (g *GiSTIndex) enlargement(node *rtreeNode, e gistEntry) float64 {
	combined := mbrUnion(node.MBR, MBR{Min: e.Min, Max: e.Max})
	return mbrArea(combined) - mbrArea(node.MBR)
}

func (g *GiSTIndex) splitLeaf(n *rtreeNode) {
	all := n.Entries
	sort.Slice(all, func(i, j int) bool {
		return all[i].Min < all[j].Min
	})
	split := len(all) / 2
	n.Entries = all[:split]
	n.updateMBR()

	newNode := &rtreeNode{IsLeaf: true, Entries: all[split:]}
	newNode.updateMBR()

	if n == g.root {
		g.root = &rtreeNode{IsLeaf: false, Children: []*rtreeNode{n, newNode}}
		g.root.updateMBR()
		return
	}

	g.addToParent(n, newNode)
}

func (g *GiSTIndex) splitInternal(n *rtreeNode) {
	all := n.Children
	sort.Slice(all, func(i, j int) bool {
		return all[i].MBR.Min < all[j].MBR.Min
	})
	split := len(all) / 2
	n.Children = all[:split]
	n.updateMBR()

	newNode := &rtreeNode{IsLeaf: false, Children: all[split:]}
	newNode.updateMBR()

	if n == g.root {
		g.root = &rtreeNode{IsLeaf: false, Children: []*rtreeNode{n, newNode}}
		g.root.updateMBR()
		return
	}

	g.addToParent(n, newNode)
}

func (g *GiSTIndex) addToParent(existing, new *rtreeNode) {
	var findParent func(n *rtreeNode, target *rtreeNode) *rtreeNode
	findParent = func(n *rtreeNode, target *rtreeNode) *rtreeNode {
		if n.IsLeaf {
			return nil
		}
		for _, c := range n.Children {
			if c == target {
				return n
			}
		}
		for _, c := range n.Children {
			if r := findParent(c, target); r != nil {
				return r
			}
		}
		return nil
	}

	parent := findParent(g.root, existing)
	if parent != nil {
		parent.Children = append(parent.Children, new)
		parent.updateMBR()
	}
}

func (g *GiSTIndex) Remove(rowID int) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.removeNode(g.root, rowID)
}

func (g *GiSTIndex) removeNode(n *rtreeNode, rowID int) bool {
	if n.IsLeaf {
		for i, e := range n.Entries {
			if e.Pos == rowID {
				n.Entries = append(n.Entries[:i], n.Entries[i+1:]...)
				n.updateMBR()
				return true
			}
		}
		return false
	}
	for _, child := range n.Children {
		if g.removeNode(child, rowID) {
			n.updateMBR()
			return true
		}
	}
	return false
}

func (g *GiSTIndex) SearchRange(min, max float64) []int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var result []int
	g.searchRangeNode(g.root, min, max, &result)
	return result
}

func (g *GiSTIndex) searchRangeNode(n *rtreeNode, min, max float64, result *[]int) {
	if n.IsLeaf {
		for _, e := range n.Entries {
			if e.Max >= min && e.Min <= max {
				*result = append(*result, e.Pos)
			}
		}
		return
	}
	for _, child := range n.Children {
		if mbrContains(child.MBR, min, max) || mbrOverlaps(child.MBR, min, max) {
			g.searchRangeNode(child, min, max, result)
		}
	}
}

func (g *GiSTIndex) SearchOverlap(queryMin, queryMax float64) []int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var result []int
	g.searchOverlapNode(g.root, queryMin, queryMax, &result)
	return result
}

func (g *GiSTIndex) searchOverlapNode(n *rtreeNode, queryMin, queryMax float64, result *[]int) {
	if n.IsLeaf {
		for _, e := range n.Entries {
			if e.Min <= queryMax && e.Max >= queryMin {
				*result = append(*result, e.Pos)
			}
		}
		return
	}
	for _, child := range n.Children {
		if mbrOverlaps(child.MBR, queryMin, queryMax) {
			g.searchOverlapNode(child, queryMin, queryMax, result)
		}
	}
}

func gistValueToString(v interface{}) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func gistParseRange(s string) (float64, float64) {
	var min, max float64
	n, err := fmt.Sscanf(s, "%f-%f", &min, &max)
	if err != nil || n < 1 {
		if f, err := fmt.Sscanf(s, "%f", &min); err == nil && f == 1 {
			return min, min
		}
		return 0, 0
	}
	if n == 1 {
		max = min
	}
	return min, max
}

type rtreeSerialized struct {
	Name     string           `json:"name"`
	Column   string           `json:"column"`
	ColIndex int              `json:"colIndex"`
	Root     *rtreeNodeSerial `json:"root"`
}

type rtreeNodeSerial struct {
	MBR      MBR                `json:"mbr"`
	Children []*rtreeNodeSerial `json:"children,omitempty"`
	Entries  []gistEntry        `json:"entries,omitempty"`
	IsLeaf   bool               `json:"isLeaf"`
}

func serializeTree(n *rtreeNode) *rtreeNodeSerial {
	if n == nil {
		return nil
	}
	s := &rtreeNodeSerial{
		MBR:    n.MBR,
		IsLeaf: n.IsLeaf,
	}
	if n.IsLeaf {
		s.Entries = n.Entries
	} else {
		s.Children = make([]*rtreeNodeSerial, len(n.Children))
		for i, c := range n.Children {
			s.Children[i] = serializeTree(c)
		}
	}
	return s
}

func deserializeTree(s *rtreeNodeSerial) *rtreeNode {
	if s == nil {
		return nil
	}
	n := &rtreeNode{
		MBR:    s.MBR,
		IsLeaf: s.IsLeaf,
	}
	if s.IsLeaf {
		n.Entries = s.Entries
	} else {
		n.Children = make([]*rtreeNode, len(s.Children))
		for i, cs := range s.Children {
			n.Children[i] = deserializeTree(cs)
		}
	}
	return n
}

func (g *GiSTIndex) Save(path string) error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	data := rtreeSerialized{
		Name:     g.name,
		Column:   g.column,
		ColIndex: g.colIndex,
		Root:     serializeTree(g.root),
	}
	bytes, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, bytes, 0600) //nolint:gosec // index metadata, not sensitive
}

func (g *GiSTIndex) Load(path string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	bytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var data rtreeSerialized
	if err := json.Unmarshal(bytes, &data); err != nil {
		return err
	}
	if data.Name != "" {
		g.name = data.Name
	}
	if data.Column != "" {
		g.column = data.Column
	}
	g.colIndex = data.ColIndex
	if data.Root != nil {
		g.root = deserializeTree(data.Root)
	}
	return nil
}
