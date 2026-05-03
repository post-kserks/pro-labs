#pragma once

#include <ftxui/dom/elements.hpp>

namespace pixeldb::tui {

class MainScreenFrame {
public:
    ftxui::Element render(ftxui::Element header,
                          ftxui::Element navigator,
                          ftxui::Element editor,
                          ftxui::Element results,
                          ftxui::Element status) const;
};

} // namespace pixeldb::tui
