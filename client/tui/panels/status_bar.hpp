#pragma once

#include <ftxui/dom/elements.hpp>

#include <string>

namespace vaultdb::tui {

enum class FocusArea {
    Navigator,
    Editor,
    Results,
};

class StatusBar {
public:
    ftxui::Element render(FocusArea focus,
                          const std::string& activeDb,
                          const std::string& message,
                          bool connected) const;
};

} // namespace vaultdb::tui
