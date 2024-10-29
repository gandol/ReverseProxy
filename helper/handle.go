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
	"time"

	"github.com/bogdanovich/dns_resolver"
)

var RemoteServers = []string{}

type Handle struct {
	ReverseProxy string
	Ip           string
}

func (this *Handle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Println(r.RemoteAddr + " " + r.Method + " " + r.URL.String() + " " + r.Proto + " " + r.UserAgent())
	muliRemote := strings.Split(this.ReverseProxy, "|")
	usedRemote := ""
	if len(muliRemote) == 1 {
		usedRemote = this.ReverseProxy
	} else {
		n := rand.Intn(len(muliRemote))
		if len(RemoteServers) == 0 {
			RemoteServers = muliRemote
			usedRemote = muliRemote[n]
		} else {
			usedRemote = RemoteServers[n]
		}

	}
	fmt.Println(usedRemote)
	remote, err := url.Parse(usedRemote)
	if err != nil {
		log.Fatalln(err)
	}
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		DualStack: true,
	}
	http.DefaultTransport.(*http.Transport).DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		remote := strings.Split(addr, ":")
		if this.Ip == "" {
			resolver := dns_resolver.New([]string{"114.114.114.114", "114.114.115.115", "119.29.29.29", "223.5.5.5", "8.8.8.8", "208.67.222.222", "208.67.220.220"})
			resolver.RetryTimes = 5
			ip, err := resolver.LookupHost(remote[0])
			if err != nil {
				log.Println(err)
			}
			this.Ip = ip[0].String()
		}
		addr = this.Ip + ":" + remote[1]
		return dialer.DialContext(ctx, network, addr)
	}
	proxy := httputil.NewSingleHostReverseProxy(remote)
	proxy.ModifyResponse = func(response *http.Response) error {
		statusCode := response.StatusCode
		if statusCode == 429 {
			if len(RemoteServers) == 1 {
				RemoteServers = muliRemote
				return nil
			}
			for i, v := range RemoteServers {
				if v == remote.Host {
					RemoteServers = append(RemoteServers[:i], RemoteServers[i+1:]...)
				}
			}
		}
		return nil
	}
	r.Host = remote.Host
	proxy.ServeHTTP(w, r)

}
