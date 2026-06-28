#include "dialogs/create_table_dialog.hpp"

#include "utils/string_utils.hpp"

#include <algorithm>
#include <cctype>

namespace vaultdb::tui {

namespace {

const std::vector<std::string> kTypes = {"INT", "FLOAT", "BOOL", "TEXT", "VARCHAR"};

std::string columnTypeSql(const CreateTableColumnDraft& column) {
    if (column.type == "VARCHAR") {
        return "VARCHAR(" + (column.length.empty() ? "255" : column.length) + ")";
    }
    return column.type;
}

} // namespace

void CreateTableDialog::open(std::string database) {
    open_ = true;
    submitted_ = false;
    canceled_ = false;
    database_ = std::move(database);
    tableName_.clear();
    columns_.clear();
    columns_.push_back(CreateTableColumnDraft{"id", "INT", ""});
    activeField_ = 0;
}

bool CreateTableDialog::handleEvent(ftxui::Event event) {
    using ftxui::Event;
    if (!open_) {
        return false;
    }
    if (event == Event::Escape) {
        open_ = false;
        canceled_ = true;
        return true;
    }
    if (event == Event::Tab) {
        activeField_ = (activeField_ + 1) % static_cast<int>(1 + columns_.size() * 3);
        return true;
    }
    if (event == Event::TabReverse) {
        activeField_ = (activeField_ - 1 + static_cast<int>(1 + columns_.size() * 3)) %
                       static_cast<int>(1 + columns_.size() * 3);
        return true;
    }
    if (event == Event::Character('+')) {
        addColumn();
        return true;
    }
    if (event == Event::Character('-')) {
        removeColumn();
        return true;
    }
    if (event == Event::ArrowLeft) {
        cycleType(-1);
        return true;
    }
    if (event == Event::ArrowRight) {
        cycleType(1);
        return true;
    }
    if (event == Event::Backspace) {
        if (activeField_ == 0 && !tableName_.empty()) {
            tableName_.pop_back();
        } else if (activeField_ > 0) {
            const int field = activeField_ - 1;
            const int columnIndex = field / 3;
            const int subField = field % 3;
            if (columnIndex < static_cast<int>(columns_.size())) {
                auto& column = columns_[static_cast<std::size_t>(columnIndex)];
                if (subField == 0 && !column.name.empty()) {
                    column.name.pop_back();
                } else if (subField == 2 && !column.length.empty()) {
                    column.length.pop_back();
                }
            }
        }
        return true;
    }
    if (event == Event::Return) {
        if (isValid()) {
            open_ = false;
            submitted_ = true;
        }
        return true;
    }
    if (event.is_character()) {
        const std::string ch = event.character();
        if (ch.size() != 1) {
            return true;
        }
        const char c = ch[0];
        if (activeField_ == 0) {
            if (std::isalnum(static_cast<unsigned char>(c)) != 0 || c == '_') {
                tableName_ += c;
            }
            return true;
        }

        const int field = activeField_ - 1;
        const int columnIndex = field / 3;
        const int subField = field % 3;
        if (columnIndex >= static_cast<int>(columns_.size())) {
            return true;
        }
        auto& column = columns_[static_cast<std::size_t>(columnIndex)];
        if (subField == 0 && (std::isalnum(static_cast<unsigned char>(c)) != 0 || c == '_')) {
            column.name += c;
        } else if (subField == 2 && std::isdigit(static_cast<unsigned char>(c)) != 0) {
            column.length += c;
        }
        return true;
    }
    return true;
}

ftxui::Element CreateTableDialog::render() const {
    using namespace ftxui;
    Elements rows;
    rows.push_back(text("CREATE A NEW TABLE in '" + database_ + "'") | bold | color(Color::Yellow));
    rows.push_back(separator());
    rows.push_back(hbox({
        text("Table name: ") | size(WIDTH, EQUAL, 14),
        text((activeField_ == 0 ? "> " : "  ") + tableName_ + (activeField_ == 0 ? "▌" : "")) |
            color(activeField_ == 0 ? Color::Cyan : Color::White),
    }));
    rows.push_back(separator());
    rows.push_back(text("Columns") | bold);
    rows.push_back(hbox({
        text("Name") | size(WIDTH, EQUAL, 18) | color(Color::Yellow),
        text("Type") | size(WIDTH, EQUAL, 14) | color(Color::Yellow),
        text("Len") | size(WIDTH, EQUAL, 8) | color(Color::Yellow),
    }));
    for (std::size_t i = 0; i < columns_.size(); ++i) {
        const int base = 1 + static_cast<int>(i) * 3;
        const auto& column = columns_[i];
        rows.push_back(hbox({
            text((activeField_ == base ? "> " : "  ") + column.name + (activeField_ == base ? "▌" : "")) |
                size(WIDTH, EQUAL, 18),
            text((activeField_ == base + 1 ? "> " : "  ") + column.type) | size(WIDTH, EQUAL, 14) |
                color(activeField_ == base + 1 ? Color::Cyan : Color::White),
            text((activeField_ == base + 2 ? "> " : "  ") + column.length + (activeField_ == base + 2 ? "▌" : "")) |
                size(WIDTH, EQUAL, 8),
        }));
    }
    rows.push_back(separator());
    rows.push_back(text("[Tab] Field  [Left/Right] Type  [+] Add  [-] Remove") | color(Color::GrayDark));
    rows.push_back(text(isValid() ? "[Enter] Create  [Esc] Cancel" : "Fill valid table and column names") |
                   color(isValid() ? Color::GrayDark : Color::Red));
    return vbox(std::move(rows)) | border | bgcolor(Color::Black) | size(WIDTH, GREATER_THAN, 58);
}

bool CreateTableDialog::consumeSubmitted() {
    const bool value = submitted_;
    submitted_ = false;
    return value;
}

bool CreateTableDialog::consumeCanceled() {
    const bool value = canceled_;
    canceled_ = false;
    return value;
}

std::string CreateTableDialog::createSql() const {
    std::string sql = "CREATE TABLE " + utils::sqlIdent(tableName_) + " (";
    for (std::size_t i = 0; i < columns_.size(); ++i) {
        if (i != 0) {
            sql += ", ";
        }
        sql += utils::sqlIdent(columns_[i].name) + " " + columnTypeSql(columns_[i]);
    }
    sql += ");";
    return sql;
}

bool CreateTableDialog::isValid() const {
    if (!utils::isIdentifier(tableName_) || columns_.empty()) {
        return false;
    }
    for (const auto& column : columns_) {
        if (!utils::isIdentifier(column.name)) {
            return false;
        }
        if (column.type == "VARCHAR") {
            if (column.length.empty()) {
                continue;
            }
            try {
                if (std::stoi(column.length) <= 0) {
                    return false;
                }
            } catch (const std::exception&) {
                return false;
            }
        }
    }
    return true;
}

void CreateTableDialog::addColumn() {
    columns_.push_back(CreateTableColumnDraft{});
    activeField_ = 1 + static_cast<int>(columns_.size() - 1) * 3;
}

void CreateTableDialog::removeColumn() {
    if (columns_.size() <= 1) {
        return;
    }
    columns_.pop_back();
    activeField_ = std::min(activeField_, static_cast<int>(1 + columns_.size() * 3) - 1);
}

void CreateTableDialog::cycleType(int direction) {
    if (activeField_ <= 0) {
        return;
    }
    const int field = activeField_ - 1;
    const int columnIndex = field / 3;
    const int subField = field % 3;
    if (subField != 1 || columnIndex >= static_cast<int>(columns_.size())) {
        return;
    }

    auto& type = columns_[static_cast<std::size_t>(columnIndex)].type;
    auto it = std::find(kTypes.begin(), kTypes.end(), type);
    int index = it == kTypes.end() ? 0 : static_cast<int>(std::distance(kTypes.begin(), it));
    index = (index + direction + static_cast<int>(kTypes.size())) % static_cast<int>(kTypes.size());
    type = kTypes[static_cast<std::size_t>(index)];
}

} // namespace vaultdb::tui
