#include "panels/editor.hpp"

#include "utils/string_utils.hpp"

#include <algorithm>
#include <cctype>

namespace pixeldb::tui {

namespace {

std::string stateText(EditorState state) {
    switch (state) {
    case EditorState::Ready:
        return "READY";
    case EditorState::Running:
        return "RUNNING...";
    case EditorState::Ok:
        return "OK";
    case EditorState::Error:
        return "ERROR";
    }
    return "READY";
}

ftxui::Color stateColor(EditorState state) {
    using namespace ftxui;
    switch (state) {
    case EditorState::Ready:
        return Color::GrayDark;
    case EditorState::Running:
        return Color::Yellow;
    case EditorState::Ok:
        return Color::Green;
    case EditorState::Error:
        return Color::Red;
    }
    return Color::GrayDark;
}

std::size_t previousLineStart(const std::string& text, std::size_t cursor) {
    if (cursor == 0) {
        return 0;
    }
    const std::size_t prevNewline = text.rfind('\n', cursor - 1);
    if (prevNewline == std::string::npos) {
        return 0;
    }
    return prevNewline + 1;
}

std::size_t lineEnd(const std::string& text, std::size_t cursor) {
    const std::size_t next = text.find('\n', cursor);
    return next == std::string::npos ? text.size() : next;
}

bool isCtrl(ftxui::Event event, char key) {
    const char upper = static_cast<char>(std::toupper(static_cast<unsigned char>(key)));
    if (upper < 'A' || upper > 'Z') {
        return false;
    }
    return event == ftxui::Event::Special(std::string(1, static_cast<char>(upper - 'A' + 1)));
}

} // namespace

ftxui::Element EditorPanel::render(bool focused) const {
    using namespace ftxui;
    const auto [line, col] = lineColumn();
    const std::string title = " SQL Editor  Ln " + std::to_string(line) + " : Col " + std::to_string(col) +
                              " | " + stateText(state_) + " ";

    Element body;
    if (query_.empty()) {
        body = text("Type SQL here...") | color(Color::GrayDark);
    } else {
        body = highlighter_.highlight(queryWithCursor());
    }

    Element content = vbox({
        body | flex,
        filler(),
        separator(),
        text("Enter: newline   F5/Ctrl+Enter: execute   Tab: autocomplete") | color(Color::GrayDark),
    });

    auto panel = window(text(title) | color(stateColor(state_)) | bold, content) |
                 color(focused ? Color::Blue : Color::GrayDark) | flex;

    if (autocompleteOpen_) {
        panel = dbox({
            panel,
            vbox({
                filler(),
                hbox({filler(), popup_.render(suggestions_, suggestionIndex_)}),
            }),
        });
    }
    return panel;
}

bool EditorPanel::handleEvent(ftxui::Event event,
                              const CompletionContext& completionContext,
                              const History& history,
                              std::string& clipboard) {
    using ftxui::Event;

    if (autocompleteOpen_) {
        if (event == Event::Escape) {
            autocompleteOpen_ = false;
            return true;
        }
        if (event == Event::ArrowUp) {
            suggestionIndex_ = std::max(0, suggestionIndex_ - 1);
            return true;
        }
        if (event == Event::ArrowDown) {
            suggestionIndex_ = std::min(static_cast<int>(suggestions_.size()) - 1, suggestionIndex_ + 1);
            return true;
        }
        if (event == Event::Return) {
            applySuggestion();
            return true;
        }
    }

    if (event == Event::Tab) {
        openAutocomplete(completionContext);
        return true;
    }
    if (event == Event::ArrowLeft) {
        moveLeft();
        return true;
    }
    if (event == Event::ArrowRight) {
        moveRight();
        return true;
    }
    if (event == Event::ArrowUp) {
        if (query_.empty() && !history.entries().empty()) {
            historyIndex_ = std::min<int>(historyIndex_ + 1, static_cast<int>(history.entries().size()) - 1);
            setQuery(history.entries()[static_cast<std::size_t>(historyIndex_)].query);
        } else {
            moveUp();
        }
        return true;
    }
    if (event == Event::ArrowDown) {
        if (query_.empty() && !history.entries().empty()) {
            historyIndex_ = std::max(-1, historyIndex_ - 1);
            if (historyIndex_ >= 0) {
                setQuery(history.entries()[static_cast<std::size_t>(historyIndex_)].query);
            }
        } else {
            moveDown();
        }
        return true;
    }
    if (event == Event::Home) {
        moveHome();
        return true;
    }
    if (event == Event::End) {
        moveEnd();
        return true;
    }
    if (event == Event::Return) {
        insertText("\n");
        return true;
    }
    if (event == Event::Backspace) {
        eraseBeforeCursor();
        return true;
    }
    if (event == Event::Delete) {
        eraseAtCursor();
        return true;
    }
    if (isCtrl(event, 'K')) {
        clear();
        return true;
    }
    if (isCtrl(event, 'Z')) {
        if (!undoStack_.empty()) {
            query_ = undoStack_.back();
            undoStack_.pop_back();
            cursor_ = std::min(cursor_, query_.size());
        }
        return true;
    }
    if (isCtrl(event, 'A')) {
        selectAll_ = true;
        cursor_ = query_.size();
        return true;
    }
    if (isCtrl(event, 'C')) {
        clipboard = query_;
        selectAll_ = false;
        return true;
    }
    if (isCtrl(event, 'V')) {
        insertText(clipboard);
        return true;
    }

    if (event.is_character()) {
        insertText(event.character());
        return true;
    }

    return false;
}

void EditorPanel::setQuery(std::string query) {
    query_ = std::move(query);
    cursor_ = query_.size();
    state_ = EditorState::Ready;
    autocompleteOpen_ = false;
    selectAll_ = false;
}

void EditorPanel::clear() {
    pushUndo();
    query_.clear();
    cursor_ = 0;
    state_ = EditorState::Ready;
    autocompleteOpen_ = false;
    selectAll_ = false;
    historyIndex_ = -1;
}

void EditorPanel::pushUndo() {
    undoStack_.push_back(query_);
    if (undoStack_.size() > 20) {
        undoStack_.erase(undoStack_.begin());
    }
}

void EditorPanel::insertText(const std::string& value) {
    if (value.empty()) {
        return;
    }
    pushUndo();
    if (selectAll_) {
        query_.clear();
        cursor_ = 0;
        selectAll_ = false;
    }
    query_.insert(cursor_, value);
    cursor_ += value.size();
    state_ = EditorState::Ready;
}

void EditorPanel::eraseBeforeCursor() {
    if (selectAll_) {
        clear();
        return;
    }
    if (cursor_ == 0) {
        return;
    }
    pushUndo();
    query_.erase(cursor_ - 1, 1);
    --cursor_;
}

void EditorPanel::eraseAtCursor() {
    if (selectAll_) {
        clear();
        return;
    }
    if (cursor_ >= query_.size()) {
        return;
    }
    pushUndo();
    query_.erase(cursor_, 1);
}

void EditorPanel::moveLeft() {
    if (cursor_ > 0) {
        --cursor_;
    }
    selectAll_ = false;
}

void EditorPanel::moveRight() {
    if (cursor_ < query_.size()) {
        ++cursor_;
    }
    selectAll_ = false;
}

void EditorPanel::moveUp() {
    const std::size_t currentStart = previousLineStart(query_, cursor_);
    if (currentStart == 0) {
        cursor_ = 0;
        return;
    }
    const std::size_t col = cursor_ - currentStart;
    const std::size_t previousEnd = currentStart - 1;
    const std::size_t previousStart = previousLineStart(query_, previousEnd);
    cursor_ = std::min(previousStart + col, previousEnd);
}

void EditorPanel::moveDown() {
    const std::size_t currentStart = previousLineStart(query_, cursor_);
    const std::size_t currentEnd = lineEnd(query_, cursor_);
    if (currentEnd >= query_.size()) {
        cursor_ = query_.size();
        return;
    }
    const std::size_t col = cursor_ - currentStart;
    const std::size_t nextStart = currentEnd + 1;
    const std::size_t nextEnd = lineEnd(query_, nextStart);
    cursor_ = std::min(nextStart + col, nextEnd);
}

void EditorPanel::moveHome() {
    cursor_ = previousLineStart(query_, cursor_);
    selectAll_ = false;
}

void EditorPanel::moveEnd() {
    cursor_ = lineEnd(query_, cursor_);
    selectAll_ = false;
}

void EditorPanel::applySuggestion() {
    if (suggestions_.empty()) {
        autocompleteOpen_ = false;
        return;
    }
    pushUndo();
    query_ = autocomplete_.apply(query_, cursor_, suggestions_[static_cast<std::size_t>(suggestionIndex_)], cursor_);
    autocompleteOpen_ = false;
}

void EditorPanel::openAutocomplete(const CompletionContext& context) {
    suggestions_ = autocomplete_.suggestions(query_, cursor_, context);
    suggestionIndex_ = 0;
    autocompleteOpen_ = !suggestions_.empty();
}

std::pair<int, int> EditorPanel::lineColumn() const {
    int line = 1;
    int col = 1;
    for (std::size_t i = 0; i < cursor_ && i < query_.size(); ++i) {
        if (query_[i] == '\n') {
            ++line;
            col = 1;
        } else {
            ++col;
        }
    }
    return {line, col};
}

std::string EditorPanel::queryWithCursor() const {
    std::string visible = query_;
    visible.insert(std::min(cursor_, visible.size()), "▌");
    return visible;
}

} // namespace pixeldb::tui
