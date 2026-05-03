#pragma once

#include <ftxui/dom/elements.hpp>

#include <string>
#include <vector>

namespace pixeldb::tui {

class AutocompletePopup {
public:
    ftxui::Element render(const std::vector<std::string>& suggestions, int selected) const;
};

} // namespace pixeldb::tui
