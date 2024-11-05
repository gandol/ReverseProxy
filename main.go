package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"proxyGolang/helper"
	"syscall"
)

var Cmd helper.Cmd
var srv http.Server

func StartServer(bind string, remote string, ip string) {
	log.Printf("Listening on %s, forwarding to %s", bind, remote)
	h := &helper.Handle{ReverseProxy: remote}
	srv.Addr = bind
	srv.Handler = h
	go func() {
		if err := srv.ListenAndServe(); err != nil {
			log.Fatalln("ListenAndServe: ", err)
		}
	}()
}

func StopServer() {
	if err := srv.Shutdown(nil); err != nil {
		log.Println(err)
	}
}

func main() {
	Cmd = helper.ParseCmd()
	StartServer(Cmd.Bind, Cmd.Remote, Cmd.Ip)

	// Wait for termination signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	StopServer()
	log.Println("Server gracefully stopped")
}
