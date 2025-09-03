package helper

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bogdanovich/dns_resolver"
	"golang.org/x/net/proxy"
)

// ServerManager handles server selection, rotation, and failover
type ServerManager struct {
	servers         []string
	deletedServers  []DeletedRemoteServer
	requestChan     chan serverRequest
	originalServers []string
	mutex           sync.RWMutex
}

type serverRequest struct {
	responseChannel  chan string
	markFailedServer string
	operation        string // "get" or "markFailed"
}

type DeletedRemoteServer struct {
	RemoteServer string
	DeletedAt    time.Time
}

// Global ServerManager
var globalServerManager *ServerManager

// Initialize the global server manager
func init() {
	globalServerManager = NewServerManager([]string{})
	go globalServerManager.Run()
}

// NewServerManager creates a new server manager
func NewServerManager(initialServers []string) *ServerManager {
	return &ServerManager{
		servers:         initialServers,
		deletedServers:  []DeletedRemoteServer{},
		requestChan:     make(chan serverRequest, 100),
		originalServers: initialServers,
		mutex:           sync.RWMutex{},
	}
}

// Run starts the server manager's main loop
func (sm *ServerManager) Run() {
	for req := range sm.requestChan {
		switch req.operation {
		case "get":
			sm.mutex.RLock()
			if len(sm.servers) == 0 {
				sm.mutex.RUnlock()
				sm.mutex.Lock()
				// Reset to original servers if empty
				sm.servers = make([]string, len(sm.originalServers))
				copy(sm.servers, sm.originalServers)
				sm.mutex.Unlock()

				sm.mutex.RLock()
			}

			var server string
			if len(sm.servers) > 0 {
				n := rand.Intn(len(sm.servers))
				server = sm.servers[n]
			} else {
				// Fallback to first original server if somehow we still have no servers
				server = sm.originalServers[0]
			}
			sm.mutex.RUnlock()

			req.responseChannel <- server

		case "markFailed":
			serverToMark := req.markFailedServer
			if serverToMark != "" {
				sm.mutex.Lock()

				// Find and remove the failed server
				for i, server := range sm.servers {
					formattedServer := CleanRemotWithoutPath(server)
					formattedFailedServer := CleanRemotWithoutPath(serverToMark)

					if formattedServer.Remote == formattedFailedServer.Remote {
						// Remove this server
						sm.deletedServers = append(sm.deletedServers, DeletedRemoteServer{
							RemoteServer: server,
							DeletedAt:    time.Now(),
						})

						// Remove from active servers
						sm.servers = append(sm.servers[:i], sm.servers[i+1:]...)
						fmt.Println("Removed failed server:", server, "- Remaining servers:", len(sm.servers))
						break
					}
				}

				// If no servers left, reset to all original servers
				if len(sm.servers) == 0 {
					sm.servers = make([]string, len(sm.originalServers))
					copy(sm.servers, sm.originalServers)
					fmt.Println("Reset to full server list with", len(sm.servers), "servers")
				}

				sm.mutex.Unlock()
			}

			// Always send response to prevent deadlock
			req.responseChannel <- "ok"
		}
	}
}

// GetServer safely gets a server from the pool
func (sm *ServerManager) GetServer() string {
	respChan := make(chan string)
	sm.requestChan <- serverRequest{
		responseChannel: respChan,
		operation:       "get",
	}
	return <-respChan
}

// MarkServerAsFailed safely marks a server as failed
func (sm *ServerManager) MarkServerAsFailed(server string) {
	respChan := make(chan string)
	sm.requestChan <- serverRequest{
		responseChannel:  respChan,
		markFailedServer: server,
		operation:        "markFailed",
	}
	<-respChan // Wait for confirmation
}

// SetOriginalServers sets the original server list
func (sm *ServerManager) SetOriginalServers(servers []string) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	sm.originalServers = make([]string, len(servers))
	copy(sm.originalServers, servers)

	// If no active servers, initialize with the original servers
	if len(sm.servers) == 0 {
		sm.servers = make([]string, len(servers))
		copy(sm.servers, servers)
	}
}

var serverIPCache = make(map[string]string)
var ipCacheMutex sync.RWMutex

