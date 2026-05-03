#pragma once

#include <cstddef>
#include <string>

namespace pixeldb::tui {

struct Config {
    std::string host = "127.0.0.1";
    int port = 5432;
    std::string theme = "pixel";
    std::size_t historySize = 500;
    bool autocomplete = true;

    static Config load();
    void saveDefaultIfMissing() const;
};

} // namespace pixeldb::tui
