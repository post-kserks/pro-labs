#pragma once

#include <string>
#include <unordered_set>
#include <vector>

namespace vaultdb::tui::utils {

inline const std::unordered_set<std::string> kSQLKeywords = {
    "SELECT", "FROM", "WHERE", "AND", "OR", "NOT", "INSERT", "UPDATE", "DELETE",
    "CREATE", "DROP", "TABLE", "DATABASE", "DATABASES", "SHOW", "TABLES", "DESCRIBE",
    "VALUES", "SET", "INTO", "USE", "LIMIT", "COUNT", "NULL", "TRUE", "FALSE",
    "INT", "FLOAT", "BOOL", "TEXT", "VARCHAR",
};

inline const std::vector<std::string> kSQLKeywordsList = {
    "SELECT", "FROM", "WHERE", "AND", "OR", "NOT", "INSERT", "INTO", "VALUES",
    "UPDATE", "SET", "DELETE", "CREATE", "DROP", "DATABASE", "TABLE", "USE",
    "SHOW", "DATABASES", "TABLES", "DESCRIBE", "LIMIT", "NULL", "TRUE", "FALSE",
    "COUNT", "INT", "FLOAT", "BOOL", "TEXT", "VARCHAR",
};

} // namespace vaultdb::tui::utils
