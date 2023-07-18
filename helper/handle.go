package helper

import (
	"context"
	"github.com/bogdanovich/dns_resolver"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

type Handle struct {
	ReverseProxy string
	Ip           string
}

func (this *Handle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Println(r.RemoteAddr + " " + r.Method + " " + r.URL.String() + " " + r.Proto + " " + r.UserAgent())
	remote, err := url.Parse(this.ReverseProxy)
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
	r.Host = remote.Host
	proxy.ServeHTTP(w, r)
}
