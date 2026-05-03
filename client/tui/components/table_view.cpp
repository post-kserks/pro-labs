#include "components/table_view.hpp"

#include "utils/string_utils.hpp"

#include <algorithm>

namespace pixeldb::tui {

namespace {

constexpr std::size_t kMaxColumnWidth = 30;

std::vector<std::size_t> widths(const std::vector<std::string>& columns,
                                const std::vector<std::vector<std::string>>& rows) {
    std::vector<std::size_t> result(columns.size(), 3);
    for (std::size_t i = 0; i < columns.size(); ++i) {
        result[i] = std::min(kMaxColumnWidth, std::max<std::size_t>(columns[i].size(), 3));
    }
    for (const auto& row : rows) {
        for (std::size_t i = 0; i < row.size() && i < result.size(); ++i) {
            result[i] = std::min(kMaxColumnWidth, std::max(result[i], row[i].size()));
        }
    }
    return result;
}

ftxui::Element styledCell(const std::string& value, std::size_t width, bool header = false) {
    using namespace ftxui;
    std::string visible = value == "NULL" ? "∅" : value;
    auto element = text(" " + utils::clip(visible, width) + " ") | size(WIDTH, EQUAL, static_cast<int>(width + 2));
    if (header) {
        return element | bold | color(Color::Yellow);
    }
    if (value == "NULL") {
        return element | color(Color::GrayDark) | dim;
    }
    if (utils::iequals(value, "true")) {
        return element | color(Color::Green);
    }
    if (utils::iequals(value, "false")) {
        return element | color(Color::Red);
    }
    return element;
}

} // namespace

ftxui::Element TableView::render(const std::vector<std::string>& columns,
                                 const std::vector<std::vector<std::string>>& rows,
                                 int selectedRow,
                                 int rowOffset,
                                 int columnOffset,
                                 int maxRows) const {
    using namespace ftxui;
    if (columns.empty()) {
        return text("No rows to display") | center | color(Color::GrayDark);
    }

    const auto columnWidths = widths(columns, rows);
    const int firstColumn = std::max(0, columnOffset);
    const int lastColumn = static_cast<int>(columns.size());

    Elements headerCells;
    for (int col = firstColumn; col < lastColumn; ++col) {
        headerCells.push_back(styledCell(columns[static_cast<std::size_t>(col)],
                                         columnWidths[static_cast<std::size_t>(col)],
                                         true));
        if (col + 1 < lastColumn) {
            headerCells.push_back(separator());
        }
    }

    Elements lines;
    lines.push_back(hbox(std::move(headerCells)));
    lines.push_back(separator());

    const int totalRows = static_cast<int>(rows.size());
    const int begin = std::max(0, std::min(rowOffset, totalRows));
    const int end = std::min(totalRows, begin + maxRows);
    for (int rowIndex = begin; rowIndex < end; ++rowIndex) {
        const auto& row = rows[static_cast<std::size_t>(rowIndex)];
        Elements rowCells;
        for (int col = firstColumn; col < lastColumn; ++col) {
            std::string value;
            if (col < static_cast<int>(row.size())) {
                value = row[static_cast<std::size_t>(col)];
            }
            rowCells.push_back(styledCell(value, columnWidths[static_cast<std::size_t>(col)]));
            if (col + 1 < lastColumn) {
                rowCells.push_back(separator());
            }
        }
        auto line = hbox(std::move(rowCells));
        if (rowIndex == selectedRow) {
            line = line | inverted;
        }
        lines.push_back(line);
    }

    if (rows.empty()) {
        lines.push_back(text(" Empty result set ") | color(Color::GrayDark));
    }

    const std::string scroll = rows.empty()
        ? ""
        : " rows " + std::to_string(std::min(totalRows, begin + 1)) + "-" + std::to_string(end) + "/" + std::to_string(totalRows);
    lines.push_back(separator());
    lines.push_back(text(scroll + "  Shift+Left/Right: columns  e: details  f: filter") | color(Color::GrayDark));
    return vbox(std::move(lines));
}

} // namespace pixeldb::tui
