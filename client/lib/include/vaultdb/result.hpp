#pragma once

#include <string>
#include <vector>

namespace vaultdb {

struct Result {
    bool success = false;
    std::string type;

    std::vector<std::string> columns;
    std::vector<std::vector<std::string>> rows;

    int affected = 0;
    std::string message;

    bool isRows() const { return type == "rows"; }
    bool isAffected() const { return type == "affected"; }
    bool isError() const { return !success; }
};

} // namespace vaultdb
