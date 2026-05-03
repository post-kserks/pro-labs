#pragma once

#include "components/autocomplete_popup.hpp"
#include "logic/autocomplete.hpp"
#include "logic/highlighter.hpp"
#include "logic/history.hpp"

#include <ftxui/component/event.hpp>
#include <ftxui/dom/elements.hpp>

#include <functional>
#include <string>
#include <vector>

namespace pixeldb::tui {

enum class EditorState {
    Ready,
    Running,
    Ok,
    Error,
};

class EditorPanel {
public:
    ftxui::Element render(bool focused) const;
    bool handleEvent(ftxui::Event event,
                     const CompletionContext& completionContext,
                     const History& history,
                     std::string& clipboard);

    const std::string& query() const { return query_; }
    std::size_t cursor() const { return cursor_; }
    void setQuery(std::string query);
    void clear();
    void setState(EditorState state) { state_ = state; }
    EditorState state() const { return state_; }

private:
    std::string query_;
    std::size_t cursor_ = 0;
    EditorState state_ = EditorState::Ready;
    std::vector<std::string> undoStack_;
    bool selectAll_ = false;

    bool autocompleteOpen_ = false;
    std::vector<std::string> suggestions_;
    int suggestionIndex_ = 0;
    int historyIndex_ = -1;

    Highlighter highlighter_;
    Autocomplete autocomplete_;
    AutocompletePopup popup_;

    void pushUndo();
    void insertText(const std::string& value);
    void eraseBeforeCursor();
    void eraseAtCursor();
    void moveLeft();
    void moveRight();
    void moveUp();
    void moveDown();
    void moveHome();
    void moveEnd();
    void applySuggestion();
    void openAutocomplete(const CompletionContext& context);
    std::pair<int, int> lineColumn() const;
    std::string queryWithCursor() const;
};

} // namespace pixeldb::tui
