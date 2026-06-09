#include "app/app.hpp"
#include "logic/config.hpp"

#include <iostream>
#include <stdexcept>
#include <string>

namespace {

void printHelp(const char* binary) {
    std::cout << "VaultDB TUI client\n"
              << "\n"
              << "Usage:\n"
              << "  " << binary << " [--host HOST] [--port PORT]\n"
              << "  " << binary << " --version\n"
              << "  " << binary << " --help\n";
}

bool readValue(int& i, int argc, char** argv, std::string& value) {
    if (i + 1 >= argc) {
        return false;
    }
    value = argv[++i];
    return true;
}

} // namespace

int main(int argc, char** argv) {
    for (int i = 1; i < argc; ++i) {
        const std::string arg = argv[i];
        if (arg == "--help" || arg == "-h") {
            printHelp(argv[0]);
            return 0;
        }
        if (arg == "--version") {
            std::cout << "vaultdb-tui 1.2.0\n";
            return 0;
        }
    }

    vaultdb::tui::Config config = vaultdb::tui::Config::load();

    for (int i = 1; i < argc; ++i) {
        const std::string arg = argv[i];
        if (arg == "--host") {
            std::string value;
            if (!readValue(i, argc, argv, value)) {
                std::cerr << "--host requires a value\n";
                return 1;
            }
            config.host = value;
            continue;
        }
        if (arg == "--port") {
            std::string value;
            if (!readValue(i, argc, argv, value)) {
                std::cerr << "--port requires a value\n";
                return 1;
            }
            try {
                config.port = std::stoi(value);
            } catch (const std::exception&) {
                std::cerr << "Invalid port: " << value << '\n';
                return 1;
            }
            continue;
        }
        std::cerr << "Unknown argument: " << arg << '\n';
        printHelp(argv[0]);
        return 1;
    }

    vaultdb::tui::App app(config);
    app.run();
    return 0;
}
