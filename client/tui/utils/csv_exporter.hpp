#pragma once

#include <string>
#include <vector>

namespace pixeldb::tui::utils {

std::string toCsvRow(const std::vector<std::string>& row);
std::string toCsv(const std::vector<std::string>& columns,
                  const std::vector<std::vector<std::string>>& rows);

} // namespace pixeldb::tui::utils
