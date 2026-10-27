// Command mockbmc serves a tiny fake Redfish service for trying the explorer
// without hardware. Credentials: admin / password.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/mattburchett/redfish-explorer/internal/mockbmc"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8443", "listen address")
	noSession := flag.Bool("no-session", false, "disable SessionService to exercise basic-auth fallback")
	flag.Parse()
	log.Printf("mockbmc listening on http://%s (%s / %s)", *addr, mockbmc.User, mockbmc.Pass)
	log.Fatal(http.ListenAndServe(*addr, mockbmc.Handler(*noSession, log.Printf)))
}
