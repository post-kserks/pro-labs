cmake_minimum_required(VERSION 3.20)
project(vaultdb_client VERSION 1.0.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
set(CMAKE_SKIP_INSTALL_RPATH TRUE)

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

add_executable(vaultdb-shell
    shell/main.cpp
    shell/renderer.cpp
)

target_link_libraries(vaultdb-shell PRIVATE vaultdb)

target_include_directories(vaultdb-shell PRIVATE lib/include)

add_subdirectory(tui)

install(TARGETS vaultdb vaultdb-shell vaultdb-tui
    LIBRARY DESTINATION ${CMAKE_BINARY_DIR}/output
    RUNTIME DESTINATION ${CMAKE_BINARY_DIR}/output
    ARCHIVE DESTINATION ${CMAKE_BINARY_DIR}/output
)
