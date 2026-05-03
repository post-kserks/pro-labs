#include "screens/splash_screen.hpp"

namespace pixeldb::tui {

ftxui::Element SplashScreen::render(const std::string& host, int port) const {
    using namespace ftxui;
    return vbox({
               filler(),
               vbox({
                   text("██████╗ ██╗██╗  ██╗███████╗██╗") | color(Color::Yellow),
                   text("██╔══██╗██║╚██╗██╔╝██╔════╝██║") | color(Color::Yellow),
                   text("██████╔╝██║ ╚███╔╝ █████╗  ██║") | color(Color::Yellow),
                   text("██╔═══╝ ██║ ██╔██╗ ██╔══╝  ██║") | color(Color::Yellow),
                   text("██║     ██║██╔╝ ██╗███████╗███████╗") | color(Color::Yellow),
                   text("╚═╝     ╚═╝╚═╝  ╚═╝╚══════╝╚══════╝") | color(Color::Yellow),
                   separator(),
                   text("A DATABASE FROM ANOTHER DIMENSION") | bold | color(Color::Cyan),
                   text("Connecting to " + host + ":" + std::to_string(port)) | color(Color::GrayLight),
                   text("[▓▓▓▓▓▓▓▓▓▓▓▓░░░░░░░░░░] Connecting...") | color(Color::Green),
               }) | border | center,
               filler(),
           }) |
           bgcolor(Color::Black);
}

} // namespace pixeldb::tui
