#include "panels/navigator.hpp"

#include "utils/string_utils.hpp"

#include <algorithm>

namespace vaultdb::tui {

namespace {

std::string sqlIdent(const std::string& value) {
    return value;
}

std::string tableLine(const NavigatorTable& table, bool last) {
    const std::string icon = last ? "  └ " : "  ├ ";
    const std::string rows = table.rowCount >= 0 ? " [" + std::to_string(table.rowCount) + "]" : "";
    return icon + table.name + rows;
}

} // namespace

void NavigatorPanel::refresh(vaultdb::Connection& connection) {
    lastError_.clear();
    try {
        const auto result = connection.execute("SHOW DATABASES;");
        if (result.isError()) {
            lastError_ = result.message;
            return;
        }

        std::vector<NavigatorDatabase> next;
        next.reserve(result.rows.size());
        for (const auto& row : result.rows) {
            if (row.empty()) {
                continue;
            }
            NavigatorDatabase db;
            db.name = row[0];
            const auto old = std::find_if(databases_.begin(), databases_.end(), [&](const NavigatorDatabase& item) {
                return utils::iequals(item.name, db.name);
            });
            if (old != databases_.end()) {
                db.expanded = old->expanded;
            }
            next.push_back(std::move(db));
        }
        databases_ = std::move(next);
        for (std::size_t i = 0; i < databases_.size(); ++i) {
            if (databases_[i].expanded) {
                loadTables(connection, i);
            }
        }
        clampSelection();
    } catch (const std::exception& ex) {
        lastError_ = ex.what();
    }
}

bool NavigatorPanel::handleEvent(ftxui::Event event,
                                 vaultdb::Connection& connection,
                                 const std::string& activeDb,
                                 const NavigatorCallbacks& callbacks) {
    using ftxui::Event;

    if (menuOpen_) {
        if (event == Event::Escape) {
            menuOpen_ = false;
            return true;
        }
        if (event == Event::ArrowUp) {
            menuIndex_ = std::max(0, menuIndex_ - 1);
            return true;
        }
        if (event == Event::ArrowDown) {
            menuIndex_ = std::min(3, menuIndex_ + 1);
            return true;
        }
        if (event == Event::Return) {
            menuOpen_ = false;
            const auto items = visibleItems();
            if (selectedIndex_ < 0 || selectedIndex_ >= static_cast<int>(items.size())) {
                return true;
            }
            const auto& item = items[static_cast<std::size_t>(selectedIndex_)];
            const auto& db = databases_[item.dbIndex];
            if (item.type == ItemType::Database) {
                if (menuIndex_ == 2 && callbacks.copyName) {
                    callbacks.copyName(db.name);
                } else if (menuIndex_ == 3 && callbacks.dropDatabase) {
                    callbacks.dropDatabase(db.name);
                }
                return true;
            }
            const auto& table = db.tables[item.tableIndex];
            if (menuIndex_ == 0 && callbacks.previewTable) {
                callbacks.previewTable(db.name, table.name);
            } else if (menuIndex_ == 1 && callbacks.showSchema) {
                callbacks.showSchema(db.name, table.name);
            } else if (menuIndex_ == 2 && callbacks.copyName) {
                callbacks.copyName(table.name);
            } else if (menuIndex_ == 3 && callbacks.dropTable) {
                callbacks.dropTable(db.name, table.name, table.rowCount);
            }
            return true;
        }
        return false;
    }

    if (event == Event::ArrowUp) {
        selectedIndex_ = std::max(0, selectedIndex_ - 1);
        return true;
    }
    if (event == Event::ArrowDown) {
        const int count = static_cast<int>(visibleItems().size());
        if (count > 0) {
            selectedIndex_ = std::min(count - 1, selectedIndex_ + 1);
        }
        return true;
    }
    if (event == Event::Character(' ') || event == Event::ArrowRight) {
        const auto items = visibleItems();
        if (selectedIndex_ >= 0 && selectedIndex_ < static_cast<int>(items.size())) {
            const auto& item = items[static_cast<std::size_t>(selectedIndex_)];
            if (item.type == ItemType::Database) {
                auto& db = databases_[item.dbIndex];
                db.expanded = !db.expanded;
                if (db.expanded && !db.loaded) {
                    loadTables(connection, item.dbIndex);
                }
            } else {
                menuOpen_ = true;
                menuIndex_ = 0;
            }
        }
        return true;
    }
    if (event == Event::Character('m')) {
        menuOpen_ = true;
        menuIndex_ = 0;
        return true;
    }
    if (event == Event::Return) {
        handleSelectedAction(connection, activeDb, callbacks, false);
        return true;
    }
    if (event == Event::Character('p')) {
        handleSelectedAction(connection, activeDb, callbacks, true);
        return true;
    }
    if (event == Event::Character('s')) {
        const auto items = visibleItems();
        if (selectedIndex_ >= 0 && selectedIndex_ < static_cast<int>(items.size())) {
            const auto& item = items[static_cast<std::size_t>(selectedIndex_)];
            if (item.type == ItemType::Table && callbacks.showSchema) {
                callbacks.showSchema(databases_[item.dbIndex].name, databases_[item.dbIndex].tables[item.tableIndex].name);
            }
        }
        return true;
    }
    if (event == Event::Character('d')) {
        const auto items = visibleItems();
        if (selectedIndex_ >= 0 && selectedIndex_ < static_cast<int>(items.size())) {
            const auto& item = items[static_cast<std::size_t>(selectedIndex_)];
            const auto& db = databases_[item.dbIndex];
            if (item.type == ItemType::Database && callbacks.dropDatabase) {
                callbacks.dropDatabase(db.name);
            } else if (item.type == ItemType::Table && callbacks.dropTable) {
                const auto& table = db.tables[item.tableIndex];
                callbacks.dropTable(db.name, table.name, table.rowCount);
            }
        }
        return true;
    }
    if (event == Event::Character('n')) {
        const std::string db = selectedDatabaseName().empty() ? activeDb : selectedDatabaseName();
        if (!db.empty() && callbacks.createTable) {
            callbacks.createTable(db);
        }
        return true;
    }
    if (event == Event::Character('r')) {
        refresh(connection);
        return true;
    }

    return false;
}

ftxui::Element NavigatorPanel::render(const std::string& activeDb, bool focused) const {
    using namespace ftxui;
    std::vector<TreeLine> lines;
    const auto items = visibleItems();
    for (int i = 0; i < static_cast<int>(items.size()); ++i) {
        const auto& item = items[static_cast<std::size_t>(i)];
        const auto& db = databases_[item.dbIndex];
        if (item.type == ItemType::Database) {
            const bool active = utils::iequals(activeDb, db.name);
            const std::string icon = db.tables.empty() && db.loaded ? "▷ " : (db.expanded ? "▼ " : "▶ ");
            lines.push_back(TreeLine{icon + (active ? "★ " : "  ") + db.name, i == selectedIndex_, active, true});
        } else {
            const auto& table = db.tables[item.tableIndex];
            lines.push_back(TreeLine{tableLine(table, item.tableIndex + 1 == db.tables.size()), i == selectedIndex_, false, false});
        }
    }

    TreeView tree;
    auto body = tree.render(lines);
    if (!lastError_.empty()) {
        body = vbox({
            body | flex,
            separator(),
            paragraph("Navigator error: " + lastError_) | color(Color::Red),
        });
    }
    auto panel = body | border | color(focused ? Color::Blue : Color::GrayDark);

    if (menuOpen_ && selectedIndex_ >= 0 && selectedIndex_ < static_cast<int>(items.size())) {
        const auto& item = items[static_cast<std::size_t>(selectedIndex_)];
        const auto title = item.type == ItemType::Database
            ? databases_[item.dbIndex].name
            : databases_[item.dbIndex].tables[item.tableIndex].name;
        panel = dbox({panel, renderContextMenu(title) | clear_under | center});
    }
    return panel | size(WIDTH, EQUAL, 28);
}

std::vector<std::string> NavigatorPanel::tableNames(const std::string& db) const {
    std::vector<std::string> names;
    for (const auto& database : databases_) {
        if (!utils::iequals(database.name, db)) {
            continue;
        }
        for (const auto& table : database.tables) {
            names.push_back(table.name);
        }
        break;
    }
    return names;
}

std::vector<std::string> NavigatorPanel::selectedColumns() const {
    const auto items = visibleItems();
    if (selectedIndex_ < 0 || selectedIndex_ >= static_cast<int>(items.size())) {
        return {};
    }
    const auto& item = items[static_cast<std::size_t>(selectedIndex_)];
    if (item.type != ItemType::Table) {
        return {};
    }
    return databases_[item.dbIndex].tables[item.tableIndex].columns;
}

void NavigatorPanel::setTableColumns(const std::string& db, const std::string& table, std::vector<std::string> columns) {
    for (auto& database : databases_) {
        if (!utils::iequals(database.name, db)) {
            continue;
        }
        for (auto& item : database.tables) {
            if (utils::iequals(item.name, table)) {
                item.columns = std::move(columns);
                return;
            }
        }
    }
}

std::string NavigatorPanel::selectedDatabaseName() const {
    const auto items = visibleItems();
    if (selectedIndex_ < 0 || selectedIndex_ >= static_cast<int>(items.size())) {
        return "";
    }
    const auto& item = items[static_cast<std::size_t>(selectedIndex_)];
    return databases_[item.dbIndex].name;
}

std::vector<NavigatorPanel::VisibleItem> NavigatorPanel::visibleItems() const {
    std::vector<VisibleItem> items;
    for (std::size_t dbIndex = 0; dbIndex < databases_.size(); ++dbIndex) {
        items.push_back(VisibleItem{ItemType::Database, dbIndex, 0});
        if (!databases_[dbIndex].expanded) {
            continue;
        }
        for (std::size_t tableIndex = 0; tableIndex < databases_[dbIndex].tables.size(); ++tableIndex) {
            items.push_back(VisibleItem{ItemType::Table, dbIndex, tableIndex});
        }
    }
    return items;
}

void NavigatorPanel::clampSelection() {
    const int count = static_cast<int>(visibleItems().size());
    if (count <= 0) {
        selectedIndex_ = 0;
        return;
    }
    selectedIndex_ = std::max(0, std::min(selectedIndex_, count - 1));
}

void NavigatorPanel::loadTables(vaultdb::Connection& connection, std::size_t dbIndex) {
    if (dbIndex >= databases_.size()) {
        return;
    }
    auto& db = databases_[dbIndex];
    try {
        const auto result = connection.execute("SHOW TABLES FROM " + sqlIdent(db.name) + ";");
        if (result.isError()) {
            lastError_ = result.message;
            return;
        }
        db.tables.clear();
        for (const auto& row : result.rows) {
            if (row.empty()) {
                continue;
            }
            NavigatorTable table;
            table.name = row[0];
            if (row.size() > 1) {
                try {
                    table.rowCount = std::stoi(row[1]);
                } catch (const std::exception&) {
                    table.rowCount = -1;
                }
            }
            db.tables.push_back(std::move(table));
        }
        db.loaded = true;
    } catch (const std::exception& ex) {
        lastError_ = ex.what();
    }
}

void NavigatorPanel::handleSelectedAction(vaultdb::Connection& connection,
                                          const std::string&,
                                          const NavigatorCallbacks& callbacks,
                                          bool previewTable) {
    const auto items = visibleItems();
    if (selectedIndex_ < 0 || selectedIndex_ >= static_cast<int>(items.size())) {
        return;
    }
    const auto& item = items[static_cast<std::size_t>(selectedIndex_)];
    auto& db = databases_[item.dbIndex];
    if (item.type == ItemType::Database) {
        if (callbacks.selectDatabase) {
            callbacks.selectDatabase(db.name);
        }
        if (!db.expanded) {
            db.expanded = true;
            if (!db.loaded) {
                loadTables(connection, item.dbIndex);
            }
        }
        return;
    }

    const auto& table = db.tables[item.tableIndex];
    if (previewTable && callbacks.previewTable) {
        callbacks.previewTable(db.name, table.name);
    } else if (callbacks.showSchema) {
        callbacks.showSchema(db.name, table.name);
    }
}

ftxui::Element NavigatorPanel::renderContextMenu(const std::string& title) const {
    using namespace ftxui;
    const std::vector<std::string> options = {"Preview", "Show Schema", "Copy Name", "Drop"};
    Elements rows;
    rows.push_back(text(" " + title + " ") | bold | color(Color::Yellow));
    rows.push_back(separator());
    for (int i = 0; i < static_cast<int>(options.size()); ++i) {
        auto row = text((i == menuIndex_ ? "> " : "  ") + options[static_cast<std::size_t>(i)]);
        if (i == menuIndex_) {
            row = row | inverted;
        }
        rows.push_back(row);
    }
    return vbox(std::move(rows)) | border | bgcolor(Color::Black);
}

} // namespace vaultdb::tui
