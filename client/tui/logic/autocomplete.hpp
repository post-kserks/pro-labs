#pragma once

#include <cstddef>
#include <string>
#include <vector>

namespace pixeldb::tui {

struct CompletionContext {
    std::vector<std::string> tables;
    std::vector<std::string> columns;
    bool enabled = true;
};

class Autocomplete {
public:
    std::vector<std::string> suggestions(const std::string& sql,
                                         std::size_t cursor,
                                         const CompletionContext& context) const;

    std::string apply(const std::string& sql,
                      std::size_t cursor,
                      const std::string& completion,
                      std::size_t& newCursor) const;

private:
    std::vector<std::string> keywordSuggestions(const std::string& prefix) const;
};

} // namespace pixeldb::tui
