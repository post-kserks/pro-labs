#pragma once

#include "dialogs/confirm_dialog.hpp"
#include "dialogs/create_db_dialog.hpp"
#include "dialogs/create_table_dialog.hpp"
#include "dialogs/help_screen.hpp"
#include "dialogs/history_screen.hpp"
#include "logic/config.hpp"
#include "logic/history.hpp"
#include "panels/editor.hpp"
#include "panels/header.hpp"
#include "panels/navigator.hpp"
#include "panels/results.hpp"
#include "panels/status_bar.hpp"
#include "pixeldb/pixeldb.hpp"
#include "screens/connection_error.hpp"
#include "screens/main_screen.hpp"
#include "screens/splash_screen.hpp"

#include <ftxui/component/component.hpp>
#include <ftxui/component/screen_interactive.hpp>
#include <ftxui/dom/elements.hpp>

#include <string>

namespace pixeldb::tui {

class App {
public:
    explicit App(Config config);
    void run();

private:
    enum class Mode {
        Splash,
        ConnectionError,
        Main,
    };

    Config config_;
    pixeldb::Connection connection_;
    History history_;

    Mode mode_ = Mode::Splash;
    std::string activeDb_;
    std::string statusMessage_;
    std::string connectionError_;
    std::string clipboard_;

    HeaderPanel header_;
    NavigatorPanel navigator_;
    EditorPanel editor_;
    ResultsPanel results_;
    StatusBar statusBar_;
    SplashScreen splashScreen_;
    ConnectionErrorScreen connectionErrorScreen_;
    MainScreenFrame mainScreen_;
    HelpScreen helpScreen_;
    HistoryScreen historyScreen_;
    CreateDbDialog createDbDialog_;
    CreateTableDialog createTableDialog_;
    ConfirmDropDialog confirmDropDialog_;
    ConfirmExitDialog confirmExitDialog_;

    FocusArea focus_ = FocusArea::Navigator;

    ftxui::ScreenInteractive* screen_ = nullptr;

    ftxui::Element render() const;
    bool handleEvent(ftxui::Event event);
    bool handleMainEvent(ftxui::Event event);
    bool handleOverlayEvent(ftxui::Event event);

    void attemptConnect();
    void executeEditorQuery();
    pixeldb::Result executeSql(const std::string& sql, std::string title, bool addHistory);
    void selectDatabase(const std::string& db);
    void previewTable(const std::string& db, const std::string& table);
    void showSchema(const std::string& db, const std::string& table);
    void createDatabase(const std::string& name);
    void createTable(const std::string& db, const std::string& sql);
    void dropConfirmed();
    void refreshNavigator();
    void switchFocus();
    void maybeUpdateActiveDbFromQuery(const std::string& sql, const pixeldb::Result& result);
    CompletionContext completionContext() const;
};

} // namespace pixeldb::tui
