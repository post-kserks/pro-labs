#pragma once

#include <cstddef>
#include <string>
#include <utility>
#include <vector>

namespace vaultdb::tui::utils {

std::string trim(const std::string& value);
std::string toLower(std::string value);
std::string toUpper(std::string value);
bool iequals(const std::string& left, const std::string& right);
bool startsWithIgnoreCase(const std::string& value, const std::string& prefix);
bool containsIgnoreCase(const std::string& value, const std::string& needle);
std::vector<std::string> splitLines(const std::string& value);
std::string clip(const std::string& value, std::size_t maxWidth);
bool isIdentifier(const std::string& value);
std::string ensureSemicolon(std::string query);
std::pair<std::size_t, std::size_t> wordBoundsAt(const std::string& value, std::size_t cursor);
std::string homeDirectory();
std::string vaultdbDirectory();
std::string isoTimestampNow();
std::string shellEscapeSingleQuoted(const std::string& value);
std::string sqlIdent(const std::string& value);

} // namespace vaultdb::tui::utils
