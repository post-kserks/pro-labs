#pragma once

#include <ftxui/dom/elements.hpp>

#include <string>

namespace vaultdb::tui {

class SplashScreen {
public:
    ftxui::Element render(const std::string& host, int port) const;
};

} // namespace vaultdb::tui
