#include "vaultdb/connection.hpp"

#include "json_utils.hpp"

#include <arpa/inet.h>
#include <sys/socket.h>
#include <netinet/tcp.h>
#include <unistd.h>

#include <cerrno>
#include <cstring>
#include <stdexcept>

namespace vaultdb {

Connection::Connection(const ConnectionOptions& opts)
    : opts_(opts), sockfd_(-1), requestId_(0) {}

Connection::Connection(const std::string& host, int port)
    : sockfd_(-1), requestId_(0) {
    opts_.host = host;
    opts_.port = port;
}

Connection::~Connection() {
    disconnect();
}

bool Connection::connect() {
    if (isConnected()) {
        return true;
    }

    sockfd_ = ::socket(AF_INET, SOCK_STREAM, 0);
    if (sockfd_ < 0) {
        return false;
    }

    // Таймаут на чтение и запись
    struct timeval tv{};
    tv.tv_sec  = opts_.timeout_ms / 1000;
    tv.tv_usec = (opts_.timeout_ms % 1000) * 1000;

    ::setsockopt(sockfd_, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof(tv));
    ::setsockopt(sockfd_, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof(tv));

    // TCP_NODELAY: отключить алгоритм Nagle для низкой латентности
    int flag = 1;
    ::setsockopt(sockfd_, IPPROTO_TCP, TCP_NODELAY, &flag, sizeof(flag));

    sockaddr_in addr {};
    addr.sin_family = AF_INET;
    addr.sin_port = htons(static_cast<uint16_t>(opts_.port));

    if (::inet_pton(AF_INET, opts_.host.c_str(), &addr.sin_addr) <= 0) {
        ::close(sockfd_);
        sockfd_ = -1;
        return false;
    }

    if (::connect(sockfd_, reinterpret_cast<sockaddr*>(&addr), sizeof(addr)) != 0) {
        ::close(sockfd_);
        sockfd_ = -1;
        return false;
    }

    return true;
}

void Connection::disconnect() {
    if (!isConnected()) {
        return;
    }

    ::close(sockfd_);
    sockfd_ = -1;
}

bool Connection::isConnected() const {
    return sockfd_ >= 0;
}

void Connection::sendPacket(const std::string& data) {
    size_t total_sent = 0;
    const char* ptr   = data.c_str();
    size_t remaining  = data.size();

    while (remaining > 0) {
        ssize_t n = ::send(sockfd_, ptr + total_sent, remaining,
#ifdef MSG_NOSIGNAL
                           MSG_NOSIGNAL
#else
                           0
#endif
        );

        if (n < 0) {
            if (errno == EINTR) continue;
            throw NetworkError(
                std::string("send failed: ") + strerror(errno));
        }

        total_sent += static_cast<size_t>(n);
        remaining  -= static_cast<size_t>(n);
    }
}

std::string Connection::recvPacket() {
    while (true) {
        // Check if we already have a newline in our internal buffer
        size_t nl_pos = buffer_.find('\n');
        if (nl_pos != std::string::npos) {
            std::string packet = buffer_.substr(0, nl_pos);
            buffer_.erase(0, nl_pos + 1);
            return packet;
        }

        char buf[4096];
        ssize_t n = ::recv(sockfd_, buf, sizeof(buf), 0);

        if (n < 0) {
            if (errno == EINTR) {
                continue;
            }
            if (errno == EAGAIN || errno == EWOULDBLOCK) {
                throw NetworkError("recv timeout: server did not respond");
            }
            throw NetworkError(
                std::string("recv failed: ") + strerror(errno));
        }

        if (n == 0) {
            throw NetworkError("connection closed by server");
        }

        buffer_.append(buf, static_cast<size_t>(n));

        constexpr size_t MAX_RESPONSE_SIZE = 64 * 1024 * 1024;
        if (buffer_.size() > MAX_RESPONSE_SIZE) {
            throw NetworkError("response too large (> 64 MB)");
        }
    }
}

Result Connection::execute(const std::string& sql) {
    if (!isConnected()) {
        throw NetworkError("not connected");
    }

    const std::string request = buildRequest(sql) + "\n";
    sendPacket(request);
    
    std::string response = recvPacket();
    return parseResponse(response);
}

std::string Connection::buildRequest(const std::string& sql) {
    const std::string escapedSql = json::escape(sql);
    return "{\"id\":\"" + std::to_string(++requestId_) + "\",\"query\":\"" + escapedSql + "\"}";
}

Result Connection::parseResponse(const std::string& rawJson) {
    const json::Value root = json::parse(rawJson);
    if (!root.isObject()) {
        throw std::runtime_error("invalid JSON response: root is not an object");
    }

    Result result;

    const auto findField = [&](const std::string& key) -> const json::Value* {
        const auto it = root.objectValue.find(key);
        if (it == root.objectValue.end()) {
            return nullptr;
        }
        return &it->second;
    };

    if (const json::Value* status = findField("status"); status != nullptr && status->type == json::Type::String) {
        result.success = status->stringValue == "ok";
    }

    if (const json::Value* type = findField("type"); type != nullptr && type->type == json::Type::String) {
        result.type = type->stringValue;
    }

    if (const json::Value* affected = findField("affected");
        affected != nullptr && affected->type == json::Type::Number) {
        result.affected = static_cast<int>(affected->numberValue);
    }

    if (const json::Value* message = findField("message"); message != nullptr && message->type == json::Type::String) {
        result.message = message->stringValue;
    }
    if (const json::Value* asOf = findField("as_of_note"); asOf != nullptr && asOf->type == json::Type::String) {
        result.asOfNote = asOf->stringValue;
    }

    if (const json::Value* columns = findField("columns"); columns != nullptr && columns->isArray()) {
        result.columns.reserve(columns->arrayValue.size());
        for (const json::Value& value : columns->arrayValue) {
            if (value.type == json::Type::String) {
                result.columns.push_back(value.stringValue);
            } else {
                result.columns.push_back(json::valueToString(value));
            }
        }
    }

    if (const json::Value* rows = findField("rows"); rows != nullptr && rows->isArray()) {
        result.rows.reserve(rows->arrayValue.size());
        for (const json::Value& row : rows->arrayValue) {
            std::vector<std::string> parsedRow;
            if (row.isArray()) {
                parsedRow.reserve(row.arrayValue.size());
                for (const json::Value& value : row.arrayValue) {
                    parsedRow.push_back(json::valueToString(value));
                }
            }
            result.rows.push_back(std::move(parsedRow));
        }
    }

    if (result.type == "error") {
        result.success = false;
    }

    return result;
}

} // namespace vaultdb
