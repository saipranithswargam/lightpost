package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

const version = "1.0.0"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "run" {
		os.Exit(cliRun(os.Args[2:]))
	}
	browser := flag.Bool("browser", false, "open the UI in the default browser instead of a native window")
	headless := flag.Bool("headless", false, "only start the server (prints the URL)")
	port := flag.Int("port", 0, "port for the local UI server (0 = random)")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()
	if *showVersion {
		fmt.Println("lightpost", version)
		return
	}

	app, err := newApp(dataDir(), true)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lightpost:", err)
		os.Exit(1)
	}
	ln, err := app.listen(*port)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lightpost:", err)
		os.Exit(1)
	}
	srv := app.serve(ln)
	defer app.shutdown(srv)
	url := fmt.Sprintf("http://127.0.0.1:%d/?t=%s", app.port, app.token)

	if *browser || *headless || !hasWindow {
		fmt.Println("Lightpost running at", url)
		if !*headless {
			openBrowser(url)
		}
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		return
	}
	runWindow(url)
}
