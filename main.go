package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
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

	// Handle daemon mode
	if Cmd.Daemon {
		// Create a new process that runs in background
		cmd := exec.Command(os.Args[0],
			"-l", Cmd.Bind,
			"-r", Cmd.Remote,
			"-ip", Cmd.Ip,
			"-h", Cmd.Headers,
			"-b", Cmd.BlockedFiles,
		)

		// Create log file for daemon output
		logFile, err := os.OpenFile("proxy.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			log.Fatalln("Failed to create log file:", err)
		}
		defer logFile.Close()

		cmd.Stdout = logFile
		cmd.Stderr = logFile

		err = cmd.Start()
		if err != nil {
			log.Fatalln("Failed to start daemon:", err)
		}

		fmt.Printf("Daemon started with PID: %d\n", cmd.Process.Pid)
		fmt.Printf("Logs are written to: proxy.log\n")
		fmt.Printf("Stats endpoint: http://%s/stats\n", Cmd.Bind)
		fmt.Printf("History endpoint: http://%s/stats/history\n", Cmd.Bind)
		fmt.Printf("Reset endpoint: http://%s/stats/reset (POST)\n", Cmd.Bind)
		return
	}

	StartServer(Cmd.Bind, Cmd.Remote, Cmd.Ip, Cmd.Headers, Cmd.BlockedFiles)

	// Wait for termination signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	StopServer()
	log.Println("Server gracefully stopped")
}
