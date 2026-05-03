cmake_minimum_required(VERSION 3.20)
project(pixeldb_client VERSION 1.0.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
set(CMAKE_SKIP_INSTALL_RPATH TRUE)

add_library(pixeldb SHARED
    lib/src/connection.cpp
    lib/src/result.cpp
    lib/src/json_utils.cpp
)

target_include_directories(pixeldb
    PUBLIC
        lib/include
    PRIVATE
        lib/src
)

add_executable(pixeldb-shell
    shell/main.cpp
    shell/renderer.cpp
)

target_link_libraries(pixeldb-shell PRIVATE pixeldb)

target_include_directories(pixeldb-shell PRIVATE lib/include)

add_subdirectory(tui)

install(TARGETS pixeldb pixeldb-shell pixeldb-tui
    LIBRARY DESTINATION ${CMAKE_BINARY_DIR}/output
    RUNTIME DESTINATION ${CMAKE_BINARY_DIR}/output
    ARCHIVE DESTINATION ${CMAKE_BINARY_DIR}/output
)
