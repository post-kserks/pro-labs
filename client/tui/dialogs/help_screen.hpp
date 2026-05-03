#pragma once

#include <ftxui/component/event.hpp>
#include <ftxui/dom/elements.hpp>

namespace vaultdb::tui {

class HelpScreen {
public:
    void open();
    bool isOpen() const { return open_; }
    bool handleEvent(ftxui::Event event);
    ftxui::Element render() const;

private:
    bool open_ = false;
};

} // namespace vaultdb::tui
