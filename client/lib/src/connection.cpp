#include "vaultdb/connection.hpp"

#include "json_utils.hpp"

#include <arpa/inet.h>
#include <sys/socket.h>
#include <unistd.h>

#include <cerrno>
#include <cstring>
#include <stdexcept>

namespace vaultdb {

Connection::Connection(const std::string& host, int port)
    : host_(host), port_(port), sockfd_(-1), requestId_(0) {}

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

    sockaddr_in addr {};
    addr.sin_family = AF_INET;
    addr.sin_port = htons(static_cast<uint16_t>(port_));

    if (::inet_pton(AF_INET, host_.c_str(), &addr.sin_addr) <= 0) {
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

Result Connection::execute(const std::string& sql) {
    if (!isConnected()) {
        throw std::runtime_error("not connected");
    }

    const std::string request = buildRequest(sql) + "\n";

    std::size_t sentTotal = 0;
    while (sentTotal < request.size()) {
        const ssize_t sent = ::send(
            sockfd_,
            request.data() + sentTotal,
            request.size() - sentTotal,
            0);
        if (sent <= 0) {
            throw std::runtime_error("send failed: " + std::string(std::strerror(errno)));
        }
        sentTotal += static_cast<std::size_t>(sent);
    }

    std::string response;
    response.reserve(2048);

    char buffer[4096];
    while (true) {
        const ssize_t readBytes = ::recv(sockfd_, buffer, sizeof(buffer), 0);
        if (readBytes <= 0) {
            throw std::runtime_error("connection closed by server");
        }

        response.append(buffer, static_cast<std::size_t>(readBytes));
        const std::size_t lineEnd = response.find('\n');
        if (lineEnd != std::string::npos) {
            response.resize(lineEnd);
            break;
        }
    }

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
