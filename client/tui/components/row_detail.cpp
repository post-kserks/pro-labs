#include "components/row_detail.hpp"

#include "utils/string_utils.hpp"

namespace pixeldb::tui {

ftxui::Element RowDetail::render(const std::vector<std::string>& columns,
                                 const std::vector<std::string>& row,
                                 int rowIndex,
                                 int rowCount) const {
    using namespace ftxui;
    Elements lines;
    lines.push_back(text("ROW DETAILS  (row " + std::to_string(rowIndex + 1) + " of " + std::to_string(rowCount) + ")") |
                    bold | color(Color::Yellow));
    lines.push_back(separator());
    for (std::size_t i = 0; i < columns.size(); ++i) {
        const std::string value = i < row.size() ? row[i] : "";
        lines.push_back(hbox({
            text(utils::clip(columns[i], 14)) | size(WIDTH, EQUAL, 16) | color(Color::Cyan),
            separator(),
            paragraph(value == "NULL" ? "∅" : value) | flex,
        }));
    }
    lines.push_back(separator());
    lines.push_back(text("[Left/Right] Prev/Next   [Esc] Close") | color(Color::GrayDark));
    return vbox(std::move(lines)) | border | size(WIDTH, GREATER_THAN, 44) | bgcolor(Color::Black);
}

} // namespace pixeldb::tui
