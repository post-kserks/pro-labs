#include "panels/status_bar.hpp"

namespace pixeldb::tui {

namespace {

std::string shortcuts(FocusArea focus) {
    switch (focus) {
    case FocusArea::Navigator:
        return "[Tab]Switch [SPC]Expand [ENT]SwitchDB [F9/^N]NewDB [p]Preview [s]Schema [d]Drop [n]NewTable [r]Refresh";
    case FocusArea::Editor:
        return "[Tab]Switch/Complete [F5/^R]Run [^K]Clear [^Z]Undo [Up/Down]History";
    case FocusArea::Results:
        return "[Tab]Switch [Up/Down]Scroll [e]Expand [c]Copy [C]CopyAll [f]Filter [PgUp/^U]Page";
    }
    return "";
}

std::string modeName(FocusArea focus) {
    switch (focus) {
    case FocusArea::Navigator:
        return "NAVIGATOR";
    case FocusArea::Editor:
        return "EDITOR";
    case FocusArea::Results:
        return "RESULTS";
    }
    return "NORMAL";
}

} // namespace

ftxui::Element StatusBar::render(FocusArea focus,
                                 const std::string& activeDb,
                                 const std::string& message,
                                 bool connected) const {
    using namespace ftxui;
    if (!connected) {
        return hbox({
                   text("[DISCONNECTED] Reconnecting..."),
                   filler(),
                   text("Press R to retry"),
               }) |
               bgcolor(Color::Red) | color(Color::White);
    }

    return hbox({
               text(" " + shortcuts(focus) + " "),
               filler(),
               text(message.empty() ? "" : " " + message + " ") | color(Color::Yellow),
               separator(),
               text(" " + modeName(focus) + " ") | bold,
               separator(),
               text(" " + (activeDb.empty() ? "<none>" : activeDb) + " "),
           }) |
           bgcolor(Color::Black) | color(Color::White);
}

} // namespace pixeldb::tui
