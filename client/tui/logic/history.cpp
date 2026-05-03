#include "logic/history.hpp"

#include "utils/string_utils.hpp"

#include <filesystem>
#include <fstream>
#include <regex>
#include <sstream>

namespace pixeldb::tui {

namespace {

std::filesystem::path historyPath() {
    return std::filesystem::path(utils::pixeldbDirectory()) / "history.json";
}

std::string escapeJson(const std::string& value) {
    std::string out;
    out.reserve(value.size());
    for (char c : value) {
        switch (c) {
        case '\\':
            out += "\\\\";
            break;
        case '"':
            out += "\\\"";
            break;
        case '\n':
            out += "\\n";
            break;
        case '\r':
            out += "\\r";
            break;
        case '\t':
            out += "\\t";
            break;
        default:
            out.push_back(c);
            break;
        }
    }
    return out;
}

std::string unescapeJson(const std::string& value) {
    std::string out;
    out.reserve(value.size());
    for (std::size_t i = 0; i < value.size(); ++i) {
        if (value[i] != '\\' || i + 1 >= value.size()) {
            out.push_back(value[i]);
            continue;
        }
        const char next = value[++i];
        switch (next) {
        case 'n':
            out.push_back('\n');
            break;
        case 'r':
            out.push_back('\r');
            break;
        case 't':
            out.push_back('\t');
            break;
        default:
            out.push_back(next);
            break;
        }
    }
    return out;
}

std::string fieldString(const std::string& object, const std::string& key) {
    const std::regex pattern("\"" + key + "\"\\s*:\\s*\"((?:\\\\.|[^\"])*)\"");
    std::smatch match;
    if (std::regex_search(object, match, pattern)) {
        return unescapeJson(match[1].str());
    }
    return "";
}

bool fieldBool(const std::string& object, const std::string& key) {
    const std::regex pattern("\"" + key + "\"\\s*:\\s*(true|false)");
    std::smatch match;
    return std::regex_search(object, match, pattern) && match[1].str() == "true";
}

int fieldInt(const std::string& object, const std::string& key) {
    const std::regex pattern("\"" + key + "\"\\s*:\\s*([0-9]+)");
    std::smatch match;
    if (std::regex_search(object, match, pattern)) {
        return std::stoi(match[1].str());
    }
    return 0;
}

} // namespace

History::History(std::size_t maxEntries) : maxEntries_(maxEntries) {}

void History::load() {
    entries_.clear();

    std::ifstream in(historyPath());
    if (!in) {
        save();
        return;
    }

    std::ostringstream buffer;
    buffer << in.rdbuf();
    const std::string json = buffer.str();

    const std::regex objectPattern("\\{[^\\}]*\\}");
    auto begin = std::sregex_iterator(json.begin(), json.end(), objectPattern);
    auto end = std::sregex_iterator();
    for (auto it = begin; it != end; ++it) {
        const std::string object = it->str();
        HistoryEntry entry;
        entry.timestamp = fieldString(object, "timestamp");
        entry.query = fieldString(object, "query");
        entry.success = fieldBool(object, "success");
        entry.durationMs = fieldInt(object, "duration_ms");
        if (!entry.query.empty()) {
            entries_.push_back(entry);
        }
    }
}

void History::save() const {
    std::ofstream out(historyPath());
    out << "[\n";
    for (std::size_t i = 0; i < entries_.size(); ++i) {
        const auto& entry = entries_[i];
        out << "  {\n"
            << "    \"timestamp\": \"" << escapeJson(entry.timestamp) << "\",\n"
            << "    \"query\": \"" << escapeJson(entry.query) << "\",\n"
            << "    \"success\": " << (entry.success ? "true" : "false") << ",\n"
            << "    \"duration_ms\": " << entry.durationMs << "\n"
            << "  }";
        if (i + 1 < entries_.size()) {
            out << ',';
        }
        out << '\n';
    }
    out << "]\n";
}

void History::add(std::string query, bool success, int durationMs) {
    query = utils::trim(query);
    if (query.empty()) {
        return;
    }

    entries_.insert(entries_.begin(), HistoryEntry{utils::isoTimestampNow(), std::move(query), success, durationMs});
    if (entries_.size() > maxEntries_) {
        entries_.resize(maxEntries_);
    }
    save();
}

void History::remove(std::size_t index) {
    if (index >= entries_.size()) {
        return;
    }
    entries_.erase(entries_.begin() + static_cast<std::ptrdiff_t>(index));
    save();
}

void History::clear() {
    entries_.clear();
    save();
}

std::vector<std::size_t> History::filter(const std::string& needle) const {
    std::vector<std::size_t> indices;
    for (std::size_t i = 0; i < entries_.size(); ++i) {
        if (utils::containsIgnoreCase(entries_[i].query, needle)) {
            indices.push_back(i);
        }
    }
    return indices;
}

} // namespace pixeldb::tui
