package storage

import "sort"

// RowPosition represents a physical location of a row in the database.
type RowPosition struct {
	PageID uint64
	Slot   uint16
}

// PageBitmap stores a collection of row positions grouped by page.
// This is a simplified in-memory bitmap index representation.
type PageBitmap struct {
	// Pages maps a PageID to a sorted slice of slots.
	Pages map[uint64][]uint16
}

// NewPageBitmap creates a new empty PageBitmap.
func NewPageBitmap() *PageBitmap {
	return &PageBitmap{
		Pages: make(map[uint64][]uint16),
	}
}

// Add inserts a new pageID and slot into the bitmap, keeping slots sorted.
func (b *PageBitmap) Add(pageID uint64, slot uint16) {
	slots, exists := b.Pages[pageID]
	if !exists {
		b.Pages[pageID] = []uint16{slot}
		return
	}

	// Find the correct position to keep the slice sorted
	idx := sort.Search(len(slots), func(i int) bool { return slots[i] >= slot })
	
	// If it already exists, do nothing
	if idx < len(slots) && slots[idx] == slot {
		return
	}

	// Insert into slice
	slots = append(slots, 0)
	copy(slots[idx+1:], slots[idx:])
	slots[idx] = slot
	b.Pages[pageID] = slots
}

// And performs an intersection of two PageBitmaps.
func (b *PageBitmap) And(other *PageBitmap) *PageBitmap {
	result := NewPageBitmap()
	for pageID, slots1 := range b.Pages {
		slots2, exists := other.Pages[pageID]
		if !exists {
			continue
		}

		var common []uint16
		i, j := 0, 0
		for i < len(slots1) && j < len(slots2) {
			if slots1[i] == slots2[j] {
				common = append(common, slots1[i])
				i++
				j++
			} else if slots1[i] < slots2[j] {
				i++
			} else {
				j++
			}
		}

		if len(common) > 0 {
			result.Pages[pageID] = common
		}
	}
	return result
}

// Or performs a union of two PageBitmaps.
func (b *PageBitmap) Or(other *PageBitmap) *PageBitmap {
	result := NewPageBitmap()

	// Copy all from b
	for pageID, slots := range b.Pages {
		cp := make([]uint16, len(slots))
		copy(cp, slots)
		result.Pages[pageID] = cp
	}

	// Merge with other
	for pageID, slots2 := range other.Pages {
		slots1, exists := result.Pages[pageID]
		if !exists {
			cp := make([]uint16, len(slots2))
			copy(cp, slots2)
			result.Pages[pageID] = cp
			continue
		}

		var union []uint16
		i, j := 0, 0
		for i < len(slots1) && j < len(slots2) {
			if slots1[i] == slots2[j] {
				union = append(union, slots1[i])
				i++
				j++
			} else if slots1[i] < slots2[j] {
				union = append(union, slots1[i])
				i++
			} else {
				union = append(union, slots2[j])
				j++
			}
		}
		
		for i < len(slots1) {
			union = append(union, slots1[i])
			i++
		}
		for j < len(slots2) {
			union = append(union, slots2[j])
			j++
		}
		result.Pages[pageID] = union
	}
	return result
}

// ToPositions returns a sorted list of all RowPositions in the bitmap.
func (b *PageBitmap) ToPositions() []RowPosition {
	var pageIDs []uint64
	for pageID := range b.Pages {
		pageIDs = append(pageIDs, pageID)
	}
	
	// Sort pageIDs to ensure deterministic order
	sort.Slice(pageIDs, func(i, j int) bool { return pageIDs[i] < pageIDs[j] })

	var pos []RowPosition
	for _, pageID := range pageIDs {
		for _, slot := range b.Pages[pageID] {
			pos = append(pos, RowPosition{PageID: pageID, Slot: slot})
		}
	}
	return pos
}