type Handle struct {
	ReverseProxy string
	Ip           string
	Headers      string
	BlockedFiles string
	SocksProxy   string
	// Traffic counters
	RequestCount  int64
	IncomingBytes int64
	OutgoingBytes int64
	BackendBytes  int64
	BlockedCount  int64
	mutex         sync.RWMutex
	// Stats persistence
	statsFile string
	stopChan  chan struct{}
}

// responseWriterWrapper wraps http.ResponseWriter to count bytes written
type responseWriterWrapper struct {
	http.ResponseWriter
	handle *Handle
}

func (rw *responseWriterWrapper) Write(data []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(data)
	if err == nil {
		rw.handle.mutex.Lock()
		rw.handle.OutgoingBytes += int64(n)
		rw.handle.mutex.Unlock()
	}
	return n, err
}

// countingTransport wraps http.RoundTripper to count backend traffic
type countingTransport struct {
	transport http.RoundTripper
	handle    *Handle
}

func (ct *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Count request body size if present
	if req.Body != nil {
		// For simplicity, we'll count the Content-Length header if available
		if req.ContentLength > 0 {
			ct.handle.mutex.Lock()
			ct.handle.BackendBytes += req.ContentLength
			ct.handle.mutex.Unlock()
		}
	}

	resp, err := ct.transport.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	// Count response body size if Content-Length is available
	if resp.ContentLength > 0 {
		ct.handle.mutex.Lock()
		ct.handle.BackendBytes += resp.ContentLength
		ct.handle.mutex.Unlock()
	}

	return resp, nil
}

type CleanRemotWithoutPathType struct {
	Remote     string
	Additional string
}

func CleanRemotWithoutPath(remote string) CleanRemotWithoutPathType {
	tmpUrl, err := url.Parse(remote)
	if err != nil {
		log.Fatalln("Error parsing URL:", err)
	}
	if tmpUrl.Scheme == "" || tmpUrl.Host == "" {
		log.Fatalln("Invalid URL format, must be like http://example.com/path/to/resource")
	}
	return CleanRemotWithoutPathType{
		Remote:     fmt.Sprintf("%s://%s", tmpUrl.Scheme, tmpUrl.Host),
		Additional: tmpUrl.Path,
	}
}

func BlockFileExtension(exts string, request *http.Request) bool {
	if exts == "" {
		return false
	}
	extensions := strings.Split(exts, "|")
	fileExtentionFromUrl := strings.ToLower(request.URL.Path[strings.LastIndex(request.URL.Path, ".")+1:])
	if len(fileExtentionFromUrl) == 0 {
		return false
	}
	for _, ext := range extensions {
		if strings.EqualFold(fileExtentionFromUrl, ext) {
			return true
		}
	}

	return false
}

func (h *Handle) GetStats() (int64, int64, int64, int64, int64) {
	h.mutex.RLock()
	defer h.mutex.RUnlock()
	return h.RequestCount, h.IncomingBytes, h.OutgoingBytes, h.BackendBytes, h.BlockedCount
}

func (h *Handle) serveStats(w http.ResponseWriter, r *http.Request) {
	reqCount, inBytes, outBytes, backendBytes, blockedCount := h.GetStats()

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{
		"requests": %d,
		"incoming_bytes": %d,
		"outgoing_bytes": %d,
		"backend_bytes": %d,
		"blocked_count": %d,
		"total_bytes": %d
	}`, reqCount, inBytes, outBytes, backendBytes, blockedCount, inBytes+outBytes+backendBytes)
}

func (h *Handle) serveHistoryStats(w http.ResponseWriter, r *http.Request) {
	file, err := os.Open(h.statsFile)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"error": "Failed to open stats file"}`)
		return
	}
	defer file.Close()

	var allStats AllStatsData
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&allStats); err != nil {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"error": "Failed to decode stats"}`)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.Encode(allStats)
}

func (h *Handle) resetStats(w http.ResponseWriter, r *http.Request) {
	h.mutex.Lock()
	h.RequestCount = 0
	h.IncomingBytes = 0
	h.OutgoingBytes = 0
	h.BackendBytes = 0
	h.BlockedCount = 0
	h.mutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status": "stats reset"}`)
}

