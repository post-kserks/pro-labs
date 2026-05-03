#include "screens/connection_error.hpp"

namespace pixeldb::tui {

ftxui::Element ConnectionErrorScreen::render(const std::string& host, int port, const std::string& message) const {
    using namespace ftxui;
    return vbox({
               filler(),
               vbox({
                   text("[CONNECTION FAILED]") | bold | color(Color::Red),
                   separator(),
                   paragraph("Could not reach the server at " + host + ":" + std::to_string(port)),
                   paragraph(message.empty() ? "Make sure the server is running: ./run.sh 127.0.0.1 5432" : message),
                   separator(),
                   text("[R] Retry    [Q] Quit") | color(Color::Yellow),
               }) | border | size(WIDTH, GREATER_THAN, 48) | center,
               filler(),
           }) |
           bgcolor(Color::Black);
}

} // namespace pixeldb::tui
