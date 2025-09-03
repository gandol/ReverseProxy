# ReverseProxy
ReverseProxy in golang with traffic calculation

## Features:
- Reverse proxy functionality
- Traffic statistics tracking
- Server failover and load balancing
- Custom headers support
- File extension blocking

## Use:

	./ReverseProxy_[OS]_[ARCH] -h
	
	Usage of ReverseProxy_[OS]_[ARCH]:
	  -l string
	        listen on ip:port (default "0.0.0.0:8888")
	  -r string
	        reverse proxy addr (default "http://idea.lanyus.com:80")
	  -h string
	        custom headers to add to requests
	  -b string
	        block file extensions (e.g., exe|zip)

	./ReverseProxy_windows_amd64.exe -l "0.0.0.0:8081" -r "https://www.baidu.com"

	Listening on 0.0.0.0:8081, forwarding to https://www.baidu.com

## Traffic Statistics

The proxy now tracks traffic statistics including:

- Total number of requests processed
- Incoming bytes (from clients)
- Outgoing bytes (to clients) 
- Backend bytes (to/from backend servers)

### Stats Persistence

Statistics are automatically saved to `proxy_stats.json` every minute and loaded when the server starts. This ensures that traffic statistics persist across server restarts.

### Accessing Statistics

Visit `http://your-proxy-host:port/stats` to get current traffic statistics in JSON format:

```json
{
  "requests": 150,
  "incoming_bytes": 0,
  "outgoing_bytes": 245680,
  "backend_bytes": 123450,
  "total_bytes": 369130
}
```

### Resetting Statistics

Send a POST request to `http://your-proxy-host:port/stats/reset` to reset all counters to zero.

The stats file format:
```json
{
  "request_count": 150,
  "incoming_bytes": 0,
  "outgoing_bytes": 245680,
  "backend_bytes": 123450,
  "timestamp": 1693740000
}
```

## Additional Options:

- `-ip`: Specify server IP for DNS resolution
- `-h`: Add custom headers (format: "Header1: Value1; Header2: Value2")
- `-b`: Block specific file extensions (format: "exe|zip|pdf")

