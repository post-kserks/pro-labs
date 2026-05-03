#include "dialogs/confirm_dialog.hpp"

namespace pixeldb::tui {

void ConfirmDropDialog::openDatabase(std::string database) {
    open_ = true;
    submitted_ = false;
    canceled_ = false;
    kind_ = DropTargetKind::Database;
    database_ = std::move(database);
    table_.clear();
    rowCount_ = -1;
    confirmation_.clear();
}

void ConfirmDropDialog::openTable(std::string database, std::string table, int rowCount) {
    open_ = true;
    submitted_ = false;
    canceled_ = false;
    kind_ = DropTargetKind::Table;
    database_ = std::move(database);
    table_ = std::move(table);
    rowCount_ = rowCount;
    confirmation_.clear();
}

bool ConfirmDropDialog::handleEvent(ftxui::Event event) {
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
        if (!confirmation_.empty()) {
            confirmation_.pop_back();
        }
        return true;
    }
    if (event == Event::Return) {
        if (confirmation_ == targetName()) {
            open_ = false;
            submitted_ = true;
        }
        return true;
    }
    if (event.is_character()) {
        confirmation_ += event.character();
        return true;
    }
    return true;
}

ftxui::Element ConfirmDropDialog::render() const {
    using namespace ftxui;
    const bool valid = confirmation_ == targetName();
    const std::string kindText = kind_ == DropTargetKind::Database ? "database" : "table";
    const std::string rows = kind_ == DropTargetKind::Table && rowCount_ >= 0
        ? "This will delete " + std::to_string(rowCount_) + " rows."
        : "This will delete all nested tables.";

    return vbox({
               text("[!] WARNING") | bold | color(Color::Red),
               separator(),
               paragraph("Drop " + kindText + " '" + targetName() + "'?"),
               paragraph(rows),
               paragraph("This action cannot be undone."),
               separator(),
               text("Type " + kindText + " name to confirm:"),
               text("> " + confirmation_ + "▌") | color(Color::Cyan),
               separator(),
               text(valid ? "[Enter] DROP IT   [Esc] Cancel" : "Exact name required") |
                   color(valid ? Color::Red : Color::GrayDark),
           }) |
           border | bgcolor(Color::Black) | size(WIDTH, GREATER_THAN, 44);
}

bool ConfirmDropDialog::consumeSubmitted() {
    const bool value = submitted_;
    submitted_ = false;
    return value;
}

bool ConfirmDropDialog::consumeCanceled() {
    const bool value = canceled_;
    canceled_ = false;
    return value;
}

std::string ConfirmDropDialog::targetName() const {
    return kind_ == DropTargetKind::Database ? database_ : table_;
}

void ConfirmExitDialog::open() {
    open_ = true;
    submitted_ = false;
}

bool ConfirmExitDialog::handleEvent(ftxui::Event event) {
    using ftxui::Event;
    if (!open_) {
        return false;
    }
    if (event == Event::Escape) {
        open_ = false;
        return true;
    }
    if (event == Event::Return || event == Event::Character('y') || event == Event::Character('Y')) {
        open_ = false;
        submitted_ = true;
        return true;
    }
    if (event == Event::Character('n') || event == Event::Character('N')) {
        open_ = false;
        return true;
    }
    return true;
}

ftxui::Element ConfirmExitDialog::render() const {
    using namespace ftxui;
    return vbox({
               text("Exit PixelDB TUI?") | bold | color(Color::Yellow),
               separator(),
               paragraph("The editor still contains a query."),
               text("[Enter/Y] Quit   [Esc/N] Cancel") | color(Color::GrayDark),
           }) |
           border | bgcolor(Color::Black) | size(WIDTH, GREATER_THAN, 36);
}

bool ConfirmExitDialog::consumeSubmitted() {
    const bool value = submitted_;
    submitted_ = false;
    return value;
}

} // namespace pixeldb::tui