// StatsData represents the structure for saving/loading stats
type DailyStatsData struct {
	RequestCount  int64 `json:"request_count"`
	IncomingBytes int64 `json:"incoming_bytes"`
	OutgoingBytes int64 `json:"outgoing_bytes"`
	BackendBytes  int64 `json:"backend_bytes"`
	BlockedCount  int64 `json:"blocked_count"`
}

type AllStatsData struct {
	DailyStats map[string]*DailyStatsData `json:"daily_stats"`
}

func (h *Handle) SaveStats() error {
	h.mutex.RLock()
	defer h.mutex.RUnlock()

	today := time.Now().Format("2006-01-02")

	// Read existing data first
	var allStats AllStatsData
	if file, err := os.Open(h.statsFile); err == nil {
		decoder := json.NewDecoder(file)
		decoder.Decode(&allStats)
		file.Close()
	}

	// Initialize if nil
	if allStats.DailyStats == nil {
		allStats.DailyStats = make(map[string]*DailyStatsData)
	}

	// Update today's stats
	allStats.DailyStats[today] = &DailyStatsData{
		RequestCount:  h.RequestCount,
		IncomingBytes: h.IncomingBytes,
		OutgoingBytes: h.OutgoingBytes,
		BackendBytes:  h.BackendBytes,
		BlockedCount:  h.BlockedCount,
	}

	// Ensure the directory exists
	dir := filepath.Dir(h.statsFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %v", dir, err)
	}

	file, err := os.Create(h.statsFile)
	if err != nil {
		return fmt.Errorf("failed to create stats file %s: %v", h.statsFile, err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	if err := encoder.Encode(allStats); err != nil {
		return fmt.Errorf("failed to encode stats: %v", err)
	}

	return nil
}

func (h *Handle) loadStats() error {
	file, err := os.Open(h.statsFile)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist, start with zero stats
			return nil
		}
		return fmt.Errorf("failed to open stats file: %v", err)
	}
	defer file.Close()

	var allStats AllStatsData
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&allStats); err != nil {
		return fmt.Errorf("failed to decode stats: %v", err)
	}

	// Load today's stats if available
	today := time.Now().Format("2006-01-02")
	if allStats.DailyStats != nil && allStats.DailyStats[today] != nil {
		data := allStats.DailyStats[today]
		h.mutex.Lock()
		h.RequestCount = data.RequestCount
		h.IncomingBytes = data.IncomingBytes
		h.OutgoingBytes = data.OutgoingBytes
		h.BackendBytes = data.BackendBytes
		h.BlockedCount = data.BlockedCount
		h.mutex.Unlock()

		fmt.Printf("Loaded today's stats from file: %d requests, %d blocked, %d total bytes\n",
			data.RequestCount, data.BlockedCount, data.IncomingBytes+data.OutgoingBytes+data.BackendBytes)
	} else {
		fmt.Println("No stats found for today, starting fresh")
	}

	return nil
}

func (h *Handle) startPeriodicSave() {
	h.stopChan = make(chan struct{})
	go func() {
		ticker := time.NewTicker(60 * time.Second) // Save every minute
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := h.SaveStats(); err != nil {
					log.Printf("Failed to save stats: %v", err)
				}
			case <-h.stopChan:
				// Save one final time before stopping
				if err := h.SaveStats(); err != nil {
					log.Printf("Failed to save final stats: %v", err)
				}
				return
			}
		}
	}()
}

func (h *Handle) StopPeriodicSave() {
	if h.stopChan != nil {
		close(h.stopChan)
	}
}

// NewHandle creates a new Handle with stats persistence
func NewHandle(reverseProxy, headers, blockedFiles, statsFile, socksProxy string) *Handle {
	h := &Handle{
		ReverseProxy: reverseProxy,
		Headers:      headers,
		BlockedFiles: blockedFiles,
		SocksProxy:   socksProxy,
		statsFile:    statsFile,
	}

	// Load existing stats
	if err := h.loadStats(); err != nil {
		log.Printf("Warning: Failed to load stats: %v", err)
	}

	// Start periodic stats saving
	h.startPeriodicSave()

	return h
}

