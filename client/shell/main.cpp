#include "vaultdb/vaultdb.hpp"
#include "vaultdb/string_utils.hpp"

#include <iostream>
#include <stdexcept>
#include <string>
#include <vector>

void renderTable(const std::vector<std::string>& columns,
                 const std::vector<std::vector<std::string>>& rows);

int main(int argc, char** argv) {
    std::string host = "127.0.0.1";
    int port = 5432;

    if (argc >= 2) {
        host = argv[1];
    }
    if (argc >= 3) {
        try {
            port = std::stoi(argv[2]);
        } catch (const std::exception&) {
            std::cerr << "Invalid port: " << argv[2] << '\n';
            return 1;
        }
    }

    std::cout << "╔══════════════════════════════════════════════╗\n";
    std::cout << "║           ⚔  VAULT DB  ⚔                    ║\n";
    std::cout << "║      A DATABASE FROM ANOTHER DIMENSION      ║\n";
    std::cout << "║                                              ║\n";
    std::cout << "║  Version 1.2.0  |  Press Ctrl+C to quit     ║\n";
    std::cout << "╚══════════════════════════════════════════════╝\n\n";

    std::cout << "[QUEST LOG] Connecting to dungeon at " << host << ':' << port << "...\n";

    vaultdb::Connection connection(host, port);
    if (!connection.connect()) {
        std::cerr << "[GAME OVER]  Failed to connect to server.\n";
        return 1;
    }

    std::cout << "[SUCCESS]   Connected! Your adventure begins.\n\n";

    std::string query;
    while (true) {
        std::cout << "VaultDB> " << std::flush;
        if (!std::getline(std::cin, query)) {
            break;
        }

        query = vaultdb::trim(query);
        if (query.empty()) {
            continue;
        }

        const std::string lower = vaultdb::toLower(query);
        if (lower == "exit" || lower == "quit") {
            break;
        }

        query = vaultdb::ensureSemicolon(query);

        try {
            const vaultdb::Result result = connection.execute(query);

            if (result.isError()) {
                std::cout << "[GAME OVER]  " << result.message << "\n";
                continue;
            }

            if (result.isRows()) {
                renderTable(result.columns, result.rows);
                std::cout << '[' << result.rows.size() << " rows in set]" << "\n";
                continue;
            }

            if (result.isAffected()) {
                std::cout << "[QUEST COMPLETE]  Affected heroes: " << result.affected << "\n";
                continue;
            }

            std::cout << "[ACHIEVEMENT UNLOCKED]  " << result.message << "\n";
        } catch (const std::exception& ex) {
            std::cout << "[GAME OVER]  " << ex.what() << "\n";
        }
    }

    connection.disconnect();
    return 0;
}
