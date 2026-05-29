#include <algorithm>
#include <iomanip>
#include <iostream>
#include <string>
#include <vector>

namespace {

std::string repeat(const std::string& unit, std::size_t count) {
    std::string out;
    out.reserve(unit.size() * count);
    for (std::size_t i = 0; i < count; ++i) {
        out += unit;
    }
    return out;
}

void printBorder(const std::vector<std::size_t>& widths,
                 const std::string& left,
                 const std::string& middle,
                 const std::string& right) {
    std::cout << left;
    for (std::size_t i = 0; i < widths.size(); ++i) {
        std::cout << repeat("═", widths[i] + 2);
        if (i + 1 < widths.size()) {
            std::cout << middle;
        }
    }
    std::cout << right << '\n';
}

void printRow(const std::vector<std::string>& row, const std::vector<std::size_t>& widths) {
    std::cout << "║";
    for (std::size_t i = 0; i < widths.size(); ++i) {
        const std::string cell = i < row.size() ? row[i] : "";
        std::cout << ' ' << std::left << std::setw(static_cast<int>(widths[i])) << cell << ' ';
        std::cout << "║";
    }
    std::cout << '\n';
}

} // namespace

void renderTable(const std::vector<std::string>& columns,
                 const std::vector<std::vector<std::string>>& rows) {
    if (columns.empty()) {
        std::cout << "[empty result]" << '\n';
        return;
    }

    std::vector<std::size_t> widths(columns.size(), 0);
    for (std::size_t i = 0; i < columns.size(); ++i) {
        widths[i] = columns[i].size();
    }

    for (const auto& row : rows) {
        for (std::size_t i = 0; i < columns.size() && i < row.size(); ++i) {
            widths[i] = std::max(widths[i], row[i].size());
        }
    }

    std::cout << "╔══════════════════════════════════════════════╗" << '\n';
    std::cout << "║  QUEST RESULTS  (" << rows.size() << " heroes found)";
    const std::size_t lineWidth = 44;
    const std::size_t textLen = 26 + std::to_string(rows.size()).size();
    if (textLen < lineWidth) {
        std::cout << repeat(" ", lineWidth - textLen);
    }
    std::cout << "║" << '\n';

    printBorder(widths, "╠", "╦", "╣");
    printRow(columns, widths);
    printBorder(widths, "╠", "╬", "╣");
    for (const auto& row : rows) {
        printRow(row, widths);
    }
    printBorder(widths, "╚", "╩", "╝");
}
