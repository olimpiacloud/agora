package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/olimpiacloud/agora/internal/api"
	"github.com/olimpiacloud/agora/internal/client"
	"github.com/olimpiacloud/agora/internal/core"
	"github.com/olimpiacloud/agora/internal/mcp"
	"github.com/olimpiacloud/agora/internal/sandbox"
	"github.com/olimpiacloud/agora/internal/stream"
	"github.com/olimpiacloud/agora/web"
)

func defaultHome() string {
	if h := os.Getenv("AGORA_HOME"); h != "" {
		return h
	}
	if os.Getuid() == 0 {
		return "/var/lib/agora"
	}
	return filepath.Join(os.Getenv("HOME"), ".agora")
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		serve(os.Args[2:])
	case sandbox.Cmd:
		sandbox.Main(os.Args[2:])
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
	case "version", "--version", "-v":
		fmt.Printf("agora %s (commit %s, built %s)\n", core.Version, core.Commit, core.Date)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `agora — agent-native desktop runtime

  agora serve [--listen 127.0.0.1:7788] [--home DIR] [--apps-user agora] [--rtc-port 0]
  agora mcp                      MCP stdio server (AGORA_URL, AGORA_TOKEN)
  agora token                    print the admin token
  agora open <workspace> [--control] [--base URL]   print a viewer URL with a short-lived token
  agora api METHOD PATH [JSON]   call the HTTP API
  agora version                  print version, commit and build date`)
}

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:7788", "HTTP listen address")
	home := fs.String("home", defaultHome(), "state directory")
	appsUser := fs.String("apps-user", "agora", "unprivileged user for apps and browsers (when running as root)")
	chrome := fs.String("chrome", "", "Chrome/Chromium binary")
	rtcPort := fs.Int("rtc-port", 0, "single UDP port for WebRTC (0 = ephemeral)")
	rtcIfaces := fs.String("rtc-interfaces", "", "comma-separated interfaces for WebRTC candidates")
	rtcLoop := fs.Bool("rtc-loopback", true, "offer loopback ICE candidates")
	idle := fs.Duration("idle-after", 5*time.Minute, "mark workspace idle after inactivity")
	suspend := fs.Duration("suspend-after", 30*time.Minute, "suspend workspaces after this long without activity or viewers (0 = never)")
	tabIdle := fs.Duration("tab-idle", 15*time.Minute, "close browser tabs and stop launched apps of ended cursors after this long unused (0 = never)")
	cursorRetention := fs.Duration("cursor-retention", core.DefaultCursorRetention, "delete finished cursors after this long (0 = keep forever)")
	fs.Parse(args)
	host, _, _ := net.SplitHostPort(*listen)
	if host != "127.0.0.1" && host != "localhost" && host != "::1" && os.Getenv("AGORA_ALLOW_PUBLIC") != "1" {
		log.Fatalf("refusing to listen on %s: bind to 127.0.0.1 and expose through an authenticated tunnel (set AGORA_ALLOW_PUBLIC=1 to override)", *listen)
	}
	var ifaces []string
	if *rtcIfaces != "" {
		ifaces = strings.Split(*rtcIfaces, ",")
	}
	exe, _ := os.Executable()
	m, err := core.NewManager(core.Config{
		Home: *home, AppsUser: *appsUser, ChromeBinary: *chrome,
		IdleAfter: *idle, SuspendAfter: *suspend, TabIdle: *tabIdle, CursorRetention: *cursorRetention,
		RTC:         stream.RTCOptions{UDPPort: *rtcPort, IncludeLoop: *rtcLoop, Interfaces: ifaces},
		AgoraBinary: exe, ListenURL: "http://" + *listen,
	})
	if err != nil {
		log.Fatal(err)
	}
	auth, err := api.NewAuth(*home, m)
	if err != nil {
		log.Fatal(err)
	}
	base, stop := context.WithCancel(context.Background())
	srv := &http.Server{Addr: *listen, Handler: api.New(m, auth, web.FS()), ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return base }}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("agora %s listening on http://%s (home %s, cgroups %v)", core.Version, *listen, *home, m.Cgroups())
	go srv.Serve(ln)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Printf("shutting down: checkpointing workspaces")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stop()
	srv.Shutdown(ctx)
	m.Shutdown()
}

type openOpts struct {
	ws      string
	control bool
	base    string
	ttl     int
}

func parseOpen(args []string) (openOpts, error) {
	o := openOpts{}
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&o.control, "control", true, "allow input")
	fs.StringVar(&o.base, "base", "", "public base URL (default AGORA_URL)")
	fs.IntVar(&o.ttl, "ttl", 3600, "seconds")
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return o, err
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(pos) != 1 {
		return o, fmt.Errorf("expected one workspace id, got %d arguments", len(pos))
	}
	o.ws = pos[0]
	return o, nil
}

func openURL(args []string) {
	o, err := parseOpen(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agora open: %v\nusage: agora open <workspace> [--control=false] [--base URL] [--ttl 3600]\n", err)
		os.Exit(2)
	}
	c := client.FromEnv()
	var out struct {
		Token string `json:"token"`
	}
	if err := c.Do(context.Background(), "POST", "/v1/viewer-tokens", map[string]any{"workspace": o.ws, "control": o.control, "ttl_seconds": o.ttl}, &out); err != nil {
		log.Fatal(err)
	}
	b := o.base
	if b == "" {
		b = c.URL
	}
	fmt.Printf("%s/view/%s#vt=%s\n", strings.TrimRight(b, "/"), o.ws, out.Token)
}

func apiCmd(args []string) {
	if len(args) < 2 {
		log.Fatal("usage: agora api METHOD PATH [JSON]")
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
