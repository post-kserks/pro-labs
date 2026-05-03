#pragma once

#include "components/tree_view.hpp"
#include "pixeldb/pixeldb.hpp"

#include <ftxui/component/event.hpp>
#include <ftxui/dom/elements.hpp>

#include <functional>
#include <string>
#include <vector>

namespace pixeldb::tui {

struct NavigatorTable {
    std::string name;
    int rowCount = -1;
    std::vector<std::string> columns;
};

struct NavigatorDatabase {
    std::string name;
    bool expanded = false;
    bool loaded = false;
    std::vector<NavigatorTable> tables;
};

struct NavigatorCallbacks {
    std::function<void(const std::string& db)> selectDatabase;
    std::function<void(const std::string& db, const std::string& table)> previewTable;
    std::function<void(const std::string& db, const std::string& table)> showSchema;
    std::function<void(const std::string& db, const std::string& table, int rowCount)> dropTable;
    std::function<void(const std::string& db)> dropDatabase;
    std::function<void(const std::string& db)> createTable;
    std::function<void(const std::string& value)> copyName;
};

class NavigatorPanel {
public:
    void refresh(pixeldb::Connection& connection);
    bool handleEvent(ftxui::Event event,
                     pixeldb::Connection& connection,
                     const std::string& activeDb,
                     const NavigatorCallbacks& callbacks);
    ftxui::Element render(const std::string& activeDb, bool focused) const;

    std::vector<std::string> tableNames(const std::string& db) const;
    std::vector<std::string> selectedColumns() const;
    void setTableColumns(const std::string& db, const std::string& table, std::vector<std::string> columns);
    std::string selectedDatabaseName() const;
    std::string lastError() const { return lastError_; }

private:
    enum class ItemType {
        Database,
        Table,
    };

    struct VisibleItem {
        ItemType type = ItemType::Database;
        std::size_t dbIndex = 0;
        std::size_t tableIndex = 0;
    };

    std::vector<NavigatorDatabase> databases_;
    int selectedIndex_ = 0;
    bool menuOpen_ = false;
    int menuIndex_ = 0;
    std::string lastError_;

    std::vector<VisibleItem> visibleItems() const;
    void clampSelection();
    void loadTables(pixeldb::Connection& connection, std::size_t dbIndex);
    void handleSelectedAction(pixeldb::Connection& connection,
                              const std::string& activeDb,
                              const NavigatorCallbacks& callbacks,
                              bool previewTable);
    ftxui::Element renderContextMenu(const std::string& title) const;
};

} // namespace pixeldb::tui
