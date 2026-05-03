#pragma once

#include <cstddef>
#include <string>
#include <vector>

namespace pixeldb::tui {

struct HistoryEntry {
    std::string timestamp;
    std::string query;
    bool success = false;
    int durationMs = 0;
};

class History {
public:
    explicit History(std::size_t maxEntries = 500);

    void load();
    void save() const;
    void add(std::string query, bool success, int durationMs);
    void remove(std::size_t index);
    void clear();

    const std::vector<HistoryEntry>& entries() const { return entries_; }
    std::vector<std::size_t> filter(const std::string& needle) const;

private:
    std::size_t maxEntries_;
    std::vector<HistoryEntry> entries_;
};

} // namespace pixeldb::tui
