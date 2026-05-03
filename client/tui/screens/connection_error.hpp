#pragma once

#include <ftxui/component/event.hpp>
#include <ftxui/dom/elements.hpp>

#include <string>

namespace pixeldb::tui {

class ConnectionErrorScreen {
public:
    ftxui::Element render(const std::string& host, int port, const std::string& message) const;
};

} // namespace pixeldb::tui
