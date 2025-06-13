package helper

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bogdanovich/dns_resolver"
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

func (this *Handle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fmt.Println(r.RemoteAddr + " " + r.Method + " " + r.URL.String() + " " + r.Proto + " " + r.UserAgent())

	hasBlockedFile := BlockFileExtension(this.BlockedFiles, r)
	if hasBlockedFile {
		fmt.Println("Blocked file extension for URL:", r.URL.String())
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Parse the remote servers
	multiRemote := strings.Split(this.ReverseProxy, "|")

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
	this.Ip = ""

	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		DualStack: true,
	}

	// Create a custom transport
	customTransport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
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
		},
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	proxy := httputil.NewSingleHostReverseProxy(remote)
	proxy.Transport = customTransport

	// Add custom headers if available
	if this.Headers != "" {
		headers := strings.Split(this.Headers, ";")
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
	proxy.ServeHTTP(w, r)
}
