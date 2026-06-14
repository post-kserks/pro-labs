#pragma once

#include <ftxui/dom/elements.hpp>
#include <ftxui/screen/screen.hpp>

#include <cctype>

namespace vaultdb::tui::utils {

inline bool isCtrl(ftxui::Event event, char key) {
    const char upper = static_cast<char>(std::toupper(static_cast<unsigned char>(key)));
    if (upper < 'A' || upper > 'Z') return false;
    return event == ftxui::Event::Special(std::string(1, static_cast<char>(upper - 'A' + 1)));
}

} // namespace vaultdb::tui::utils
