#include "dialogs/create_db_dialog.hpp"

#include "utils/string_utils.hpp"

#include <cctype>

namespace pixeldb::tui {

void CreateDbDialog::open() {
    open_ = true;
    submitted_ = false;
    canceled_ = false;
    name_.clear();
}

bool CreateDbDialog::handleEvent(ftxui::Event event) {
    using ftxui::Event;
    if (!open_) {
        return false;
    }
    if (event == Event::Escape) {
        open_ = false;
        canceled_ = true;
        return true;
    }
    if (event == Event::Backspace) {
        if (!name_.empty()) {
            name_.pop_back();
        }
        return true;
    }
    if (event == Event::Return) {
        if (utils::isIdentifier(name_)) {
            open_ = false;
            submitted_ = true;
        }
        return true;
    }
    if (event.is_character()) {
        const std::string ch = event.character();
        if (ch.size() == 1 && (std::isalnum(static_cast<unsigned char>(ch[0])) != 0 || ch[0] == '_')) {
            name_ += ch;
        }
        return true;
    }
    return true;
}

ftxui::Element CreateDbDialog::render() const {
    using namespace ftxui;
    const bool valid = utils::isIdentifier(name_);
    return vbox({
               text("CREATE A NEW DATABASE") | bold | color(Color::Yellow),
               separator(),
               text("Database name:"),
               text("> " + name_ + "▌") | color(Color::Cyan),
               separator(),
               text(valid ? "[Enter] Create   [Esc] Cancel" : "Use letters, digits and underscore") |
                   color(valid ? Color::GrayDark : Color::Red),
           }) |
           border | bgcolor(Color::Black) | size(WIDTH, GREATER_THAN, 38);
}

bool CreateDbDialog::consumeSubmitted() {
    const bool value = submitted_;
    submitted_ = false;
    return value;
}

bool CreateDbDialog::consumeCanceled() {
    const bool value = canceled_;
    canceled_ = false;
    return value;
}

} // namespace pixeldb::tui
