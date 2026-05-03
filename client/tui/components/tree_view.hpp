#pragma once

#include <ftxui/dom/elements.hpp>

#include <string>
#include <vector>

namespace pixeldb::tui {

struct TreeLine {
    std::string text;
    bool selected = false;
    bool active = false;
    bool database = false;
};

class TreeView {
public:
    ftxui::Element render(const std::vector<TreeLine>& lines) const;
};

} // namespace pixeldb::tui
