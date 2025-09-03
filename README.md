# ReverseProxy
ReverseProxy in golang with traffic calculation and daily statistics

## Features:
- Reverse proxy functionality
- **Daily traffic statistics tracking with date separation**
- **Blocked requests counting**
- **Daemon mode for background operation**
- **SOCKS proxy support for upstream connections**
- Server failover and load balancing
- Custom headers support
- File extension blocking

## Use:

	./proxyGolang -h
	
	Usage of proxyGolang:
	  -l string
	        listen on ip:port (default "0.0.0.0:8888")
	  -r string
	        reverse proxy addr (default "http://idea.lanyus.com:80")
	  -h string
	        custom headers to add to requests
	  -b string
	        block file extensions (e.g., exe|zip)
	  -d bool
	        run as daemon in background (default false)
	  -socks string
	        SOCKS proxy address (e.g., socks5://127.0.0.1:1080)

### Standard Mode (blocks terminal):
	./proxyGolang -l "0.0.0.0:8081" -r "https://www.baidu.com"

### Daemon Mode (runs in background):
	./proxyGolang -d -l "0.0.0.0:8081" -r "https://www.baidu.com"

### With SOCKS Proxy:
	./proxyGolang -l "0.0.0.0:8081" -r "https://www.baidu.com" -socks "socks5://127.0.0.1:1080"

## SOCKS Proxy Support

The reverse proxy now supports routing upstream connections through a SOCKS proxy. This feature allows you to:

- Route backend connections through SOCKS4, SOCKS4a, or SOCKS5 proxies
- Use SOCKS proxy for better network routing or privacy
- Access geo-restricted backend services
- Work with existing SOCKS proxy infrastructure

### SOCKS Proxy URL Formats:

**Standard URL Format:**
- `socks5://username:password@host:port` - SOCKS5 with authentication
- `socks5://host:port` - SOCKS5 without authentication  
- `socks4://host:port` - SOCKS4 proxy
- `socks4a://host:port` - SOCKS4a proxy

**Custom Format:**
- `socks5:host:port:username:password` - SOCKS5 with authentication
- `socks4:host:port:username:password` - SOCKS4 with authentication
- `socks4a:host:port:username:password` - SOCKS4a with authentication

### Examples:
```bash
# Standard URL format
./proxyGolang -l "0.0.0.0:8081" -r "https://example.com" -socks "socks5://user:pass@127.0.0.1:1080"

# Custom format (useful for proxy services)
./proxyGolang -l "0.0.0.0:8081" -r "https://example.com" -socks "socks5:as.proxys5.net:6200:59739141-zone-custom-sessid-rT8p2Grd-sessTime-15:kNSTvoc4"

# SOCKS4 with custom format
./proxyGolang -l "0.0.0.0:8081" -r "https://example.com" -socks "socks4:proxy.example.com:1080:username:password"
```

## Daily Traffic Statistics

The proxy now tracks **daily traffic statistics** with date separation, including:

- Total number of requests processed per day
- Incoming bytes (from clients) per day
- Outgoing bytes (to clients) per day
- Backend bytes (to/from backend servers) per day
- **Blocked requests count per day**

### Stats Persistence

Statistics are automatically saved to `proxy_stats.json` every minute with daily separation. Stats are organized by date and persist across server restarts.

### Accessing Statistics

```

#### Current Day Stats
Visit `http://your-proxy-host:port/stats` to get **current day only** traffic statistics in JSON format:

```json
{
  "requests": 150,
  "incoming_bytes": 0,
  "outgoing_bytes": 245680,
  "backend_bytes": 123450,
  "blocked_count": 5,
  "total_bytes": 369130
}
```

#### Historical Stats
Visit `http://your-proxy-host:port/stats/history` to get all historical statistics organized by date:

```json
{
  "daily_stats": {
    "2025-09-01": {
      "request_count": 100,
      "incoming_bytes": 0,
      "outgoing_bytes": 150000,
      "backend_bytes": 75000,
      "blocked_count": 3
    },
    "2025-09-02": {
      "request_count": 200,
      "incoming_bytes": 0,
      "outgoing_bytes": 300000,
      "backend_bytes": 150000,
      "blocked_count": 8
    },
    "2025-09-03": {
      "request_count": 150,
      "incoming_bytes": 0,
      "outgoing_bytes": 245680,
      "backend_bytes": 123450,
      "blocked_count": 5
    }
  }
}
```

### Resetting Statistics

Send a POST request to `http://your-proxy-host:port/stats/reset` to reset **current day** counters to zero.

```bash
curl -X POST http://your-proxy-host:port/stats/reset
```

## Daemon Mode

When running as a CLI app, you can now use daemon mode to run the proxy in the background without blocking your terminal:

### Quick Start Scripts

#### Standard Mode:
```bash
./run.sh
```

#### Daemon Mode:
```bash
./run_daemon.sh
```

### Manual Daemon Control:

Start daemon:
```bash
./proxyGolang -d -r "https://example.com" -l "0.0.0.0:8083" -b "exe|zip"
```

Check running processes:
```bash
ps aux | grep proxyGolang
```

Stop daemon:
```bash
kill <PID>
```

Logs are written to `proxy.log` when running in daemon mode.

## Additional Options:

- `-ip`: Specify server IP for DNS resolution
- `-h`: Add custom headers (format: "Header1: Value1; Header2: Value2")
- `-b`: Block specific file extensions (format: "exe|zip|pdf")
- `-d`: Run as daemon in background (doesn't block terminal)

## API Endpoints:

- `GET /stats` - Current day statistics
- `GET /stats/history` - All historical statistics by date
- `POST /stats/reset` - Reset current day statistics

