#include "panels/results.hpp"

#include "utils/csv_exporter.hpp"
#include "utils/string_utils.hpp"

#include <algorithm>

namespace vaultdb::tui {

namespace {
bool isCtrl(ftxui::Event event, char key) {
    const char upper = static_cast<char>(std::toupper(static_cast<unsigned char>(key)));
    if (upper < 'A' || upper > 'Z') return false;
    return event == ftxui::Event::Special(std::string(1, static_cast<char>(upper - 'A' + 1)));
}
} // namespace

void ResultsPanel::display(const vaultdb::Result& result, int durationMs, std::string title) {
    result_ = result;
    durationMs_ = durationMs;
    title_ = std::move(title);
    selectedRow_ = 0;
    rowOffset_ = 0;
    columnOffset_ = 0;
    detailOpen_ = false;
    filterOpen_ = false;
    filter_.clear();
}

bool ResultsPanel::handleEvent(ftxui::Event event, std::string& clipboard) {
    using ftxui::Event;

    if (filterOpen_) {
        if (event == Event::Escape) {
            filter_.clear();
            filterOpen_ = false;
            selectedRow_ = 0;
            rowOffset_ = 0;
            return true;
        }
        if (event == Event::Return) {
            filterOpen_ = false;
            return true;
        }
        if (event == Event::Backspace) {
            if (!filter_.empty()) {
                filter_.pop_back();
                selectedRow_ = 0;
                rowOffset_ = 0;
            }
            return true;
        }
        if (event.is_character()) {
            filter_ += event.character();
            selectedRow_ = 0;
            rowOffset_ = 0;
            return true;
        }
    }

    if (detailOpen_) {
        const auto rows = filteredRows();
        if (event == Event::Escape) {
            detailOpen_ = false;
            return true;
        }
        if (event == Event::ArrowLeft) {
            selectedRow_ = std::max(0, selectedRow_ - 1);
            return true;
        }
        if (event == Event::ArrowRight) {
            if (!rows.empty()) {
                selectedRow_ = std::min(static_cast<int>(rows.size()) - 1, selectedRow_ + 1);
            }
            return true;
        }
        return false;
    }

    if (event == Event::ArrowUp) {
        selectedRow_ = std::max(0, selectedRow_ - 1);
        rowOffset_ = std::min(rowOffset_, selectedRow_);
        return true;
    }
    if (event == Event::ArrowDown) {
        const auto rows = filteredRows();
        if (!rows.empty()) {
            selectedRow_ = std::min(static_cast<int>(rows.size()) - 1, selectedRow_ + 1);
            if (selectedRow_ >= rowOffset_ + 14) {
                rowOffset_ = selectedRow_ - 13;
            }
        }
        return true;
    }
    if (event == Event::PageUp || isCtrl(event, 'U')) {
        selectedRow_ = std::max(0, selectedRow_ - 14);
        rowOffset_ = std::max(0, rowOffset_ - 14);
        return true;
    }
    if (event == Event::PageDown || isCtrl(event, 'D')) {
        const auto rows = filteredRows();
        if (!rows.empty()) {
            selectedRow_ = std::min(static_cast<int>(rows.size()) - 1, selectedRow_ + 14);
            rowOffset_ = std::min(std::max(0, static_cast<int>(rows.size()) - 1), rowOffset_ + 14);
        }
        return true;
    }
    if (event == Event::Home || isCtrl(event, 'T')) {
        selectedRow_ = 0;
        rowOffset_ = 0;
        return true;
    }
    if (event == Event::End || isCtrl(event, 'B')) {
        const auto rows = filteredRows();
        if (!rows.empty()) {
            selectedRow_ = static_cast<int>(rows.size()) - 1;
            rowOffset_ = std::max(0, selectedRow_ - 13);
        }
        return true;
    }
    if (event == Event::Character('h')) {
        columnOffset_ = std::max(0, columnOffset_ - 1);
        return true;
    }
    if (event == Event::Character('l')) {
        columnOffset_ = std::min(static_cast<int>(result_.columns.size()), columnOffset_ + 1);
        return true;
    }
    if (event == Event::Character('e')) {
        const auto rows = filteredRows();
        if (!rows.empty()) {
            detailOpen_ = true;
        }
        return true;
    }
    if (event == Event::Character('c')) {
        const auto rows = filteredRows();
        if (!rows.empty() && selectedRow_ < static_cast<int>(rows.size())) {
            clipboard = utils::toCsvRow(rows[static_cast<std::size_t>(selectedRow_)]);
        }
        return true;
    }
    if (event == Event::Character('C')) {
        clipboard = utils::toCsv(result_.columns, filteredRows());
        return true;
    }
    if (event == Event::Character('f')) {
        filterOpen_ = true;
        return true;
    }
    return false;
}

ftxui::Element ResultsPanel::render(bool focused) const {
    using namespace ftxui;
    const std::string status = result_.isRows()
        ? std::to_string(filteredRows().size()) + " rows | " + std::to_string(durationMs_) + "ms"
        : std::to_string(durationMs_) + "ms";

    Element content;
    if (result_.isRows()) {
        content = tableView_.render(result_.columns, filteredRows(), selectedRow_, rowOffset_, columnOffset_);
    } else {
        content = renderMessage();
    }

    auto panel = window(text(" " + title_ + "  " + status + " ") | bold | color(Color::Yellow), content) |
                 color(focused ? Color::Blue : Color::GrayDark) | flex;

    if (filterOpen_) {
        panel = dbox({panel, renderFilterPopup() | clear_under | center});
    }

    if (detailOpen_) {
        const auto rows = filteredRows();
        if (!rows.empty() && selectedRow_ < static_cast<int>(rows.size())) {
            panel = dbox({
                panel,
                rowDetail_.render(result_.columns,
                                  rows[static_cast<std::size_t>(selectedRow_)],
                                  selectedRow_,
                                  static_cast<int>(rows.size())) |
                    clear_under | center,
            });
        }
    }
    return panel;
}

std::vector<std::vector<std::string>> ResultsPanel::filteredRows() const {
    if (filter_.empty()) {
        return result_.rows;
    }

    std::vector<std::vector<std::string>> rows;
    for (const auto& row : result_.rows) {
        bool match = false;
        for (const auto& cell : row) {
            if (utils::containsIgnoreCase(cell, filter_)) {
                match = true;
                break;
            }
        }
        if (match) {
            rows.push_back(row);
        }
    }
    return rows;
}

ftxui::Element ResultsPanel::renderMessage() const {
    using namespace ftxui;
    if (result_.isError()) {
        return vbox({
                   filler(),
                   text("[ERROR]") | bold | color(Color::Red),
                   paragraph("✗  " + result_.message) | color(Color::Red),
                   filler(),
               }) |
               center;
    }

    if (result_.isAffected()) {
        return vbox({
                   filler(),
                   text("[SUCCESS]") | bold | color(Color::Green),
                   text("Affected rows: " + std::to_string(result_.affected)) | color(Color::Green),
                   filler(),
               }) |
               center;
    }

    return vbox({
               filler(),
               text("[SUCCESS]") | bold | color(Color::Yellow),
               paragraph(result_.message.empty() ? "Command completed successfully." : result_.message),
               filler(),
           }) |
           center;
}

ftxui::Element ResultsPanel::renderFilterPopup() const {
    using namespace ftxui;
    const auto rows = filteredRows();
    return vbox({
               text("Filter results") | bold | color(Color::Yellow),
               separator(),
               text("> " + filter_ + "▌") | color(Color::Cyan),
               separator(),
               text("Showing " + std::to_string(rows.size()) + " of " + std::to_string(result_.rows.size()) + " rows") |
                   color(Color::GrayDark),
               text("[Esc] Clear filter   [Enter] Apply") | color(Color::GrayDark),
           }) |
           border | bgcolor(Color::Black) | size(WIDTH, GREATER_THAN, 42);
}

} // namespace vaultdb::tui
