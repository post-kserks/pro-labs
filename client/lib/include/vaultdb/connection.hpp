#pragma once

#include "result.hpp"

#include <cstdint>
#include <string>

namespace vaultdb {

class Connection {
public:
    Connection(const std::string& host, int port);
    ~Connection();

    Connection(const Connection&) = delete;
    Connection& operator=(const Connection&) = delete;

    bool connect();
    void disconnect();
    bool isConnected() const;

    Result execute(const std::string& sql);

private:
    std::string host_;
    int port_;
    int sockfd_;
    std::uint64_t requestId_;

    std::string buildRequest(const std::string& sql);
    Result parseResponse(const std::string& json);
};

} // namespace vaultdb
