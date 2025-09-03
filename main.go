package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"proxyGolang/helper"
	"syscall"
	"time"
)

var Cmd helper.Cmd
var srv http.Server

func StartServer(bind string, remote string, ip string, headers string, blocked string) {
	log.Printf("Listening on %s, forwarding to %s", bind, remote)
	h := helper.NewHandle(remote, headers, blocked, "proxy_stats.json")
	srv.Addr = bind
	srv.Handler = h
	go func() {
		if err := srv.ListenAndServe(); err != nil {
			log.Fatalln("ListenAndServe: ", err)
		}
	}()
}

func StopServer() {
	// Stop periodic stats saving (this will trigger a final save in the goroutine)
	if h, ok := srv.Handler.(*helper.Handle); ok {
		h.StopPeriodicSave()

		// Wait a bit for the periodic goroutine to finish its final save
		time.Sleep(200 * time.Millisecond)

		// Also save from main thread to ensure it's saved
		if err := h.SaveStats(); err != nil {
			log.Printf("Failed to save final stats: %v", err)
		}
	}

	if err := srv.Shutdown(context.Background()); err != nil {
		log.Println(err)
	}
}

func main() {
	Cmd = helper.ParseCmd()
	StartServer(Cmd.Bind, Cmd.Remote, Cmd.Ip, Cmd.Headers, Cmd.BlockedFiles)

	// Wait for termination signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	StopServer()
	log.Println("Server gracefully stopped")
}
