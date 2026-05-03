#include "utils/csv_exporter.hpp"

#include <sstream>

namespace pixeldb::tui::utils {

namespace {

std::string escapeCell(const std::string& value) {
    bool needsQuotes = false;
    for (char c : value) {
        if (c == '"' || c == ',' || c == '\n' || c == '\r') {
            needsQuotes = true;
            break;
        }
    }

    if (!needsQuotes) {
        return value;
    }

    std::string escaped = "\"";
    for (char c : value) {
        if (c == '"') {
            escaped += "\"\"";
        } else {
            escaped.push_back(c);
        }
    }
    escaped.push_back('"');
    return escaped;
}

} // namespace

std::string toCsvRow(const std::vector<std::string>& row) {
    std::ostringstream out;
    for (std::size_t i = 0; i < row.size(); ++i) {
        if (i != 0) {
            out << ',';
        }
        out << escapeCell(row[i]);
    }
    return out.str();
}

std::string toCsv(const std::vector<std::string>& columns,
                  const std::vector<std::vector<std::string>>& rows) {
    std::ostringstream out;
    out << toCsvRow(columns);
    for (const auto& row : rows) {
        out << '\n' << toCsvRow(row);
    }
    return out.str();
}

} // namespace pixeldb::tui::utils