// dialWithDNSResolution handles DNS resolution and IP caching for direct connections
func (h *Handle) dialWithDNSResolution(ctx context.Context, network, addr string, dialer *net.Dialer) (net.Conn, error) {
	remoteAddr := strings.Split(addr, ":")
	hostName := remoteAddr[0]

	// Check if we already have this IP cached
	ipCacheMutex.RLock()
	cachedIP, exists := serverIPCache[hostName]
	ipCacheMutex.RUnlock()

	// If not cached or IP is empty, resolve it
	if !exists || cachedIP == "" {
		resolver := dns_resolver.New([]string{"114.114.114.114", "114.114.115.115", "119.29.29.29", "223.5.5.5", "8.8.8.8", "208.67.222.222", "208.67.220.220"})
		resolver.RetryTimes = 5

		ips, err := resolver.LookupHost(hostName)
		if err != nil {
			log.Println("DNS resolution error:", err)
			return nil, err
		}

		if len(ips) == 0 {
			return nil, fmt.Errorf("no IP addresses found for host: %s", hostName)
		}

		// Cache the resolved IP
		ipCacheMutex.Lock()
		serverIPCache[hostName] = ips[0].String()
		cachedIP = serverIPCache[hostName]
		ipCacheMutex.Unlock()
	}

	// Use the resolved/cached IP
	addr = cachedIP + ":" + remoteAddr[1]
	return dialer.DialContext(ctx, network, addr)
}

// parseCustomSOCKSFormat parses custom SOCKS format: type:host:port:username:password
func (h *Handle) parseCustomSOCKSFormat(socksConfig string, baseDialer *net.Dialer) (proxy.Dialer, error) {
	parts := strings.Split(socksConfig, ":")
	if len(parts) != 5 {
		return nil, fmt.Errorf("invalid SOCKS format, expected type:host:port:username:password, got: %s", socksConfig)
	}

	socksType := parts[0]
	host := parts[1]
	port := parts[2]
	username := parts[3]
	password := parts[4]

	log.Printf("Parsing SOCKS proxy - Type: %s, Host: %s, Port: %s",
		socksType, host, port)

	// Validate SOCKS type
	if socksType != "socks5" && socksType != "socks4" && socksType != "socks4a" {
		return nil, fmt.Errorf("unsupported SOCKS type: %s", socksType)
	}

	// For SOCKS5, try to create a direct SOCKS5 dialer with explicit auth
	if socksType == "socks5" && username != "" && password != "" {
		// Try creating a SOCKS5 dialer with Auth
		auth := &proxy.Auth{
			User:     username,
			Password: password,
		}

		// Create the proxy address
		proxyAddr := net.JoinHostPort(host, port)

		dialer, err := proxy.SOCKS5("tcp", proxyAddr, auth, baseDialer)
		if err != nil {
			log.Printf("Failed to create SOCKS5 dialer with explicit auth: %v", err)

			// Fallback to URL-based approach
			proxyURL := &url.URL{
				Scheme: socksType,
				User:   url.UserPassword(username, password),
				Host:   proxyAddr,
			}
			log.Printf("Trying fallback URL-based SOCKS5 approach")
			return proxy.FromURL(proxyURL, baseDialer)
		}

		log.Printf("Created SOCKS5 dialer with explicit auth for %s:%s", host, port)
		return dialer, nil
	}

	// Fallback to URL-based approach for other cases
	var proxyURL *url.URL
	if username != "" && password != "" {
		proxyURL = &url.URL{
			Scheme: socksType,
			User:   url.UserPassword(username, password),
			Host:   net.JoinHostPort(host, port),
		}
		log.Printf("Created SOCKS URL with auth: %s://[username]@%s", socksType, net.JoinHostPort(host, port))
	} else {
		proxyURL = &url.URL{
			Scheme: socksType,
			Host:   net.JoinHostPort(host, port),
		}
		log.Printf("Created SOCKS URL without auth: %s://%s", socksType, net.JoinHostPort(host, port))
	}

	// Create SOCKS dialer
	return proxy.FromURL(proxyURL, baseDialer)
}

