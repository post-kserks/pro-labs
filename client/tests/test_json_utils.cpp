#include <gtest/gtest.h>
#include <vaultdb/json_utils.hpp>

using namespace vaultdb::json;

TEST(JsonParse, SimpleObject) {
    Value v = parse(R"({"key":"value"})");
    EXPECT_TRUE(v.isObject());
    EXPECT_EQ(v.objectValue.at("key").stringValue, "value");
}

TEST(JsonParse, NestedObject) {
    Value v = parse(R"({"a":{"b":"c"}})");
    EXPECT_TRUE(v.isObject());
    EXPECT_EQ(v.objectValue.at("a").objectValue.at("b").stringValue, "c");
}

TEST(JsonParse, Array) {
    Value v = parse(R"([1,2,3])");
    EXPECT_TRUE(v.isArray());
    EXPECT_EQ(v.arrayValue.size(), 3u);
    EXPECT_EQ(v.arrayValue[0].toInt(), 1);
}

TEST(JsonParse, EmptyString) {
    EXPECT_THROW(parse(""), std::runtime_error);
}

TEST(JsonParse, MalformedJson) {
    EXPECT_THROW(parse("{invalid}"), std::runtime_error);
}

TEST(JsonParse, BooleanValues) {
    Value t = parse("true");
    EXPECT_EQ(t.type, Type::Bool);
    EXPECT_TRUE(t.boolValue);

    Value f = parse("false");
    EXPECT_EQ(f.type, Type::Bool);
    EXPECT_FALSE(f.boolValue);
}

TEST(JsonParse, NullValue) {
    Value v = parse("null");
    EXPECT_EQ(v.type, Type::Null);
}

TEST(JsonParse, IntegerNumber) {
    Value v = parse("42");
    EXPECT_EQ(v.type, Type::Number);
    EXPECT_EQ(v.toInt(), 42);
}

TEST(JsonParse, FloatNumber) {
    Value v = parse("3.14");
    EXPECT_EQ(v.type, Type::Number);
    EXPECT_DOUBLE_EQ(v.toDouble(), 3.14);
}

TEST(JsonParse, NegativeNumber) {
    Value v = parse("-5");
    EXPECT_EQ(v.toInt(), -5);
}

TEST(JsonParse, EscapedString) {
    Value v = parse(R"("hello\nworld")");
    EXPECT_EQ(v.stringValue, "hello\nworld");
}

TEST(JsonParse, DeepNesting) {
    std::string deep = "{\"a\":";
    for (int i = 0; i < 5; i++) deep += "{\"b\":";
    deep += "1";
    for (int i = 0; i < 5; i++) deep += "}";
    deep += "}";
    Value v = parse(deep);
    EXPECT_TRUE(v.isObject());
}

TEST(JsonEscape, SpecialChars) {
    std::string result = escape("hello\"world\\");
    EXPECT_EQ(result, "hello\\\"world\\\\");
}

TEST(JsonEscape, EmptyString) {
    EXPECT_EQ(escape(""), "");
}
