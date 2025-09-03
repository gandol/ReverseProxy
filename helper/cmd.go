package helper

import "flag"

type Cmd struct {
	Bind         string
	Remote       string
	Ip           string
	Headers      string
	BlockedFiles string
	Daemon       bool
	SocksProxy   string
}

func ParseCmd() Cmd {
	var cmd Cmd
	flag.StringVar(&cmd.Bind, "l", "0.0.0.0:8888", "listen on ip:port")
	flag.StringVar(&cmd.Remote, "r", "http://idea.lanyus.com:80", "reverse proxy addr")
	flag.StringVar(&cmd.Ip, "ip", "", "reverse proxy addr server ip")
	flag.StringVar(&cmd.Headers, "h", "", "custom headers to add to requests")
	flag.StringVar(&cmd.BlockedFiles, "b", "", "block file extensions (e.g., exe|zip)")
	flag.BoolVar(&cmd.Daemon, "d", false, "run as daemon in background")
	flag.StringVar(&cmd.SocksProxy, "socks", "", "SOCKS proxy address (URL format: socks5://user:pass@host:port or custom format: socks5:host:port:username:password)")
	flag.Parse()
	return cmd
}
