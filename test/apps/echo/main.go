// Command echo is the end-to-end test server. It records every request it receives, so tests can
// read them per pod on the stats port, and answers with the status code from STATUS.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"flag"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type record struct {
	Method  string      `json:"method"`
	Path    string      `json:"path"`
	Host    string      `json:"host"`
	Headers http.Header `json:"headers"`
	Body    string      `json:"body"`
}

type server struct {
	status  int
	ready   bool
	mu      sync.Mutex
	records []record
}

func main() {
	listen := flag.String("listen", ":8080", "comma-separated HTTP listen addresses")
	tlsListen := flag.String("tls-listen", "", "comma-separated HTTPS listen addresses")
	stats := flag.String("stats", ":9090", "listen address for /requests and /ready")
	flag.Parse()

	s := &server{status: http.StatusOK, ready: os.Getenv("READY") != "false"}
	if v := os.Getenv("STATUS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			log.Fatalf("STATUS: %v", err)
		}
		s.status = n
	}

	statsMux := http.NewServeMux()
	statsMux.HandleFunc("/requests", s.handleRequests)
	statsMux.HandleFunc("/ready", s.handleReady)
	go serve(*stats, statsMux, nil)

	for _, addr := range split(*listen) {
		go serve(addr, http.HandlerFunc(s.handleEcho), nil)
	}
	if addrs := split(*tlsListen); len(addrs) > 0 {
		cfg := &tls.Config{Certificates: []tls.Certificate{selfSigned()}}
		for _, addr := range addrs {
			go serve(addr, http.HandlerFunc(s.handleEcho), cfg)
		}
	}
	select {}
}

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func serve(addr string, h http.Handler, cfg *tls.Config) {
	srv := &http.Server{Addr: addr, Handler: h, TLSConfig: cfg, ReadHeaderTimeout: 10 * time.Second}
	var err error
	if cfg != nil {
		err = srv.ListenAndServeTLS("", "")
	} else {
		err = srv.ListenAndServe()
	}
	log.Fatal(err)
}

func (s *server) handleEcho(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/__big" {
		n, _ := strconv.Atoi(r.URL.Query().Get("bytes"))
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte(strings.Repeat("x", n)))
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	s.mu.Lock()
	s.records = append(s.records, record{Method: r.Method, Path: r.URL.RequestURI(), Host: r.Host, Headers: r.Header, Body: string(body)})
	s.mu.Unlock()
	w.WriteHeader(s.status)
}

func (s *server) handleRequests(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(append([]record{}, s.records...))
}

func (s *server) handleReady(w http.ResponseWriter, _ *http.Request) {
	if !s.ready {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func selfSigned() tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "echo"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		log.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
