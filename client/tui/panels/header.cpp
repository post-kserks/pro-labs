#include "panels/header.hpp"

namespace vaultdb::tui {

ftxui::Element HeaderPanel::render(const std::string& activeDb,
                                   const std::string& host,
                                   int port,
                                   bool connected) const {
    using namespace ftxui;
    const std::string database_name = activeDb.empty() ? "<none>" : activeDb;
    const std::string status = connected ? "Connected" : "Disconnected";
    return hbox({
               text(" VaultDB TUI ") | bold | color(Color::Yellow),
               separator(),
               text(" Database: " + database_name + " ") | color(Color::Cyan),
               separator(),
               text(" " + status + ": " + host + ":" + std::to_string(port) + " ") |
                   color(connected ? Color::Green : Color::Red),
               filler(),
           }) |
           bgcolor(Color::Black);
}

} // namespace vaultdb::tui
