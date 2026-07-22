import re

with open("internal/core/storage/page_engine_io.go", "r") as f:
    content = f.read()

# Fix UpdateRowsVM (which also has the old mutateRows logic inserted!)
old_update_pos = """	t.posMu.Lock()
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

new_update_pos = """	t.posMu.Lock()
	if t.posDirectoryValid && t.posDirectory != nil {
		for i, idx := range indices {
			if i < len(newSlots) && idx >= 0 && idx < len(t.posDirectory) {
				t.posDirectory[idx] = newSlots[i]
			}
		}
	}
	t.posMu.Unlock()"""

# I need to ONLY replace the SECOND occurrence (which is inside UpdateRowsVM)
# But wait, my previous python script replaced both!
# Let me just check the file content
content_parts = content.split("func (e *PageStorageEngine) UpdateRowsVM")

if len(content_parts) == 2:
    content_parts[1] = content_parts[1].replace(old_update_pos, new_update_pos)
    content = "func (e *PageStorageEngine) UpdateRowsVM".join(content_parts)

with open("internal/core/storage/page_engine_io.go", "w") as f:
    f.write(content)
