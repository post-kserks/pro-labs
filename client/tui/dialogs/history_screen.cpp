#include "dialogs/history_screen.hpp"

#include "utils/event_utils.hpp"
#include "utils/string_utils.hpp"

#include <algorithm>
#include <cctype>

namespace vaultdb::tui {

using utils::isCtrl;

void HistoryScreen::open() {
    open_ = true;
    selected_ = 0;
    filter_.clear();
    loadedQuery_.clear();
}

bool HistoryScreen::handleEvent(ftxui::Event event, History& history) {
    using ftxui::Event;
    if (!open_) {
        return false;
    }
    const auto indices = history.filter(filter_);
    if (event == Event::Escape || event == Event::F2) {
        open_ = false;
        return true;
    }
    if (event == Event::ArrowUp) {
        selected_ = std::max(0, selected_ - 1);
        return true;
    }
    if (event == Event::ArrowDown) {
        if (!indices.empty()) {
            selected_ = std::min(static_cast<int>(indices.size()) - 1, selected_ + 1);
        }
        return true;
    }
    if (event == Event::Return) {
        if (!indices.empty() && selected_ >= 0 && selected_ < static_cast<int>(indices.size())) {
            loadedQuery_ = history.entries()[indices[static_cast<std::size_t>(selected_)]].query;
            open_ = false;
        }
        return true;
    }
    if (event == Event::Delete) {
        if (!indices.empty() && selected_ >= 0 && selected_ < static_cast<int>(indices.size())) {
            history.remove(indices[static_cast<std::size_t>(selected_)]);
            clampSelection(history);
        }
        return true;
    }
    if (isCtrl(event, 'D')) {
        history.clear();
        selected_ = 0;
        return true;
    }
    if (event == Event::Backspace) {
        if (!filter_.empty()) {
            filter_.pop_back();
            selected_ = 0;
        }
        return true;
    }
    if (event.is_character()) {
        filter_ += event.character();
        selected_ = 0;
        return true;
    }
    return true;
}

ftxui::Element HistoryScreen::render(const History& history) const {
    using namespace ftxui;
    const auto indices = history.filter(filter_);
    Elements rows;
    rows.push_back(hbox({
        text("QUERY HISTORY") | bold | color(Color::Yellow),
        filler(),
        text("[Esc] Close") | color(Color::GrayDark),
    }));
    rows.push_back(separator());
    rows.push_back(text("Filter: " + filter_ + "▌") | color(Color::Cyan));
    rows.push_back(separator());

    const int maxRows = 12;
    const int begin = std::max(0, selected_ - maxRows + 1);
    const int end = std::min(static_cast<int>(indices.size()), begin + maxRows);
    for (int i = begin; i < end; ++i) {
        const auto& entry = history.entries()[indices[static_cast<std::size_t>(i)]];
        std::string time = entry.timestamp.size() >= 19 ? entry.timestamp.substr(11, 8) : "--:--:--";
        std::string line = std::string(entry.success ? "[✓] " : "[✗] ") + time + "  " +
                           utils::clip(utils::trim(entry.query), 58) +
                           (entry.success ? "" : "  ERROR");
        auto element = text(line) | color(entry.success ? Color::Green : Color::Red);
        if (i == selected_) {
            element = element | inverted;
        }
        rows.push_back(element);
    }
    if (indices.empty()) {
        rows.push_back(text("No history entries") | color(Color::GrayDark));
    }

    rows.push_back(filler());
    rows.push_back(separator());
    rows.push_back(text("[Enter] Load into editor   [Del] Delete entry   [Ctrl+D] Clear history   " +
                        std::to_string(history.entries().size()) + " entries") |
                   color(Color::GrayDark));
    return vbox(std::move(rows)) | border | bgcolor(Color::Black) | size(WIDTH, GREATER_THAN, 76);
}

bool HistoryScreen::consumeLoadedQuery(std::string& query) {
    if (loadedQuery_.empty()) {
        return false;
    }
    query = loadedQuery_;
    loadedQuery_.clear();
    return true;
}

void HistoryScreen::clampSelection(const History& history) {
    const int count = static_cast<int>(history.filter(filter_).size());
    if (count <= 0) {
        selected_ = 0;
    } else {
        selected_ = std::max(0, std::min(selected_, count - 1));
    }
}

} // namespace vaultdb::tui
