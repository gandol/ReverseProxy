# ReverseProxy
ReverseProxy in golang with traffic calculation and daily statistics

## Features:
- Reverse proxy functionality
- **Daily traffic statistics tracking with date separation**
- **Blocked requests counting**
- **Daemon mode for background operation**
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

### Standard Mode (blocks terminal):
	./proxyGolang -l "0.0.0.0:8081" -r "https://www.baidu.com"

### Daemon Mode (runs in background):
	./proxyGolang -d -l "0.0.0.0:8081" -r "https://www.baidu.com"

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

