#include "dialogs/help_screen.hpp"

namespace vaultdb::tui {

void HelpScreen::open() {
    open_ = true;
}

bool HelpScreen::handleEvent(ftxui::Event event) {
    using ftxui::Event;
    if (!open_) {
        return false;
    }
    if (event == Event::Escape || event == Event::F1) {
        open_ = false;
        return true;
    }
    return true;
}

ftxui::Element HelpScreen::render() const {
    using namespace ftxui;
    return vbox({
               hbox({
                   text("VAULT DB HELP") | bold | color(Color::Yellow),
                   filler(),
                   text("[Esc] Close") | color(Color::GrayDark),
               }),
               separator(),
               hbox({
                   vbox({
                       text("NAVIGATION") | bold | color(Color::Cyan),
                       text("Tab       Switch panel"),
                       text("Up/Down   Move in panel"),
                       text("Space     Expand/Collapse"),
                       text("Enter     Select"),
                       text("Esc       Cancel / Back"),
                   }) | flex,
                   separator(),
                   vbox({
                       text("SQL SHORTCUTS") | bold | color(Color::Cyan),
                       text("F5 / ^R / Ctrl+Enter  Execute"),
                       text("Tab              Autocomplete"),
                       text("Ctrl+K           Clear editor"),
                       text("Ctrl+Z           Undo"),
                       text("Up(empty)        History prev"),
                   }) | flex,
               }),
               separator(),
               hbox({
                   vbox({
                       text("GLOBAL") | bold | color(Color::Cyan),
                       text("F1        Help"),
                       text("F2        History"),
                       text("F9        New Database"),
                       text("F10/^Q    Quit"),
                   }) | flex,
                   separator(),
                   vbox({
                       text("RESULTS") | bold | color(Color::Cyan),
                       text("e         Expand row"),
                       text("c/C       Copy row/all CSV"),
                       text("f         Filter results"),
                       text("h/l       Horizontal scroll"),
                   }) | flex,
               }),
               separator(),
               text("SQL EXAMPLES") | bold | color(Color::Cyan),
               text("SELECT * FROM employees WHERE salary > 5;"),
               text("INSERT INTO employees (id, name) VALUES (1, 'Smith');"),
               text("UPDATE employees SET salary = 11 WHERE id = 1;"),
               text("DELETE FROM employees WHERE active = FALSE;"),
               text("CREATE TABLE items (id INT, name VARCHAR(50));"),
           }) |
           border | bgcolor(Color::Black) | size(WIDTH, GREATER_THAN, 68);
}

} // namespace vaultdb::tui
