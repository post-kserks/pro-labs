#include "logic/highlighter.hpp"

#include "utils/sql_keywords.hpp"
#include "utils/string_utils.hpp"

#include <cctype>
#include <vector>

namespace vaultdb::tui {

namespace {

bool isWord(unsigned char c) {
    return std::isalnum(c) != 0 || c == '_';
}

ftxui::Decorator styleForWord(const std::string& word, bool keyword) {
    using namespace ftxui;
    if (keyword) {
        return color(Color::Magenta) | bold;
    }
    if (utils::iequals(word, "NULL")) {
        return color(Color::GrayDark) | dim;
    }
    if (utils::iequals(word, "TRUE")) {
        return color(Color::Green) | bold;
    }
    if (utils::iequals(word, "FALSE")) {
        return color(Color::Red) | bold;
    }
    return color(Color::White);
}

} // namespace

ftxui::Element Highlighter::highlight(const std::string& sql) const {
    using namespace ftxui;
    Elements lines;
    for (const auto& line : utils::splitLines(sql)) {
        lines.push_back(highlightLine(line));
    }
    if (lines.empty()) {
        lines.push_back(text(""));
    }
    return vbox(std::move(lines));
}

ftxui::Element Highlighter::highlightLine(const std::string& line) const {
    using namespace ftxui;
    Elements parts;

    for (std::size_t i = 0; i < line.size();) {
        const char c = line[i];

        if (c == '-' && i + 1 < line.size() && line[i + 1] == '-') {
            parts.push_back(text(line.substr(i)) | color(Color::GrayDark) | dim);
            break;
        }

        if (c == '\'') {
            std::size_t end = i + 1;
            while (end < line.size()) {
                if (line[end] == '\\' && end + 1 < line.size()) {
                    end += 2;
                    continue;
                }
                if (line[end] == '\'') {
                    ++end;
                    break;
                }
                ++end;
            }
            parts.push_back(text(line.substr(i, end - i)) | color(Color::Yellow));
            i = end;
            continue;
        }

        if (std::isdigit(static_cast<unsigned char>(c)) != 0 ||
            (c == '-' && i + 1 < line.size() && std::isdigit(static_cast<unsigned char>(line[i + 1])) != 0)) {
            std::size_t end = i + 1;
            while (end < line.size() &&
                   (std::isdigit(static_cast<unsigned char>(line[end])) != 0 || line[end] == '.')) {
                ++end;
            }
            parts.push_back(text(line.substr(i, end - i)) | color(Color::Cyan));
            i = end;
            continue;
        }

        if (isWord(static_cast<unsigned char>(c))) {
            std::size_t end = i + 1;
            while (end < line.size() && isWord(static_cast<unsigned char>(line[end]))) {
                ++end;
            }
            const std::string word = line.substr(i, end - i);
            parts.push_back(text(word) | styleForWord(word, isKeyword(word)));
            i = end;
            continue;
        }

        if (std::string("=<>!,;()*").find(c) != std::string::npos) {
            parts.push_back(text(std::string(1, c)) | color(Color::GrayLight));
            ++i;
            continue;
        }

        unsigned char uc = static_cast<unsigned char>(c);
        std::size_t len = 1;
        if ((uc & 0x80) == 0) len = 1;
        else if ((uc & 0xE0) == 0xC0) len = 2;
        else if ((uc & 0xF0) == 0xE0) len = 3;
        else if ((uc & 0xF8) == 0xF0) len = 4;
        
        len = std::min(len, line.size() - i);
        parts.push_back(text(line.substr(i, len)));
        i += len;
    }

    return hbox(std::move(parts));
}

bool Highlighter::isKeyword(const std::string& word) const {
    return utils::kSQLKeywords.find(utils::toUpper(word)) != utils::kSQLKeywords.end();
}

} // namespace vaultdb::tui
