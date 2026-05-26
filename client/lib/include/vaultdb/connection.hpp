#pragma once

#include "result.hpp"

#include <cstdint>
#include <string>
#include <stdexcept>

#ifdef _WIN32
    #ifndef WIN32_LEAN_AND_MEAN
        #define WIN32_LEAN_AND_MEAN
    #endif
    #include <winsock2.h>
    #include <ws2tcpip.h>
    typedef SOCKET socket_t;
#else
    typedef int socket_t;
#endif

namespace vaultdb {

class NetworkError : public std::runtime_error {
public:
    explicit NetworkError(const std::string& message) : std::runtime_error(message) {}
};

struct ConnectionOptions {
    std::string host = "127.0.0.1";
    int port = 5432;
    std::string token;
    int timeout_ms = 5000;
};

class Connection {
public:
    explicit Connection(const ConnectionOptions& opts);
    Connection(const std::string& host, int port);
    ~Connection();

    Connection(const Connection&) = delete;
    Connection& operator=(const Connection&) = delete;

    bool connect();
    void disconnect();
    bool isConnected() const;

    Result execute(const std::string& sql);

private:
    ConnectionOptions opts_;
    socket_t sockfd_;
    std::uint64_t requestId_;
    std::string buffer_;

    void sendPacket(const std::string& data);
    std::string recvPacket();

    std::string buildRequest(const std::string& sql);
    Result parseResponse(const std::string& json);
};

} // namespace vaultdb
