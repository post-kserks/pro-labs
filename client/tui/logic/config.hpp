#pragma once

#include <cstddef>
#include <string>

namespace vaultdb::tui {

struct Config {
    std::string host = "127.0.0.1";
    int port = 5432;
    std::string theme = "vault";
    std::size_t historySize = 500;
    bool autocomplete = true;

    static Config load();
    void saveDefaultIfMissing() const;
};

} // namespace vaultdb::tui
