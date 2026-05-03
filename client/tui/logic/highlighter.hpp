#pragma once

#include <ftxui/dom/elements.hpp>

#include <string>

namespace pixeldb::tui {

class Highlighter {
public:
    ftxui::Element highlight(const std::string& sql) const;
    ftxui::Element highlightLine(const std::string& line) const;

private:
    bool isKeyword(const std::string& word) const;
};

} // namespace pixeldb::tui
