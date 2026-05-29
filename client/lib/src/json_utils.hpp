#pragma once

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
    bool numberIsInteger = false;
    std::string stringValue;
    std::vector<Value> arrayValue;
    std::unordered_map<std::string, Value> objectValue;

    bool isObject() const { return type == Type::Object; }
    bool isArray() const { return type == Type::Array; }
};

Value parse(const std::string& input);
std::string escape(const std::string& input);
std::string valueToString(const Value& value);

} // namespace vaultdb::json
