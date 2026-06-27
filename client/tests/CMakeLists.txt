cmake_minimum_required(VERSION 3.20)
project(vaultdb-tests LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

enable_testing()
find_package(GTest QUIET)

if(GTest_FOUND)
    add_executable(vaultdb-tests
        test_json_utils.cpp
        test_string_utils.cpp
    )
    target_link_libraries(vaultdb-tests PRIVATE GTest::gtest_main vaultdb)
    target_include_directories(vaultdb-tests PRIVATE ${CMAKE_SOURCE_DIR}/lib/include)
    add_test(NAME vaultdb-tests COMMAND vaultdb-tests)
else()
    message(STATUS "GoogleTest not found, skipping tests")
endif()
