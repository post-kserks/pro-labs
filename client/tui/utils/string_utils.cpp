#include "utils/string_utils.hpp"

#include <algorithm>
#include <chrono>
#include <cctype>
#include <cstdlib>
#include <ctime>
#include <filesystem>
#include <sstream>

namespace vaultdb::tui::utils {

std::string trim(const std::string& value) {
    std::size_t begin = 0;
    while (begin < value.size() && std::isspace(static_cast<unsigned char>(value[begin])) != 0) {
        ++begin;
    }

    std::size_t end = value.size();
    while (end > begin && std::isspace(static_cast<unsigned char>(value[end - 1])) != 0) {
        --end;
    }
    return value.substr(begin, end - begin);
}

std::string toLower(std::string value) {
    std::transform(value.begin(), value.end(), value.begin(), [](unsigned char c) {
        return static_cast<char>(std::tolower(c));
    });
    return value;
}

std::string toUpper(std::string value) {
    std::transform(value.begin(), value.end(), value.begin(), [](unsigned char c) {
        return static_cast<char>(std::toupper(c));
    });
    return value;
}

bool iequals(const std::string& left, const std::string& right) {
    return toLower(left) == toLower(right);
}

bool startsWithIgnoreCase(const std::string& value, const std::string& prefix) {
    if (prefix.size() > value.size()) {
        return false;
    }
    return toLower(value.substr(0, prefix.size())) == toLower(prefix);
}

bool containsIgnoreCase(const std::string& value, const std::string& needle) {
    if (needle.empty()) {
        return true;
    }
    return toLower(value).find(toLower(needle)) != std::string::npos;
}

std::vector<std::string> splitLines(const std::string& value) {
    std::vector<std::string> lines;
    std::stringstream stream(value);
    std::string line;
    while (std::getline(stream, line)) {
        lines.push_back(line);
    }
    if (value.empty() || (!value.empty() && value.back() == '\n')) {
        lines.emplace_back();
    }
    return lines;
}

std::string clip(const std::string& value, std::size_t maxWidth) {
    if (maxWidth == 0) {
        return "";
    }
    if (value.size() <= maxWidth) {
        return value;
    }
    if (maxWidth <= 3) {
        return value.substr(0, maxWidth);
    }
    return value.substr(0, maxWidth - 3) + "...";
}

bool isIdentifier(const std::string& value) {
    if (value.empty()) {
        return false;
    }
    const auto validFirst = [](unsigned char c) {
        return std::isalpha(c) != 0 || c == '_';
    };
    const auto validNext = [](unsigned char c) {
        return std::isalnum(c) != 0 || c == '_';
    };
    if (!validFirst(static_cast<unsigned char>(value.front()))) {
        return false;
    }
    for (unsigned char c : value) {
        if (!validNext(c)) {
            return false;
        }
    }
    return true;
}

std::string ensureSemicolon(std::string query) {
    query = trim(query);
    if (!query.empty() && query.back() != ';') {
        query.push_back(';');
    }
    return query;
}

std::pair<std::size_t, std::size_t> wordBoundsAt(const std::string& value, std::size_t cursor) {
    cursor = std::min(cursor, value.size());
    const auto isWord = [](unsigned char c) {
        return std::isalnum(c) != 0 || c == '_';
    };

    std::size_t begin = cursor;
    while (begin > 0 && isWord(static_cast<unsigned char>(value[begin - 1]))) {
        --begin;
    }

    std::size_t end = cursor;
    while (end < value.size() && isWord(static_cast<unsigned char>(value[end]))) {
        ++end;
    }
    return {begin, end};
}

std::string homeDirectory() {
    if (const char* home = std::getenv("HOME"); home != nullptr && *home != '\0') {
        return home;
    }
    return ".";
}

std::string vaultdbDirectory() {
    const std::filesystem::path path = std::filesystem::path(homeDirectory()) / ".vaultdb";
    std::error_code ignored;
    std::filesystem::create_directories(path, ignored);
    return path.string();
}

std::string isoTimestampNow() {
    const auto now = std::chrono::system_clock::now();
    const std::time_t time = std::chrono::system_clock::to_time_t(now);
    std::tm tm {};
#if defined(_WIN32)
    gmtime_s(&tm, &time);
#else
    gmtime_r(&time, &tm);
#endif
    char buffer[32] {};
    std::strftime(buffer, sizeof(buffer), "%Y-%m-%dT%H:%M:%SZ", &tm);
    return buffer;
}

std::string shellEscapeSingleQuoted(const std::string& value) {
    std::string escaped;
    escaped.reserve(value.size() + 8);
    for (char c : value) {
        if (c == '\'') {
            escaped += "\\'";
        } else {
            escaped.push_back(c);
        }
    }
    return escaped;
}

} // namespace vaultdb::tui::utils