func (h *Handle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Handle stats endpoint
	if r.URL.Path == "/stats" {
		h.serveStats(w, r)
		return
	}
	if r.URL.Path == "/stats/history" {
		h.serveHistoryStats(w, r)
		return
	}

	// Count the request
	h.mutex.Lock()
	h.RequestCount++
	h.mutex.Unlock()

	fmt.Println(r.RemoteAddr + " " + r.Method + " " + r.URL.String() + " " + r.Proto + " " + r.UserAgent())

	hasBlockedFile := BlockFileExtension(h.BlockedFiles, r)
	if hasBlockedFile {
		fmt.Println("Blocked file extension for URL:", r.URL.String())

		// Increment blocked count
		h.mutex.Lock()
		h.BlockedCount++
		h.mutex.Unlock()

		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Parse the remote servers
	multiRemote := strings.Split(h.ReverseProxy, "|")

	// Make sure the global server manager has the current server list
	globalServerManager.SetOriginalServers(multiRemote)

	// Safely get a server to use
	usedRemote := globalServerManager.GetServer()

	// Process URL path if needed
	formatedRemote := CleanRemotWithoutPath(usedRemote)
	if formatedRemote.Additional != "" {
		usedRemote = formatedRemote.Remote
		r.URL.Path = formatedRemote.Additional + r.URL.Path
	}

	fmt.Println("Using remote server:", usedRemote)
	remote, err := url.Parse(usedRemote)
	if err != nil {
		log.Fatalln(err)
	}

	// Reset IP for this request
	h.Ip = ""

	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		DualStack: true,
	}

	// Create a custom transport
	var customTransport *http.Transport

	// Check if SOCKS proxy is configured
	if h.SocksProxy != "" {
		// Parse SOCKS proxy - support both URL format and custom format
		var socksDialer proxy.Dialer
		var err error

		// Check if it's in custom format: type:host:port:username:password
		// Custom format has exactly 5 parts when split by ":"
		parts := strings.Split(h.SocksProxy, ":")
		if len(parts) == 5 && (parts[0] == "socks5" || parts[0] == "socks4" || parts[0] == "socks4a") {
			// Parse custom format
			socksDialer, err = h.parseCustomSOCKSFormat(h.SocksProxy, dialer)
		} else {
			// Try URL format
			proxyURL, urlErr := url.Parse(h.SocksProxy)
			if urlErr != nil {
				log.Printf("Invalid SOCKS proxy format: %v", urlErr)
				err = urlErr
			} else {
				// Create SOCKS dialer from URL
				socksDialer, err = proxy.FromURL(proxyURL, dialer)
			}
		}

		if err != nil {
			log.Printf("Failed to create SOCKS dialer: %v", err)
			// Fall back to direct connection
			customTransport = &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return h.dialWithDNSResolution(ctx, network, addr, dialer)
				},
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			}
		} else {
			// Use SOCKS proxy
			customTransport = &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					// For SOCKS proxy, we can directly use the hostname without DNS resolution
					return socksDialer.Dial(network, addr)
				},
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			}
		}
	} else {
		// Direct connection without SOCKS proxy
		customTransport = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return h.dialWithDNSResolution(ctx, network, addr, dialer)
			},
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
	}

	proxy := httputil.NewSingleHostReverseProxy(remote)
	proxy.Transport = &countingTransport{
		transport: customTransport,
		handle:    h,
	}

	// Add custom headers if available
	if h.Headers != "" {
		headers := strings.Split(h.Headers, ";")
		for _, header := range headers {
			if strings.Contains(header, ":") {
				parts := strings.SplitN(header, ":", 2)
				if len(parts) == 2 {
					key := strings.TrimSpace(parts[0])
					value := strings.TrimSpace(parts[1])
					if key != "" && value != "" {
						r.Header.Set(key, value)
					}
				}
			}
		}
	}

	proxy.ModifyResponse = func(response *http.Response) error {
		statusCode := response.StatusCode
		if statusCode == 429 {
			fmt.Println("Server returned 429 - Too Many Requests")

			// Mark this server as failed
			globalServerManager.MarkServerAsFailed(usedRemote)
		} else if statusCode == 403 || statusCode == 404 {
			requestUrl := usedRemote + r.URL.Path
			fmt.Println("Remote server returned status code", statusCode, "for URL:", requestUrl)
		}
		return nil
	}

	r.Host = remote.Host

	// Wrap the response writer to count outgoing bytes
	wrappedWriter := &responseWriterWrapper{
		ResponseWriter: w,
		handle:         h,
	}

	proxy.ServeHTTP(wrappedWriter, r)
}
