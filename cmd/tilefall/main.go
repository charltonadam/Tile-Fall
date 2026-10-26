// Command tilefall runs the Tile-Fall tabletop server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/charltonadam/tilefall/assets"
	"github.com/charltonadam/tilefall/internal/game"
	"github.com/charltonadam/tilefall/internal/server"
	"github.com/charltonadam/tilefall/internal/store"
)

func main() {
	port := flag.Int("port", 8080, "HTTP port")
	dataDir := flag.String("data", "data", "directory for saved scenes and uploads")
	mcpLAN := flag.Bool("mcp-lan", false, "allow MCP clients from other machines (default: host only)")
	flag.Parse()

	cat, err := assets.Catalog()
	if err != nil {
		log.Fatalf("catalog: %v", err)
	}
	st, err := store.Open(*dataDir)
	if err != nil {
		log.Fatalf("data dir: %v", err)
	}
	g, err := game.Open(cat, st)
	if err != nil {
		log.Fatalf("open game: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	saved := make(chan struct{})
	go func() {
		st.Autosave(ctx, g.Table)
		close(saved)
	}()

	srv := &http.Server{Addr: ":" + strconv.Itoa(*port), Handler: server.New(g, server.Options{MCPFromLAN: *mcpLAN})}
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		log.Fatal(err)
	}
	printURLs(*port)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	<-saved
	log.Println("saved; bye")
}

func printURLs(port int) {
	fmt.Printf("Tile-Fall is running.\n")
	fmt.Printf("  DM (this computer):  http://localhost:%d/?role=dm\n", port)
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() && ipn.IP.IsPrivate() {
			fmt.Printf("  Players:             http://%s:%d/\n", ipn.IP, port)
		}
	}
	fmt.Printf("  MCP (Claude Code):   claude mcp add --transport http tilefall http://localhost:%d/mcp\n", port)
}
