package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	version := flag.String("version", "v1", "page version: v1 or v2")
	addr := flag.String("addr", "127.0.0.1:8000", "listen address")
	flag.Parse()
	if *version != "v1" && *version != "v2" {
		log.Fatal("version must be v1 or v2")
	}
	body, err := os.ReadFile("examples/demo-site/" + *version + ".html")
	if err != nil {
		log.Fatal(err)
	}
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(body)
	})
	fmt.Printf("Demo %s at http://%s\n", *version, *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
