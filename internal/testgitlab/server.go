// Package testgitlab provides a deterministic TLS GitLab fake for tests. It
// never contacts an external host.
package testgitlab

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
)

type Request struct {
	Method string
	URL    string
	Header http.Header
	Body   []byte
}

type Server struct {
	HTTP *httptest.Server

	mu       sync.Mutex
	requests []Request
	handler  http.Handler
}

func New(handler http.Handler) *Server {
	server := &Server{handler: handler}
	server.HTTP = httptest.NewTLSServer(http.HandlerFunc(server.serveHTTP))
	return server
}

func (s *Server) Close() { s.HTTP.Close() }

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	r.Body = io.NopCloser(bytes.NewReader(body))
	s.mu.Lock()
	s.requests = append(s.requests, Request{Method: r.Method, URL: r.URL.RequestURI(), Header: r.Header.Clone(), Body: body})
	s.mu.Unlock()
	if s.handler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
		return
	}
	s.handler.ServeHTTP(w, r)
}

func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Request, len(s.requests))
	copy(out, s.requests)
	return out
}

func (s *Server) Reset() {
	s.mu.Lock()
	s.requests = nil
	s.mu.Unlock()
}

func (s *Server) CAFile(dir string) (string, error) {
	cert := s.HTTP.Certificate()
	der := cert.Raw
	if parsed, err := x509.ParseCertificate(der); err == nil {
		der = parsed.Raw
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	path := filepath.Join(dir, "fake-gitlab-ca.pem")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}
