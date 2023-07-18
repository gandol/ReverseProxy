package helper

import "flag"

type Cmd struct {
	Bind   string
	Remote string
	Ip     string
}

func ParseCmd() Cmd {
	var cmd Cmd
	flag.StringVar(&cmd.Bind, "l", "0.0.0.0:8888", "listen on ip:port")
	flag.StringVar(&cmd.Remote, "r", "http://idea.lanyus.com:80", "reverse proxy addr")
	flag.StringVar(&cmd.Ip, "ip", "", "reverse proxy addr server ip")
	flag.Parse()
	return cmd
}
