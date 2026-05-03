#include "components/tree_view.hpp"

namespace pixeldb::tui {

ftxui::Element TreeView::render(const std::vector<TreeLine>& lines) const {
    using namespace ftxui;
    Elements rows;
    rows.push_back(text("DATABASES") | bold | color(Color::Yellow));
    rows.push_back(separator());

    if (lines.empty()) {
        rows.push_back(text("No databases found") | color(Color::GrayDark));
    }

    for (const auto& line : lines) {
        auto row = text(line.text);
        if (line.database) {
            row = row | color(Color::Cyan);
        }
        if (line.active) {
            row = row | bold | color(Color::Yellow);
        }
        if (line.selected) {
            row = row | inverted;
        }
        rows.push_back(row);
    }

    rows.push_back(filler());
    rows.push_back(separator());
    rows.push_back(text("[SPC]Expand [ENT]Select") | color(Color::GrayDark));
    rows.push_back(text("[p]Preview [s]Schema [d]Drop") | color(Color::GrayDark));
    return vbox(std::move(rows));
}

} // namespace pixeldb::tui
