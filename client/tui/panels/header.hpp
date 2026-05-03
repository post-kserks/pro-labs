#pragma once

#include <ftxui/dom/elements.hpp>

#include <string>

namespace pixeldb::tui {

class HeaderPanel {
public:
    ftxui::Element render(const std::string& activeDb,
                          const std::string& host,
                          int port,
                          bool connected) const;
};

} // namespace pixeldb::tui
