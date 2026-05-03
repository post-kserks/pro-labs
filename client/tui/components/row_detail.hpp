#pragma once

#include <ftxui/dom/elements.hpp>

#include <string>
#include <vector>

namespace pixeldb::tui {

class RowDetail {
public:
    ftxui::Element render(const std::vector<std::string>& columns,
                          const std::vector<std::string>& row,
                          int rowIndex,
                          int rowCount) const;
};

} // namespace pixeldb::tui
