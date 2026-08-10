package main

import (
	"fmt"
	"os"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

const (
	serveNotImplemented = "panel serve: not implemented in M2. Scheduled: M6 (\"Basic panel CRUD\")."
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "init":
		cmdInit()
	case "serve":
		fmt.Fprintln(os.Stderr, serveNotImplemented)
		os.Exit(1)
	default:
		usage()
	}
}

func cmdInit() {
	path := db.DefaultPath()
	handle, err := db.Open(path)
	if err != nil {
		fatal(err)
	}
	defer handle.Close()
	if err := db.Migrate(handle); err != nil {
		fatal(err)
	}
	if err := awgconf.Generate(handle, "/config/awg0.conf"); err != nil {
		fatal(err)
	}
	fmt.Println("panel init: awg0.conf generated")
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "panel init: %v\n", err)
	os.Exit(1)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: /app/panel [init|serve]")
	os.Exit(2)
}
