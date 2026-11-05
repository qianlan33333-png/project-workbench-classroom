package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"projectworkbench/internal/toolplugin"
	"syscall"
	"time"
)

func main() {
	path := flag.String("config", "", "private plugin config")
	flag.Parse()
	raw, e := os.ReadFile(*path)
	if e != nil {
		log.Fatal("plugin config unavailable")
	}
	var c toolplugin.Config
	if json.Unmarshal(raw, &c) != nil {
		log.Fatal("invalid plugin config")
	}
	s, e := toolplugin.Open(c)
	if e != nil {
		log.Fatal("plugin initialization failed: ", e)
	}
	defer s.DB.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go s.Run(ctx)
	server := &http.Server{Addr: s.Config.Listen, Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 120 * time.Second}
	go func() {
		<-ctx.Done()
		stop, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		server.Shutdown(stop)
	}()
	if e = server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Fatal(e)
	}
}
