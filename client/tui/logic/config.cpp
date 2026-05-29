#include "logic/config.hpp"

#include "utils/string_utils.hpp"

#include <filesystem>
#include <fstream>
#include <regex>
#include <sstream>

namespace vaultdb::tui {

namespace {

std::filesystem::path configPath() {
    return std::filesystem::path(utils::vaultdbDirectory()) / "config.json";
}

std::string readFile(const std::filesystem::path& path) {
    std::ifstream in(path);
    if (!in) {
        return "";
    }
    std::ostringstream buffer;
    buffer << in.rdbuf();
    return buffer.str();
}

std::string stringValue(const std::string& json, const std::string& key, const std::string& fallback) {
    const std::regex pattern("\"" + key + "\"\\s*:\\s*\"([^\"]*)\"");
    std::smatch match;
    if (std::regex_search(json, match, pattern)) {
        return match[1].str();
    }
    return fallback;
}

int intValue(const std::string& json, const std::string& key, int fallback) {
    const std::regex pattern("\"" + key + "\"\\s*:\\s*([0-9]+)");
    std::smatch match;
    if (std::regex_search(json, match, pattern)) {
        return std::stoi(match[1].str());
    }
    return fallback;
}

bool boolValue(const std::string& json, const std::string& key, bool fallback) {
    const std::regex pattern("\"" + key + "\"\\s*:\\s*(true|false)");
    std::smatch match;
    if (std::regex_search(json, match, pattern)) {
        return match[1].str() == "true";
    }
    return fallback;
}

} // namespace

Config Config::load() {
    Config config;
    config.saveDefaultIfMissing();

    const std::string json = readFile(configPath());
    if (json.empty()) {
        return config;
    }

    config.host = stringValue(json, "host", config.host);
    config.port = intValue(json, "port", config.port);
    config.theme = stringValue(json, "theme", config.theme);
    config.historySize = static_cast<std::size_t>(intValue(json, "history_size", static_cast<int>(config.historySize)));
    config.autocomplete = boolValue(json, "autocomplete", config.autocomplete);
    return config;
}

void Config::saveDefaultIfMissing() const {
    const std::filesystem::path path = configPath();
    if (std::filesystem::exists(path)) {
        return;
    }

    std::ofstream out(path);
    out << "{\n"
        << "  \"host\": \"" << host << "\",\n"
        << "  \"port\": " << port << ",\n"
        << "  \"theme\": \"" << theme << "\",\n"
        << "  \"history_size\": " << historySize << ",\n"
        << "  \"autocomplete\": " << (autocomplete ? "true" : "false") << "\n"
        << "}\n";
}

} // namespace vaultdb::tui
