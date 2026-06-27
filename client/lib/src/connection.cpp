#include "vaultdb/connection.hpp"

#include "json_utils.hpp"

#ifdef _WIN32
    #include <winsock2.h>
    #include <ws2tcpip.h>
    #pragma comment(lib, "ws2_32.lib")
    #define CLOSE_SOCKET closesocket
    #define SOCK_ERR WSAGetLastError()
    #define EINTR_ERR WSAEINTR
    #define EAGAIN_ERR WSAEWOULDBLOCK
    typedef SSIZE_T ssize_t;
#else
    #include <arpa/inet.h>
    #include <sys/socket.h>
    #include <netinet/tcp.h>
    #include <unistd.h>
    #define CLOSE_SOCKET close
    #define SOCK_ERR errno
    #define EINTR_ERR EINTR
    #define EAGAIN_ERR EAGAIN
    #define INVALID_SOCKET -1
#endif

#include <openssl/ssl.h>
#include <openssl/err.h>
#include <openssl/x509v3.h>

#include <cerrno>
#include <cstring>
#include <stdexcept>

namespace vaultdb {

namespace {

struct SSLContextDeleter {
    void operator()(SSL_CTX* ctx) const { SSL_CTX_free(ctx); }
};
struct SSLDeleter {
    void operator()(SSL* ssl) const { SSL_free(ssl); }
};

} // namespace

Connection::Connection(const ConnectionOptions& opts)
    : opts_(opts), sockfd_(INVALID_SOCKET), requestId_(0) {}

Connection::Connection(const std::string& host, int port)
    : sockfd_(INVALID_SOCKET), requestId_(0) {
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

#ifdef _WIN32
    WSADATA wsaData;
    if (WSAStartup(MAKEWORD(2, 2), &wsaData) != 0) {
        return false;
    }
#endif

    sockfd_ = ::socket(AF_INET, SOCK_STREAM, 0);
    if (sockfd_ == INVALID_SOCKET) {
#ifdef _WIN32
        WSACleanup();
#endif
        return false;
    }

    // Таймаут на чтение и запись
#ifdef _WIN32
    DWORD timeout = opts_.timeout_ms;
    if (::setsockopt(sockfd_, SOL_SOCKET, SO_RCVTIMEO, (const char*)&timeout, sizeof(timeout)) != 0) {
        CLOSE_SOCKET(sockfd_);
        sockfd_ = INVALID_SOCKET;
        return false;
    }
    if (::setsockopt(sockfd_, SOL_SOCKET, SO_SNDTIMEO, (const char*)&timeout, sizeof(timeout)) != 0) {
        CLOSE_SOCKET(sockfd_);
        sockfd_ = INVALID_SOCKET;
        return false;
    }
#else
    struct timeval tv{};
    tv.tv_sec  = opts_.timeout_ms / 1000;
    tv.tv_usec = (opts_.timeout_ms % 1000) * 1000;
    if (::setsockopt(sockfd_, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof(tv)) != 0) {
        CLOSE_SOCKET(sockfd_);
        sockfd_ = INVALID_SOCKET;
        return false;
    }
    if (::setsockopt(sockfd_, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof(tv)) != 0) {
        CLOSE_SOCKET(sockfd_);
        sockfd_ = INVALID_SOCKET;
        return false;
    }
#endif

    // TCP_NODELAY: отключить алгоритм Nagle для низкой латентности
    int flag = 1;
    if (::setsockopt(sockfd_, IPPROTO_TCP, TCP_NODELAY, (const char*)&flag, sizeof(flag)) != 0) {
        CLOSE_SOCKET(sockfd_);
        sockfd_ = INVALID_SOCKET;
        return false;
    }

    sockaddr_in addr {};
    addr.sin_family = AF_INET;
    addr.sin_port = htons(static_cast<uint16_t>(opts_.port));

    if (::inet_pton(AF_INET, opts_.host.c_str(), &addr.sin_addr) <= 0) {
        CLOSE_SOCKET(sockfd_);
        sockfd_ = INVALID_SOCKET;
#ifdef _WIN32
        WSACleanup();
#endif
        return false;
    }

    if (::connect(sockfd_, reinterpret_cast<sockaddr*>(&addr), sizeof(addr)) != 0) {
        CLOSE_SOCKET(sockfd_);
        sockfd_ = INVALID_SOCKET;
#ifdef _WIN32
        WSACleanup();
#endif
        return false;
    }

    // TLS handshake если включён
    if (opts_.useTls) {
        ctx_ = SSL_CTX_new(TLS_client_method());
        if (!ctx_) {
            CLOSE_SOCKET(sockfd_);
            sockfd_ = INVALID_SOCKET;
            return false;
        }

        if (!opts_.tlsCaFile.empty()) {
            if (SSL_CTX_load_verify_locations(ctx_, opts_.tlsCaFile.c_str(), nullptr) != 1) {
                SSL_CTX_free(ctx_);
                ctx_ = nullptr;
                CLOSE_SOCKET(sockfd_);
                sockfd_ = INVALID_SOCKET;
                return false;
            }
        }

        if (!opts_.tlsCertFile.empty() && !opts_.tlsKeyFile.empty()) {
            if (SSL_CTX_use_certificate_file(ctx_, opts_.tlsCertFile.c_str(), SSL_FILETYPE_PEM) != 1) {
                SSL_CTX_free(ctx_);
                ctx_ = nullptr;
                CLOSE_SOCKET(sockfd_);
                sockfd_ = INVALID_SOCKET;
                return false;
            }
            if (SSL_CTX_use_PrivateKey_file(ctx_, opts_.tlsKeyFile.c_str(), SSL_FILETYPE_PEM) != 1) {
                SSL_CTX_free(ctx_);
                ctx_ = nullptr;
                CLOSE_SOCKET(sockfd_);
                sockfd_ = INVALID_SOCKET;
                return false;
            }
        }

        ssl_ = SSL_new(ctx_);
        if (!ssl_) {
            SSL_CTX_free(ctx_);
            ctx_ = nullptr;
            CLOSE_SOCKET(sockfd_);
            sockfd_ = INVALID_SOCKET;
            return false;
        }
        SSL_set_fd(ssl_, static_cast<int>(sockfd_));
        SSL_set_tlsext_host_name(ssl_, opts_.host.c_str());

        if (SSL_connect(ssl_) <= 0) {
            SSL_free(ssl_);
            ssl_ = nullptr;
            SSL_CTX_free(ctx_);
            ctx_ = nullptr;
            CLOSE_SOCKET(sockfd_);
            sockfd_ = INVALID_SOCKET;
            return false;
        }
    }

    return true;
}

void Connection::disconnect() {
    if (ssl_) {
        SSL_shutdown(ssl_);
        SSL_free(ssl_);
        ssl_ = nullptr;
    }
    if (ctx_) {
        SSL_CTX_free(ctx_);
        ctx_ = nullptr;
    }
    if (sockfd_ != INVALID_SOCKET) {
        CLOSE_SOCKET(sockfd_);
        sockfd_ = INVALID_SOCKET;
    }
#ifdef _WIN32
    WSACleanup();
#endif
}

bool Connection::isConnected() const {
    return sockfd_ != INVALID_SOCKET;
}

void Connection::sendPacket(const std::string& data) {
    size_t total_sent = 0;
    const char* ptr   = data.c_str();
    size_t remaining  = data.size();

    while (remaining > 0) {
        ssize_t n;
        if (ssl_) {
            n = SSL_write(ssl_, ptr + total_sent,
                          remaining > static_cast<size_t>(INT_MAX) ? INT_MAX : static_cast<int>(remaining));
        } else {
            n = ::send(sockfd_, ptr + total_sent,
                       remaining > static_cast<size_t>(INT_MAX) ? INT_MAX : static_cast<int>(remaining),
#if defined(MSG_NOSIGNAL)
                       MSG_NOSIGNAL
#else
                       0
#endif
            );
        }

        if (n < 0) {
            if (SOCK_ERR == EINTR_ERR) continue;
            throw NetworkError(
                std::string("send failed: ") + std::to_string(SOCK_ERR));
        }
        if (n == 0) {
            throw NetworkError("send returned 0 (connection closed)");
        }

        total_sent += static_cast<size_t>(n);
        remaining  -= static_cast<size_t>(n);
    }
}

std::string Connection::recvPacket() {
    constexpr size_t kRecvBufSize = 4096;

    while (true) {
        size_t nl_pos = buffer_.find('\n');
        if (nl_pos != std::string::npos) {
            std::string packet = buffer_.substr(0, nl_pos);
            buffer_.erase(0, nl_pos + 1);
            return packet;
        }

        char buf[kRecvBufSize];
        ssize_t n;
        if (ssl_) {
            n = SSL_read(ssl_, buf, sizeof(buf));
        } else {
            n = ::recv(sockfd_, buf, sizeof(buf), 0);
        }

        if (n < 0) {
            int err = SOCK_ERR;
            if (err == EINTR_ERR) {
                continue;
            }
            if (err == EAGAIN_ERR) {
                throw NetworkError("recv timeout: server did not respond");
            }
            throw NetworkError(
                std::string("recv failed: ") + std::to_string(err));
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
        result.affected = static_cast<int>(affected->toInt());
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
