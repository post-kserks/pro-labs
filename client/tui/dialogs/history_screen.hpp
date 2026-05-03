#pragma once

#include "logic/history.hpp"

#include <ftxui/component/event.hpp>
#include <ftxui/dom/elements.hpp>

#include <string>

namespace pixeldb::tui {

class HistoryScreen {
public:
    void open();
    bool isOpen() const { return open_; }
    bool handleEvent(ftxui::Event event, History& history);
    ftxui::Element render(const History& history) const;

    bool consumeLoadedQuery(std::string& query);

private:
    bool open_ = false;
    std::string filter_;
    int selected_ = 0;
    std::string loadedQuery_;

    void clampSelection(const History& history);
};

} // namespace pixeldb::tui
