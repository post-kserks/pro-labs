#include "screens/main_screen.hpp"

namespace pixeldb::tui {

ftxui::Element MainScreenFrame::render(ftxui::Element header,
                                       ftxui::Element navigator,
                                       ftxui::Element editor,
                                       ftxui::Element results,
                                       ftxui::Element status) const {
    using namespace ftxui;
    return vbox({
               header | size(HEIGHT, EQUAL, 1),
               separator(),
               hbox({
                   navigator,
                   separator(),
                   vbox({
                       editor | size(HEIGHT, EQUAL, 13),
                       separator(),
                       results | flex,
                   }) | flex,
               }) | flex,
               separator(),
               status | size(HEIGHT, EQUAL, 1),
           }) |
           bgcolor(Color::Black);
}

} // namespace pixeldb::tui
