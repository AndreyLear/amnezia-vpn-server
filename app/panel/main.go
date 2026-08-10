package main

import (
	"os"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
