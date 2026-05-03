#pragma once

#include <ftxui/component/event.hpp>
#include <ftxui/dom/elements.hpp>

#include <string>
#include <vector>

namespace vaultdb::tui {

struct CreateTableColumnDraft {
    std::string name;
    std::string type = "INT";
    std::string length;
};

class CreateTableDialog {
public:
    void open(std::string database);
    bool isOpen() const { return open_; }
    bool handleEvent(ftxui::Event event);
    ftxui::Element render() const;

    bool consumeSubmitted();
    bool consumeCanceled();
    std::string database() const { return database_; }
    std::string createSql() const;

private:
    bool open_ = false;
    bool submitted_ = false;
    bool canceled_ = false;
    std::string database_;
    std::string tableName_;
    std::vector<CreateTableColumnDraft> columns_;
    int activeField_ = 0;

    bool isValid() const;
    void addColumn();
    void removeColumn();
    void cycleType(int direction);
};

} // namespace vaultdb::tui
