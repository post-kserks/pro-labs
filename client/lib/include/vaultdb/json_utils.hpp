#pragma once

#include <cstdint>
#include <string>
#include <unordered_map>
#include <vector>

namespace vaultdb::json {

enum class Type {
    Null,
    Bool,
    Number,
    String,
    Array,
    Object,
};

struct Value {
    Type type = Type::Null;
    bool boolValue = false;
    double numberValue = 0.0;
    int64_t intValue = 0;
    bool numberIsInteger = false;
    bool useInt = false;
    std::string stringValue;
    std::vector<Value> arrayValue;
    std::unordered_map<std::string, Value> objectValue;

    bool isObject() const { return type == Type::Object; }
    bool isArray() const { return type == Type::Array; }

    int64_t toInt() const { return useInt ? intValue : static_cast<int64_t>(numberValue); }
    double toDouble() const { return useInt ? static_cast<double>(intValue) : numberValue; }
};

Value parse(const std::string& input);
std::string escape(const std::string& input);
std::string valueToString(const Value& value);

} // namespace vaultdb::json
