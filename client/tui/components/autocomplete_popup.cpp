#include "components/autocomplete_popup.hpp"

#include <algorithm>

namespace vaultdb::tui {

ftxui::Element AutocompletePopup::render(const std::vector<std::string>& suggestions, int selected) const {
    using namespace ftxui;
    if (suggestions.empty()) {
        return text("");
    }

    const int maxVisible = 8;
    const int total = static_cast<int>(suggestions.size());
    const int count = std::min(total, maxVisible);
    
    int start = 0;
    if (selected >= maxVisible) {
        start = selected - maxVisible + 1;
    }

    Elements rows;
    for (int i = 0; i < count; ++i) {
        int index = start + i;
        if (index >= total) break;
        
        auto row = text((index == selected ? "> " : "  ") + suggestions[static_cast<std::size_t>(index)]);
        if (index == selected) {
            row = row | inverted;
        }
        rows.push_back(row);
    }
    
    if (total > maxVisible) {
        rows.push_back(separator());
        rows.push_back(text(" " + std::to_string(selected + 1) + "/" + std::to_string(total)) | 
                      color(Color::GrayDark) | hcenter);
    }

    return vbox(std::move(rows)) | border | color(Color::Cyan) | bgcolor(Color::Black);
}

} // namespace vaultdb::tui
