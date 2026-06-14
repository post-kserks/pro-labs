#pragma once

#include <algorithm>
#include <cctype>
#include <string>

namespace vaultdb {

inline std::string trim(const std::string& input) {
    std::size_t begin = 0;
    while (begin < input.size() && std::isspace(static_cast<unsigned char>(input[begin])) != 0) {
        ++begin;
    }
    std::size_t end = input.size();
    while (end > begin && std::isspace(static_cast<unsigned char>(input[end - 1])) != 0) {
        --end;
    }
    return input.substr(begin, end - begin);
}

inline std::string toLower(std::string input) {
    std::transform(input.begin(), input.end(), input.begin(), [](unsigned char c) {
        return static_cast<char>(std::tolower(c));
    });
    return input;
}

inline std::string ensureSemicolon(std::string query) {
    if (!query.empty() && query.back() != ';') {
        query.push_back(';');
    }
    return query;
}

} // namespace vaultdb
