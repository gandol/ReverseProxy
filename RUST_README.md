# ReverseProxy - Rust Implementation

This is a Rust implementation of the ReverseProxy feature, providing the same command-line interface and functionality as the Go version.

## Building

To build the Rust version:

```bash
cargo build --release
```

The compiled binary will be available at `./target/release/proxyrust`.

## Usage

The Rust version maintains the same command-line interface as the Go version:

```bash
./target/release/proxyrust [OPTIONS]

Options:
  -l, --bind <BIND>                    Listen on ip:port [default: 0.0.0.0:8888]
  -r, --remote <REMOTE>                Reverse proxy addr [default: http://idea.lanyus.com:80]
      --ip <IP>                        Reverse proxy addr server ip [default: ]
  -h, --headers <HEADERS>              Custom headers to add to requests [default: ]
  -b, --blocked-files <BLOCKED_FILES>  Block file extensions (e.g., exe|zip) [default: ]
  -d, --daemon                         Run as daemon in background
      --socks <SOCKS_PROXY>            SOCKS proxy address [default: ]
      --help                           Print help information
```

## Examples

### Standard Mode (blocks terminal):
```bash
./target/release/proxyrust -l "0.0.0.0:8081" -r "https://www.baidu.com"
```

### Daemon Mode (runs in background):
```bash
./target/release/proxyrust -d -l "0.0.0.0:8081" -r "https://www.baidu.com"
```

### With SOCKS Proxy:
```bash
./target/release/proxyrust -l "0.0.0.0:8081" -r "https://www.baidu.com" --socks "socks5://127.0.0.1:1080"
```

### With Custom Headers and Blocked Files:
```bash
./target/release/proxyrust -l "0.0.0.0:8081" -r "https://example.com" -h "X-Custom-Header: value" -b "exe|zip"
```

## Features

All features from the Go version are implemented:

- ✅ Reverse proxy functionality
- ✅ Daily traffic statistics tracking with date separation
- ✅ Blocked requests counting
- ✅ Daemon mode for background operation
- ✅ SOCKS proxy support for upstream connections
- ✅ Server failover and load balancing
- ✅ Custom headers support
- ✅ File extension blocking
- ✅ Stats API endpoints:
  - `GET /stats` - Current day statistics
  - `GET /stats/history` - All historical statistics by date
  - `POST /stats/reset` - Reset current day statistics

## Statistics

Statistics are automatically saved to `proxy_stats.json` every minute with daily separation. The format is identical to the Go version.

### Accessing Statistics

#### Current Day Stats
Visit `http://your-proxy-host:port/stats` to get current day statistics:

```json
{
  "requests": 150,
  "incoming_gb": 0.0,
  "outgoing_gb": 0.23,
  "backend_gb": 0.12,
  "blocked_count": 5,
  "total_gb": 0.35
}
```

#### Historical Stats
Visit `http://your-proxy-host:port/stats/history` to get all historical statistics.

#### Reset Statistics
```bash
curl -X POST http://your-proxy-host:port/stats/reset
```

## SOCKS Proxy Support

The Rust version supports both URL format and custom format for SOCKS proxy configuration:

**URL Format:**
- `socks5://username:password@host:port` - SOCKS5 with authentication
- `socks5://host:port` - SOCKS5 without authentication
- `socks4://host:port` - SOCKS4 proxy

**Custom Format:**
- `socks5:host:port:username:password` - SOCKS5 with authentication
- `socks4:host:port:username:password` - SOCKS4 with authentication

## Performance

The Rust implementation is expected to have:
- Lower memory footprint
- Better performance under high load
- Improved safety due to Rust's memory safety guarantees

## Differences from Go Version

While the functionality is identical, there are some internal implementation differences:

1. **Async Runtime**: Uses Tokio for async operations
2. **HTTP Client**: Uses reqwest for HTTP client functionality
3. **Command-line Parsing**: Uses clap instead of the flag package
4. **Daemon Mode**: Uses daemonize crate instead of exec.Command

All external behavior and APIs remain the same.
