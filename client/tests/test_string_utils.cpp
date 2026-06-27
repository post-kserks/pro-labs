#include <gtest/gtest.h>
#include <vaultdb/string_utils.hpp>

using namespace vaultdb;

TEST(StringUtils, TrimLeadingSpaces) {
    EXPECT_EQ(trim("  hello"), "hello");
}

TEST(StringUtils, TrimTrailingSpaces) {
    EXPECT_EQ(trim("hello  "), "hello");
}

TEST(StringUtils, TrimBothSides) {
    EXPECT_EQ(trim("  hello  "), "hello");
}

TEST(StringUtils, TrimNoSpaces) {
    EXPECT_EQ(trim("hello"), "hello");
}

TEST(StringUtils, TrimEmpty) {
    EXPECT_EQ(trim(""), "");
}

TEST(StringUtils, TrimTabs) {
    EXPECT_EQ(trim("\t\nhello\r\n"), "hello");
}

TEST(StringUtils, ToLower) {
    EXPECT_EQ(toLower("Hello World"), "hello world");
}

TEST(StringUtils, ToLowerEmpty) {
    EXPECT_EQ(toLower(""), "");
}

TEST(StringUtils, ToLowerAlready) {
    EXPECT_EQ(toLower("hello"), "hello");
}

TEST(StringUtils, EnsureSemicolonAdds) {
    EXPECT_EQ(ensureSemicolon("SELECT 1"), "SELECT 1;");
}

TEST(StringUtils, EnsureSemicolonKeeps) {
    EXPECT_EQ(ensureSemicolon("SELECT 1;"), "SELECT 1;");
}

TEST(StringUtils, EnsureSemicolonEmpty) {
    EXPECT_EQ(ensureSemicolon(""), "");
}
