include(FetchContent)

find_package(ftxui CONFIG QUIET)
if(NOT TARGET ftxui::screen)
    set(FTXUI_ENABLE_INSTALL OFF CACHE BOOL "" FORCE)
    set(FTXUI_BUILD_EXAMPLES OFF CACHE BOOL "" FORCE)
    set(FTXUI_BUILD_DOCS OFF CACHE BOOL "" FORCE)
    set(FTXUI_BUILD_TESTS OFF CACHE BOOL "" FORCE)
    FetchContent_Declare(
        ftxui
        GIT_REPOSITORY https://github.com/ArthurSonzogni/FTXUI
        GIT_TAG        v5.0.0
        GIT_SHALLOW    TRUE
    )
    FetchContent_MakeAvailable(ftxui)
endif()

set(VAULTDB_TUI_SOURCES
    main.cpp
    app/app.cpp
    screens/splash_screen.cpp
    screens/main_screen.cpp
    screens/connection_error.cpp
    panels/header.cpp
    panels/status_bar.cpp
    panels/navigator.cpp
    panels/editor.cpp
    panels/results.cpp
    dialogs/create_db_dialog.cpp
    dialogs/create_table_dialog.cpp
    dialogs/confirm_dialog.cpp
    dialogs/help_screen.cpp
    dialogs/history_screen.cpp
    components/table_view.cpp
    components/row_detail.cpp
    components/tree_view.cpp
    components/autocomplete_popup.cpp
    logic/highlighter.cpp
    logic/autocomplete.cpp
    logic/history.cpp
    logic/config.cpp
    utils/string_utils.cpp
    utils/csv_exporter.cpp
)

add_executable(vaultdb-tui ${VAULTDB_TUI_SOURCES})

target_include_directories(vaultdb-tui
    PRIVATE
        ${CMAKE_CURRENT_SOURCE_DIR}
        ${CMAKE_CURRENT_SOURCE_DIR}/../lib/include
)

target_link_libraries(vaultdb-tui
    PRIVATE
        ftxui::screen
        ftxui::dom
        ftxui::component
        vaultdb
)
