#include "json_utils.hpp"

#include <cmath>
#include <cstddef>
#include <iomanip>
#include <sstream>
#include <stdexcept>

namespace vaultdb::json {
namespace {

class Parser {
public:
    explicit Parser(const std::string& input) : input_(input) {}

    Value parse() {
        skipWhitespace();
        Value value = parseValue();
        skipWhitespace();
        if (!eof()) {
            throw std::runtime_error("unexpected trailing characters in JSON");
        }
        return value;
    }

private:
    static constexpr std::size_t kMaxDepth = 128;

    const std::string& input_;
    std::size_t pos_ = 0;
    std::size_t depth_ = 0;

    Value parseValue() {
        if (eof()) {
            throw std::runtime_error("unexpected end of JSON");
        }

        switch (peek()) {
        case '{':
            return parseObject();
        case '[':
            return parseArray();
        case '"':
            return makeString(parseString());
        case 't':
            consumeLiteral("true");
            return makeBool(true);
        case 'f':
            consumeLiteral("false");
            return makeBool(false);
        case 'n':
            consumeLiteral("null");
            return makeNull();
        default:
            if (peek() == '-' || isDigit(peek())) {
                return parseNumber();
            }
            throw std::runtime_error("invalid JSON value");
        }
    }

    Value parseObject() {
        if (depth_ >= kMaxDepth) {
            throw std::runtime_error("JSON nesting depth exceeds limit");
        }
        ++depth_;
        consume('{');

        Value object;
        object.type = Type::Object;

        skipWhitespace();
        if (peek() == '}') {
            consume('}');
            return object;
        }

        while (true) {
            skipWhitespace();
            if (peek() == '}') {
                consume('}');
                --depth_;
                break;
            }
            consume(',');
            skipWhitespace();
        }

        return object;
    }

    Value parseArray() {
        if (depth_ >= kMaxDepth) {
            throw std::runtime_error("JSON nesting depth exceeds limit");
        }
        ++depth_;
        consume('[');

        Value array;
        array.type = Type::Array;

        skipWhitespace();
        if (peek() == ']') {
            consume(']');
            return array;
        }

        while (true) {
            skipWhitespace();
            array.arrayValue.push_back(parseValue());

            skipWhitespace();
            if (peek() == ']') {
                consume(']');
                --depth_;
                break;
            }
            consume(',');
            skipWhitespace();
        }

        return array;
    }

    std::string parseString() {
        consume('"');
        std::string out;

        while (!eof()) {
            char ch = next();
            if (ch == '"') {
                return out;
            }
            if (ch == '\\') {
                if (eof()) {
                    throw std::runtime_error("unfinished escape in JSON string");
                }
                char esc = next();
                switch (esc) {
                case '"':
                    out.push_back('"');
                    break;
                case '\\':
                    out.push_back('\\');
                    break;
                case '/':
                    out.push_back('/');
                    break;
                case 'b':
                    out.push_back('\b');
                    break;
                case 'f':
                    out.push_back('\f');
                    break;
                case 'n':
                    out.push_back('\n');
                    break;
                case 'r':
                    out.push_back('\r');
                    break;
                case 't':
                    out.push_back('\t');
                    break;
                case 'u':
                    throw std::runtime_error("unicode escapes are not supported by this JSON parser");
                default:
                    throw std::runtime_error("invalid escape sequence in JSON string");
                }
                continue;
            }
            out.push_back(ch);
        }

        throw std::runtime_error("unterminated JSON string");
    }

    Value parseNumber() {
        std::size_t start = pos_;

        if (peek() == '-') {
            next();
        }

        consumeDigits();

        bool isInteger = true;
        if (!eof() && peek() == '.') {
            isInteger = false;
            next();
            consumeDigits();
        }

        if (!eof() && (peek() == 'e' || peek() == 'E')) {
            isInteger = false;
            next();
            if (!eof() && (peek() == '+' || peek() == '-')) {
                next();
            }
            consumeDigits();
        }

        const std::string token = input_.substr(start, pos_ - start);
        Value number;
        number.type = Type::Number;
        if (isInteger) {
            try {
                number.intValue = std::stoll(token);
                number.useInt = true;
            } catch (...) {
                number.numberValue = std::stod(token);
            }
        } else {
            number.numberValue = std::stod(token);
        }
        number.numberIsInteger = isInteger;
        return number;
    }

    void consumeDigits() {
        if (eof() || !isDigit(peek())) {
            throw std::runtime_error("expected JSON digit");
        }
        while (!eof() && isDigit(peek())) {
            next();
        }
    }

    void consumeLiteral(const char* literal) {
        for (const char* p = literal; *p != '\0'; ++p) {
            if (eof() || next() != *p) {
                throw std::runtime_error("invalid JSON literal");
            }
        }
    }

    void consume(char expected) {
        if (eof() || next() != expected) {
            throw std::runtime_error("unexpected JSON token");
        }
    }

    void skipWhitespace() {
        while (!eof()) {
            const char ch = peek();
            if (ch == ' ' || ch == '\n' || ch == '\r' || ch == '\t') {
                ++pos_;
                continue;
            }
            break;
        }
    }

    char peek() const {
        if (eof()) {
            return '\0';
        }
        return input_[pos_];
    }

    char next() {
        if (eof()) {
            return '\0';
        }
        return input_[pos_++];
    }

    bool eof() const {
        return pos_ >= input_.size();
    }

    static bool isDigit(char ch) {
        return ch >= '0' && ch <= '9';
    }

    static Value makeNull() {
        Value value;
        value.type = Type::Null;
        return value;
    }

    static Value makeBool(bool raw) {
        Value value;
        value.type = Type::Bool;
        value.boolValue = raw;
        return value;
    }

    static Value makeString(std::string raw) {
        Value value;
        value.type = Type::String;
        value.stringValue = std::move(raw);
        return value;
    }
};

} // namespace

Value parse(const std::string& input) {
    Parser parser(input);
    return parser.parse();
}

std::string escape(const std::string& input) {
    std::string escaped;
    escaped.reserve(input.size() + 8);

    for (char ch : input) {
        switch (ch) {
        case '"':
            escaped += "\\\"";
            break;
        case '\\':
            escaped += "\\\\";
            break;
        case '\n':
            escaped += "\\n";
            break;
        case '\r':
            escaped += "\\r";
            break;
        case '\t':
            escaped += "\\t";
            break;
        default:
            escaped.push_back(ch);
            break;
        }
    }

    return escaped;
}

std::string valueToString(const Value& value) {
    switch (value.type) {
    case Type::Null:
        return "NULL";
    case Type::Bool:
        return value.boolValue ? "true" : "false";
    case Type::Number: {
        if (value.useInt) {
            return std::to_string(value.intValue);
        }
        if (value.numberIsInteger && std::floor(value.numberValue) == value.numberValue) {
            long long number = static_cast<long long>(value.numberValue);
            return std::to_string(number);
        }
        std::ostringstream ss;
        ss << std::setprecision(15) << value.numberValue;
        return ss.str();
    }
    case Type::String:
        return value.stringValue;
    case Type::Array:
        return "[array]";
    case Type::Object:
        return "[object]";
    }

    return {};
}

} // namespace vaultdb::json
