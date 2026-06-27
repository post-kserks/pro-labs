cmake_minimum_required(VERSION 3.20)
project(vaultdb_client VERSION 1.2.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
set(CMAKE_SKIP_INSTALL_RPATH TRUE)
set(CMAKE_WINDOWS_EXPORT_ALL_SYMBOLS ON)

find_package(OpenSSL REQUIRED)

add_library(vaultdb SHARED
    lib/src/connection.cpp
    lib/src/result.cpp
    lib/src/json_utils.cpp
)

target_include_directories(vaultdb
    PUBLIC
        lib/include
    PRIVATE
        lib/src
)

target_link_libraries(vaultdb
    PUBLIC
        OpenSSL::SSL
        OpenSSL::Crypto
)

add_executable(vaultdb-shell
    shell/main.cpp
    shell/renderer.cpp
)

target_link_libraries(vaultdb-shell PRIVATE vaultdb)

target_include_directories(vaultdb-shell PRIVATE lib/include)

add_subdirectory(tui)
add_subdirectory(tests)

install(TARGETS vaultdb vaultdb-shell vaultdb-tui
    LIBRARY DESTINATION ${CMAKE_BINARY_DIR}/output
    RUNTIME DESTINATION ${CMAKE_BINARY_DIR}/output
    ARCHIVE DESTINATION ${CMAKE_BINARY_DIR}/output
)
