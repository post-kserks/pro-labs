#include "app/app.hpp"

#include "utils/string_utils.hpp"

#include <chrono>
#include <cctype>
#include <regex>
#include <thread>

namespace vaultdb::tui {

namespace {

vaultdb::Result errorResult(const std::string& message) {
    vaultdb::Result result;
    result.success = false;
    result.type = "error";
    result.message = message;
    return result;
}

bool isMutatingOrMetadataQuery(const std::string& sql) {
    const std::string lower = utils::toLower(utils::trim(sql));
    return lower.rfind("create ", 0) == 0 ||
           lower.rfind("drop ", 0) == 0 ||
           lower.rfind("insert ", 0) == 0 ||
           lower.rfind("update ", 0) == 0 ||
           lower.rfind("delete ", 0) == 0;
}

std::string firstIdentifierAfterUse(const std::string& sql) {
    const std::regex pattern("^\\s*use\\s+([A-Za-z_][A-Za-z0-9_]*)\\s*;?\\s*$", std::regex::icase);
    std::smatch match;
    if (std::regex_match(sql, match, pattern)) {
        return match[1].str();
    }
    return "";
}

bool isCtrl(ftxui::Event event, char key) {
    const char upper = static_cast<char>(std::toupper(static_cast<unsigned char>(key)));
    if (upper < 'A' || upper > 'Z') {
        return false;
    }
    return event == ftxui::Event::Special(std::string(1, static_cast<char>(upper - 'A' + 1)));
}

std::vector<std::string> columnsFromDescribe(const vaultdb::Result& result) {
    std::vector<std::string> columns;
    for (const auto& row : result.rows) {
        if (!row.empty()) {
            columns.push_back(row[0]);
        }
    }
    return columns;
}

} // namespace

App::App(Config config)
    : config_(std::move(config)),
      connection_(config_.host, config_.port),
      history_(config_.historySize) {
    history_.load();
}

void App::run() {
    auto screen = ftxui::ScreenInteractive::Fullscreen();
    screen_ = &screen;

    auto root = ftxui::CatchEvent(ftxui::Renderer([&] {
        return render();
    }), [&](ftxui::Event event) {
        return handleEvent(event);
    });

    std::thread connector([&] {
        std::this_thread::sleep_for(std::chrono::milliseconds(1500));
        attemptConnect();
        if (screen_ != nullptr) {
            screen_->PostEvent(ftxui::Event::Custom);
        }
    });

    screen.Loop(root);
    if (connector.joinable()) {
        connector.join();
    }
    connection_.disconnect();
    history_.save();
}

ftxui::Element App::render() const {
    using namespace ftxui;

    Element base;
    if (mode_ == Mode::Splash) {
        base = splashScreen_.render(config_.host, config_.port);
    } else if (mode_ == Mode::ConnectionError) {
        base = connectionErrorScreen_.render(config_.host, config_.port, connectionError_);
    } else {
        base = mainScreen_.render(
            header_.render(activeDb_, config_.host, config_.port, connection_.isConnected()),
            navigator_.render(activeDb_, focus_ == FocusArea::Navigator),
            editor_.render(focus_ == FocusArea::Editor),
            results_.render(focus_ == FocusArea::Results),
            statusBar_.render(focus_, activeDb_, statusMessage_, connection_.isConnected()));
    }

    if (mode_ != Mode::Main) {
        return base;
    }

    if (helpScreen_.isOpen()) {
        base = dbox({base, helpScreen_.render() | clear_under | center});
    }
    if (historyScreen_.isOpen()) {
        base = dbox({base, historyScreen_.render(history_) | clear_under | center});
    }
    if (createDbDialog_.isOpen()) {
        base = dbox({base, createDbDialog_.render() | clear_under | center});
    }
    if (createTableDialog_.isOpen()) {
        base = dbox({base, createTableDialog_.render() | clear_under | center});
    }
    if (confirmDropDialog_.isOpen()) {
        base = dbox({base, confirmDropDialog_.render() | clear_under | center});
    }
    if (confirmExitDialog_.isOpen()) {
        base = dbox({base, confirmExitDialog_.render() | clear_under | center});
    }
    return base;
}

bool App::handleEvent(ftxui::Event event) {
    using ftxui::Event;

    if (mode_ == Mode::Splash) {
        if (isCtrl(event, 'Q')) {
            screen_->ExitLoopClosure()();
            return true;
        }
        return true;
    }

    if (mode_ == Mode::ConnectionError) {
        if (event == Event::Character('r') || event == Event::Character('R')) {
            mode_ = Mode::Splash;
            attemptConnect();
            return true;
        }
        if (event == Event::Character('q') || event == Event::Character('Q') || isCtrl(event, 'Q')) {
            screen_->ExitLoopClosure()();
            return true;
        }
        return true;
    }

    if (handleOverlayEvent(event)) {
        return true;
    }

    if (event == Event::F1) {
        helpScreen_.open();
        return true;
    }
    if (event == Event::F2) {
        historyScreen_.open();
        return true;
    }
    if (event == Event::F5 || isCtrl(event, 'R') || 
        event == ftxui::Event::Special("\x1b\x0d") || 
        event == ftxui::Event::Special("\x1b\x0a") ||
        event == ftxui::Event::Special("\x1b[13;5u")) {
        executeEditorQuery();
        return true;
    }
    if (event == Event::F9 || isCtrl(event, 'N')) {
        createDbDialog_.open();
        return true;
    }
    if (event == Event::F10 || isCtrl(event, 'Q')) {
        if (!utils::trim(editor_.query()).empty()) {
            confirmExitDialog_.open();
        } else {
            screen_->ExitLoopClosure()();
        }
        return true;
    }
    if (isCtrl(event, 'Q')) {
        screen_->ExitLoopClosure()();
        return true;
    }
    if (event == Event::TabReverse) {
        switchFocus();
        return true;
    }
    if (event == Event::Tab && focus_ != FocusArea::Editor) {
        switchFocus();
        return true;
    }

    return handleMainEvent(event);
}

bool App::handleMainEvent(ftxui::Event event) {
    if (focus_ == FocusArea::Navigator) {
        NavigatorCallbacks callbacks;
        callbacks.selectDatabase = [&](const std::string& db) { selectDatabase(db); };
        callbacks.previewTable = [&](const std::string& db, const std::string& table) { previewTable(db, table); };
        callbacks.showSchema = [&](const std::string& db, const std::string& table) { showSchema(db, table); };
        callbacks.dropTable = [&](const std::string& db, const std::string& table, int rows) {
            confirmDropDialog_.openTable(db, table, rows);
        };
        callbacks.dropDatabase = [&](const std::string& db) { confirmDropDialog_.openDatabase(db); };
        callbacks.createTable = [&](const std::string& db) { createTableDialog_.open(db); };
        callbacks.copyName = [&](const std::string& value) {
            clipboard_ = value;
            statusMessage_ = "Copied: " + value;
        };
        return navigator_.handleEvent(event, connection_, activeDb_, callbacks);
    }

    if (focus_ == FocusArea::Editor) {
        if (event == ftxui::Event::Tab) {
            return editor_.handleEvent(event, completionContext(), history_, clipboard_);
        }
        return editor_.handleEvent(event, completionContext(), history_, clipboard_);
    }

    if (focus_ == FocusArea::Results) {
        return results_.handleEvent(event, clipboard_);
    }

    return false;
}

bool App::handleOverlayEvent(ftxui::Event event) {
    if (helpScreen_.isOpen()) {
        return helpScreen_.handleEvent(event);
    }

    if (historyScreen_.isOpen()) {
        const bool handled = historyScreen_.handleEvent(event, history_);
        std::string loaded;
        if (historyScreen_.consumeLoadedQuery(loaded)) {
            editor_.setQuery(loaded);
            focus_ = FocusArea::Editor;
        }
        return handled;
    }

    if (createDbDialog_.isOpen()) {
        const bool handled = createDbDialog_.handleEvent(event);
        if (createDbDialog_.consumeSubmitted()) {
            createDatabase(createDbDialog_.name());
        }
        return handled;
    }

    if (createTableDialog_.isOpen()) {
        const bool handled = createTableDialog_.handleEvent(event);
        if (createTableDialog_.consumeSubmitted()) {
            createTable(createTableDialog_.database(), createTableDialog_.createSql());
        }
        return handled;
    }

    if (confirmDropDialog_.isOpen()) {
        const bool handled = confirmDropDialog_.handleEvent(event);
        if (confirmDropDialog_.consumeSubmitted()) {
            dropConfirmed();
        }
        return handled;
    }

    if (confirmExitDialog_.isOpen()) {
        const bool handled = confirmExitDialog_.handleEvent(event);
        if (confirmExitDialog_.consumeSubmitted()) {
            screen_->ExitLoopClosure()();
        }
        return handled;
    }

    return false;
}

void App::attemptConnect() {
    connectionError_.clear();
    connection_.disconnect();
    if (!connection_.connect()) {
        mode_ = Mode::ConnectionError;
        connectionError_ = "Connection refused or host is unreachable.";
        statusMessage_ = "Disconnected";
        return;
    }

    mode_ = Mode::Main;
    statusMessage_ = "Connected";
    refreshNavigator();
}

void App::executeEditorQuery() {
    const std::string sql = utils::ensureSemicolon(editor_.query());
    if (sql.empty()) {
        statusMessage_ = "Editor is empty";
        return;
    }
    executeSql(sql, "Results", true);
    focus_ = FocusArea::Results;
}

vaultdb::Result App::executeSql(const std::string& sql, std::string title, bool addHistory) {
    const std::string normalized = utils::ensureSemicolon(sql);
    editor_.setState(EditorState::Running);
    const auto start = std::chrono::steady_clock::now();

    vaultdb::Result result;
    try {
        result = connection_.execute(normalized);
    } catch (const std::exception& ex) {
        result = errorResult(ex.what());
    }

    const auto finish = std::chrono::steady_clock::now();
    const int duration = static_cast<int>(std::chrono::duration_cast<std::chrono::milliseconds>(finish - start).count());

    editor_.setState(result.isError() ? EditorState::Error : EditorState::Ok);
    results_.display(result, duration, std::move(title));
    statusMessage_ = result.isError() ? result.message : "OK in " + std::to_string(duration) + "ms";

    if (addHistory) {
        history_.add(normalized, !result.isError(), duration);
    }
    maybeUpdateActiveDbFromQuery(normalized, result);

    if (!result.isError() && isMutatingOrMetadataQuery(normalized)) {
        refreshNavigator();
    }
    return result;
}

void App::selectDatabase(const std::string& db) {
    const auto result = executeSql("USE " + db + ";", "Results", true);
    if (!result.isError()) {
        activeDb_ = db;
        statusMessage_ = "Using " + db;
    }
}

void App::previewTable(const std::string& db, const std::string& table) {
    selectDatabase(db);
    const std::string query = "SELECT * FROM " + table + " LIMIT 10;";
    editor_.setQuery(query);
    executeSql(query, "Preview: " + table, true);
    focus_ = FocusArea::Results;
}

void App::showSchema(const std::string& db, const std::string& table) {
    const std::string query = "DESCRIBE " + table + " FROM " + db + ";";
    const auto result = executeSql(query, "Schema: " + table, false);
    if (!result.isError()) {
        navigator_.setTableColumns(db, table, columnsFromDescribe(result));
    }
    focus_ = FocusArea::Results;
}

void App::createDatabase(const std::string& name) {
    const auto result = executeSql("CREATE DATABASE " + name + ";", "Results", true);
    if (!result.isError()) {
        selectDatabase(name);
        refreshNavigator();
    }
}

void App::createTable(const std::string& db, const std::string& sql) {
    selectDatabase(db);
    executeSql(sql, "Results", true);
    refreshNavigator();
}

void App::dropConfirmed() {
    if (confirmDropDialog_.kind() == DropTargetKind::Database) {
        const std::string db = confirmDropDialog_.database();
        const auto result = executeSql("DROP DATABASE " + db + ";", "Results", true);
        if (!result.isError() && utils::iequals(activeDb_, db)) {
            activeDb_.clear();
        }
    } else {
        selectDatabase(confirmDropDialog_.database());
        executeSql("DROP TABLE " + confirmDropDialog_.table() + ";", "Results", true);
    }
    refreshNavigator();
}

void App::refreshNavigator() {
    if (!connection_.isConnected()) {
        return;
    }
    navigator_.refresh(connection_);
    if (!navigator_.lastError().empty()) {
        statusMessage_ = navigator_.lastError();
    }
}

void App::switchFocus() {
    switch (focus_) {
    case FocusArea::Navigator:
        focus_ = FocusArea::Editor;
        break;
    case FocusArea::Editor:
        focus_ = FocusArea::Results;
        break;
    case FocusArea::Results:
        focus_ = FocusArea::Navigator;
        break;
    }
}

void App::maybeUpdateActiveDbFromQuery(const std::string& sql, const vaultdb::Result& result) {
    if (result.isError()) {
        return;
    }
    const std::string db = firstIdentifierAfterUse(sql);
    if (!db.empty()) {
        activeDb_ = db;
    }
}

CompletionContext App::completionContext() const {
    CompletionContext context;
    context.enabled = config_.autocomplete;
    context.tables = navigator_.tableNames(activeDb_);
    context.columns = navigator_.selectedColumns();
    return context;
}

} // namespace vaultdb::tui
