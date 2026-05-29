#include "logic/autocomplete.hpp"

#include "utils/string_utils.hpp"

#include <algorithm>
#include <unordered_set>

namespace vaultdb::tui {

namespace {

const std::vector<std::string> kKeywords = {
    "SELECT", "FROM", "WHERE", "AND", "OR", "NOT", "INSERT", "INTO", "VALUES",
    "UPDATE", "SET", "DELETE", "CREATE", "DROP", "DATABASE", "TABLE", "USE",
    "SHOW", "DATABASES", "TABLES", "DESCRIBE", "LIMIT", "NULL", "TRUE", "FALSE",
};

bool lastTokenIsOneOf(const std::string& before, const std::unordered_set<std::string>& tokens) {
    const std::string flattened = utils::toLower(before);
    std::size_t end = flattened.find_last_not_of(" \t\r\n");
    if (end == std::string::npos) {
        return false;
    }
    std::size_t begin = flattened.find_last_of(" \t\r\n", end);
    begin = begin == std::string::npos ? 0 : begin + 1;
    return tokens.find(utils::toUpper(flattened.substr(begin, end - begin + 1))) != tokens.end();
}

std::vector<std::string> filterByPrefix(const std::vector<std::string>& values, const std::string& prefix) {
    std::vector<std::string> out;
    for (const auto& value : values) {
        if (prefix.empty() || utils::startsWithIgnoreCase(value, prefix)) {
            out.push_back(value);
        }
    }
    std::sort(out.begin(), out.end());
    out.erase(std::unique(out.begin(), out.end()), out.end());
    return out;
}

} // namespace

std::vector<std::string> Autocomplete::suggestions(const std::string& sql,
                                                   std::size_t cursor,
                                                   const CompletionContext& context) const {
    if (!context.enabled) {
        return {};
    }

    cursor = std::min(cursor, sql.size());
    const auto [begin, end] = utils::wordBoundsAt(sql, cursor);
    const std::string prefix = sql.substr(begin, cursor - begin);
    const std::string beforeWord = sql.substr(0, begin);
    const std::string beforeCursor = sql.substr(0, cursor);

    if (lastTokenIsOneOf(beforeWord, {"FROM", "INTO", "UPDATE", "TABLE", "DESCRIBE"})) {
        return filterByPrefix(context.tables, prefix);
    }

    if (utils::containsIgnoreCase(beforeCursor, " where ") ||
        lastTokenIsOneOf(beforeWord, {"WHERE", "AND", "OR", "SET"})) {
        auto columns = filterByPrefix(context.columns, prefix);
        if (!columns.empty()) {
            return columns;
        }
    }

    return keywordSuggestions(prefix);
}

std::string Autocomplete::apply(const std::string& sql,
                                std::size_t cursor,
                                const std::string& completion,
                                std::size_t& newCursor) const {
    cursor = std::min(cursor, sql.size());
    const auto [begin, end] = utils::wordBoundsAt(sql, cursor);
    std::string out = sql;
    out.replace(begin, end - begin, completion);
    newCursor = begin + completion.size();
    return out;
}

std::vector<std::string> Autocomplete::keywordSuggestions(const std::string& prefix) const {
    return filterByPrefix(kKeywords, prefix);
}

} // namespace vaultdb::tui
