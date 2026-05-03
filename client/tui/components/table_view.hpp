#pragma once

#include <ftxui/dom/elements.hpp>

#include <string>
#include <vector>

namespace vaultdb::tui {

class TableView {
public:
    ftxui::Element render(const std::vector<std::string>& columns,
                          const std::vector<std::vector<std::string>>& rows,
                          int selectedRow,
                          int rowOffset,
                          int columnOffset,
                          int maxRows = 14) const;
};

} // namespace vaultdb::tui
