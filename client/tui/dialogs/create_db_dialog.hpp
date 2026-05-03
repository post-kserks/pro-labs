#pragma once

#include <ftxui/component/event.hpp>
#include <ftxui/dom/elements.hpp>

#include <string>

namespace pixeldb::tui {

class CreateDbDialog {
public:
    void open();
    bool isOpen() const { return open_; }
    bool handleEvent(ftxui::Event event);
    ftxui::Element render() const;

    bool consumeSubmitted();
    bool consumeCanceled();
    std::string name() const { return name_; }

private:
    bool open_ = false;
    bool submitted_ = false;
    bool canceled_ = false;
    std::string name_;
};

} // namespace pixeldb::tui
