#pragma once

#include <ftxui/component/event.hpp>
#include <ftxui/dom/elements.hpp>

#include <string>

namespace pixeldb::tui {

enum class DropTargetKind {
    Database,
    Table,
};

class ConfirmDropDialog {
public:
    void openDatabase(std::string database);
    void openTable(std::string database, std::string table, int rowCount);
    bool isOpen() const { return open_; }
    bool handleEvent(ftxui::Event event);
    ftxui::Element render() const;

    bool consumeSubmitted();
    bool consumeCanceled();
    DropTargetKind kind() const { return kind_; }
    std::string database() const { return database_; }
    std::string table() const { return table_; }

private:
    bool open_ = false;
    bool submitted_ = false;
    bool canceled_ = false;
    DropTargetKind kind_ = DropTargetKind::Table;
    std::string database_;
    std::string table_;
    int rowCount_ = -1;
    std::string confirmation_;

    std::string targetName() const;
};

class ConfirmExitDialog {
public:
    void open();
    bool isOpen() const { return open_; }
    bool handleEvent(ftxui::Event event);
    ftxui::Element render() const;
    bool consumeSubmitted();

private:
    bool open_ = false;
    bool submitted_ = false;
};

} // namespace pixeldb::tui
