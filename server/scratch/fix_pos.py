import re

with open("internal/core/storage/page_engine_io.go", "r") as f:
    content = f.read()

# 1. Fix mutateRows
old_mutate_pos = """	t.posMu.Lock()
	if t.posDirectoryValid && t.posDirectory != nil {
		for i, idx := range indices {
			if i < len(newSlots) && idx >= 0 && idx < len(t.posDirectory) {
				t.posDirectory[idx] = newSlots[i]
			}
		}
		if len(newSlots) > 0 {
			t.posDirectory = t.posDirectory[:len(t.posDirectory)-len(newSlots)]
			t.rowCount.Add(int64(-len(newSlots)))
		}
	}
	t.posMu.Unlock()"""

new_mutate_pos = """	t.posMu.Lock()
	if t.posDirectoryValid && t.posDirectory != nil {
		if isDelete {
			var newPos []PageSlot
			toRemove := make(map[int]bool, len(indices))
			for _, idx := range indices {
				toRemove[idx] = true
			}
			for i, p := range t.posDirectory {
				if !toRemove[i] {
					newPos = append(newPos, p)
				}
			}
			t.posDirectory = newPos
			t.rowCount.Add(int64(-len(indices)))
		} else {
			for i, idx := range indices {
				if i < len(newSlots) && idx >= 0 && idx < len(t.posDirectory) {
					t.posDirectory[idx] = newSlots[i]
				}
			}
		}
	}
	t.posMu.Unlock()"""

content = content.replace(old_mutate_pos, new_mutate_pos)


# 2. Fix DeleteRowsVM
old_delete_pos = """	if affected > 0 {
		e.updateIndexesOnDelete(dbName, tableName, indices)
	}

	mutateLockReleased = true
	t.mu.Unlock()"""

new_delete_pos = """	t.posMu.Lock()
	if t.posDirectoryValid && t.posDirectory != nil {
		var newPos []PageSlot
		toRemove := make(map[int]bool, len(indices))
		for _, idx := range indices {
			toRemove[idx] = true
		}
		for i, p := range t.posDirectory {
			if !toRemove[i] {
				newPos = append(newPos, p)
			}
		}
		t.posDirectory = newPos
		t.rowCount.Add(int64(-len(indices)))
	}
	t.posMu.Unlock()

	if affected > 0 {
		e.updateIndexesOnDelete(dbName, tableName, indices)
	}

	mutateLockReleased = true
	t.mu.Unlock()"""

content = content.replace(old_delete_pos, new_delete_pos)

with open("internal/core/storage/page_engine_io.go", "w") as f:
    f.write(content)
