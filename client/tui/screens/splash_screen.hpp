#pragma once

#include <ftxui/dom/elements.hpp>

#include <string>

namespace pixeldb::tui {

class SplashScreen {
public:
    ftxui::Element render(const std::string& host, int port) const;
};

} // namespace pixeldb::tui
