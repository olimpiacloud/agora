package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/agusgarcia3007/carbon/internal/api"
	"github.com/agusgarcia3007/carbon/internal/client"
	"github.com/agusgarcia3007/carbon/internal/core"
	"github.com/agusgarcia3007/carbon/internal/mcp"
	"github.com/agusgarcia3007/carbon/internal/stream"
	"github.com/agusgarcia3007/carbon/web"
)

func defaultHome() string {
	if h := os.Getenv("CARBON_HOME"); h != "" {
		return h
	}
	if os.Getuid() == 0 {
		return "/var/lib/carbon"
	}
	return filepath.Join(os.Getenv("HOME"), ".carbon")
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		serve(os.Args[2:])
	case "mcp":
		c := client.FromEnv()
		if err := mcp.New(c).Serve(context.Background(), os.Stdin); err != nil {
			log.Fatal(err)
		}
	case "token":
		b, err := os.ReadFile(filepath.Join(defaultHome(), "admin.token"))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(string(b))
	case "open":
		openURL(os.Args[2:])
	case "api":
		apiCmd(os.Args[2:])
	case "version":
		fmt.Println(core.Version)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `carbon — agent-native desktop runtime

  carbon serve [--listen 127.0.0.1:7788] [--home DIR] [--apps-user carbon] [--rtc-port 0]
  carbon mcp                      MCP stdio server (CARBON_URL, CARBON_TOKEN)
  carbon token                    print the admin token
  carbon open <workspace> [--control] [--base URL]   print a viewer URL with a short-lived token
  carbon api METHOD PATH [JSON]   call the HTTP API`)
}

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:7788", "HTTP listen address")
	home := fs.String("home", defaultHome(), "state directory")
	appsUser := fs.String("apps-user", "carbon", "unprivileged user for apps and browsers (when running as root)")
	chrome := fs.String("chrome", "", "Chrome/Chromium binary")
	rtcPort := fs.Int("rtc-port", 0, "single UDP port for WebRTC (0 = ephemeral)")
	rtcIfaces := fs.String("rtc-interfaces", "", "comma-separated interfaces for WebRTC candidates")
	rtcLoop := fs.Bool("rtc-loopback", true, "offer loopback ICE candidates")
	idle := fs.Duration("idle-after", 5*time.Minute, "mark workspace idle after inactivity")
	suspend := fs.Duration("suspend-after", 0, "suspend idle workspaces after (0 = never by default)")
	fs.Parse(args)
	host, _, _ := net.SplitHostPort(*listen)
	if host != "127.0.0.1" && host != "localhost" && host != "::1" && os.Getenv("CARBON_ALLOW_PUBLIC") != "1" {
		log.Fatalf("refusing to listen on %s: bind to 127.0.0.1 and expose through an authenticated tunnel (set CARBON_ALLOW_PUBLIC=1 to override)", *listen)
	}
	var ifaces []string
	if *rtcIfaces != "" {
		ifaces = strings.Split(*rtcIfaces, ",")
	}
	exe, _ := os.Executable()
	m, err := core.NewManager(core.Config{
		Home: *home, AppsUser: *appsUser, ChromeBinary: *chrome,
		IdleAfter: *idle, SuspendAfter: *suspend,
		RTC:          stream.RTCOptions{UDPPort: *rtcPort, IncludeLoop: *rtcLoop, Interfaces: ifaces},
		CarbonBinary: exe, ListenURL: "http://" + *listen,
	})
	if err != nil {
		log.Fatal(err)
	}
	auth, err := api.NewAuth(*home, m)
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{Addr: *listen, Handler: api.New(m, auth, web.FS()), ReadHeaderTimeout: 10 * time.Second}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("carbon %s listening on http://%s (home %s, cgroups %v)", core.Version, *listen, *home, m.Cgroups())
	go srv.Serve(ln)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Printf("shutting down: checkpointing workspaces")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	m.Shutdown()
}

func openURL(args []string) {
	fs := flag.NewFlagSet("open", flag.ExitOnError)
	control := fs.Bool("control", true, "allow input")
	base := fs.String("base", "", "public base URL (default CARBON_URL)")
	ttl := fs.Int("ttl", 3600, "seconds")
	fs.Parse(args)
	if fs.NArg() < 1 {
		log.Fatal("workspace id required")
	}
	c := client.FromEnv()
	var out struct {
		Token string `json:"token"`
	}
	if err := c.Do(context.Background(), "POST", "/v1/viewer-tokens", map[string]any{"workspace": fs.Arg(0), "control": *control, "ttl_seconds": *ttl}, &out); err != nil {
		log.Fatal(err)
	}
	b := *base
	if b == "" {
		b = c.URL
	}
	fmt.Printf("%s/view/%s#vt=%s\n", strings.TrimRight(b, "/"), fs.Arg(0), out.Token)
}

func apiCmd(args []string) {
	if len(args) < 2 {
		log.Fatal("usage: carbon api METHOD PATH [JSON]")
	}
	c := client.FromEnv()
	var body any
	if len(args) > 2 {
		if err := json.Unmarshal([]byte(args[2]), &body); err != nil {
			log.Fatal(err)
		}
	}
	b, _, err := c.Raw(context.Background(), strings.ToUpper(args[0]), args[1], body)
	if err != nil {
		log.Fatal(err)
	}
	os.Stdout.Write(b)
	if len(b) > 0 && b[len(b)-1] != '\n' {
		fmt.Println()
	}
}
