#pragma once

#include "components/row_detail.hpp"
#include "components/table_view.hpp"
#include "vaultdb/vaultdb.hpp"

#include <ftxui/component/event.hpp>
#include <ftxui/dom/elements.hpp>

#include <string>
#include <vector>

namespace vaultdb::tui {

class ResultsPanel {
public:
    void display(const vaultdb::Result& result, int durationMs, std::string title = "Results");
    bool handleEvent(ftxui::Event event, std::string& clipboard);
    ftxui::Element render(bool focused) const;

    const vaultdb::Result& result() const { return result_; }
    bool canOpenHistory() const;
    std::string selectedPrimaryKey() const;

private:
    vaultdb::Result result_;
    int durationMs_ = 0;
    std::string title_ = "Results";
    int selectedRow_ = 0;
    int rowOffset_ = 0;
    int columnOffset_ = 0;
    bool detailOpen_ = false;
    bool filterOpen_ = false;
    std::string filter_;

    TableView tableView_;
    RowDetail rowDetail_;

    std::vector<std::vector<std::string>> filteredRows() const;
    void clampSelection();
    ftxui::Element renderMessage() const;
    ftxui::Element renderFilterPopup() const;
};

} // namespace vaultdb::tui
