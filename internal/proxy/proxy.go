// Package proxy implements an embedded, lightweight reverse proxy for DRL.
// It lets DRL serve edge or sidecar ingress directly over HTTP/HTTPS without
// requiring a separate Envoy sidecar, routing traffic through DRL's local
// rate-limiting accounting rules before forwarding to upstream services.
package proxy

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/metrics"
)

// BlocklistChecker checks whether an entity key is currently blocked.
// Satisfied by cache.BlocklistCache.
type BlocklistChecker interface {
	IsBlockedWithExpiration(key string) (time.Time, bool)
}

// AccountingProcessor processes an incoming request asynchronously for
// rate-limit accounting and supplies the canonical entity key.
// Satisfied by accounting.Engine.
type AccountingProcessor interface {
	Process(sourceIP, path string, headers map[string]string)
	BuildEntityKey(sourceIP, path string, headers map[string]string) string
}

// Server is the embedded reverse proxy server.
type Server struct {
	cfg        config.EmbeddedProxyConfig
	blocklist  BlocklistChecker
	accounting AccountingProcessor
	metrics    *metrics.Metrics
	server     *http.Server
	cancel     context.CancelFunc
	logger     *slog.Logger
}

// NewServer creates a new embedded proxy Server.
//
//   - blocklist and accounting may be nil (rate-limiting is then skipped).
//   - metricsManager may be nil (proxy metrics are then silently disabled).
func NewServer(
	cfg config.EmbeddedProxyConfig,
	blocklist BlocklistChecker,
	accounting AccountingProcessor,
	metricsManager *metrics.Metrics,
) (*Server, error) {
	return &Server{
		cfg:        cfg,
		blocklist:  blocklist,
		accounting: accounting,
		metrics:    metricsManager,
		logger:     slog.With("component", "embedded-proxy"),
	}, nil
}

// Start builds the chi router, starts the HTTP/HTTPS listener in a background
// goroutine, and returns immediately. Call Stop to shut down gracefully.
// ctx controls the lifetime of background workers (e.g. DNS watchers).
func (s *Server) Start(ctx context.Context) error {
	workerCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	router, err := s.buildRouter(workerCtx)
	if err != nil {
		cancel()
		return fmt.Errorf("embedded-proxy: build router: %w", err)
	}

	s.server = &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	if s.cfg.TLS.Enabled {
		tlsCfg, tlsErr := buildTLSConfig(s.cfg.TLS)
		if tlsErr != nil {
			cancel()
			return fmt.Errorf("embedded-proxy: build TLS config: %w", tlsErr)
		}
		s.server.TLSConfig = tlsCfg

		go func() {
			s.logger.Info("starting embedded proxy (TLS)", "listen", s.cfg.Listen)
			// Cert/key are already loaded into TLSConfig; pass empty strings.
			if err := s.server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				s.logger.Error("embedded proxy TLS listener error", "err", err)
			}
		}()
	} else {
		go func() {
			s.logger.Info("starting embedded proxy (plain HTTP)", "listen", s.cfg.Listen)
			if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				s.logger.Error("embedded proxy listener error", "err", err)
			}
		}()
	}

	return nil
}

// Stop performs a graceful shutdown of the proxy listener and cancels all
// background workers (DNS watchers). It honours the deadline on ctx.
func (s *Server) Stop(ctx context.Context) error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

// buildRouter constructs the chi.Mux with one route handler per configured
// virtual-host + route-prefix combination.
// When a host has an OIDC issuer configured, routes with require-auth=true are
// wrapped with the OIDC middleware (outermost) before rate-limiting and proxying.
func (s *Server) buildRouter(ctx context.Context) (http.Handler, error) {
	r := chi.NewRouter()

	// Initialise one OIDC verifier per host that has an issuer configured.
	// A failed provider init is non-fatal: DRL logs a warning and skips auth for that host.
	verifiers := make(map[string]*oidcVerifier, len(s.cfg.Hosts))
	for _, hostCfg := range s.cfg.Hosts {
		if hostCfg.OIDC.Issuer == "" {
			continue
		}
		v, err := newOIDCVerifier(ctx, hostCfg.OIDC)
		if err != nil {
			s.logger.Warn("oidc: provider init failed, skipping auth for host",
				"host", hostCfg.Hostname, "err", err)
			continue
		}
		if v != nil {
			verifiers[hostCfg.Hostname] = v
		}
	}

	for _, hostCfg := range s.cfg.Hosts {
		hostCfg := hostCfg // capture loop variable
		for _, routeCfg := range hostCfg.Routes.Routes {
			routeCfg := routeCfg

			rp, err := s.buildReverseProxy(ctx, hostCfg.Hostname, routeCfg)
			if err != nil {
				return nil, fmt.Errorf(
					"embedded-proxy: build route %s%s: %w",
					hostCfg.Hostname, routeCfg.Prefix, err,
				)
			}

			pattern := routeCfg.Prefix
			if !strings.HasSuffix(pattern, "*") {
				pattern = strings.TrimSuffix(pattern, "/") + "/*"
			}

			// Middleware chain (outermost → innermost): OIDC → rateLimit → reverseProxy
			handler := s.rateLimitMiddleware(hostCfg.Hostname, routeCfg.Prefix, rp)

			if routeCfg.RequireAuth {
				if v, ok := verifiers[hostCfg.Hostname]; ok {
					handler = s.oidcMiddleware(hostCfg.Hostname, v, routeCfg.Scopes, handler)
				} else {
					s.logger.Warn("embedded-proxy: require-auth=true but no OIDC issuer configured",
						"host", hostCfg.Hostname, "route", routeCfg.Prefix)
				}
			}

			r.Handle(pattern, handler)
		}
	}

	return r, nil
}

// buildReverseProxy creates an httputil.ReverseProxy for the given route.
// When balance-strategy is "dns-round-robin" a RoundRobinDirector is started
// in the background to keep the IP pool fresh.
func (s *Server) buildReverseProxy(ctx context.Context, hostname string, route config.ProxyRouteConfig) (http.Handler, error) {
	upstreamURL, err := url.Parse(route.Upstream)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream URL %q: %w", route.Upstream, err)
	}

	rp := &httputil.ReverseProxy{}

	if strings.EqualFold(route.BalanceStrategy, "dns-round-robin") {
		interval := route.DNSRefreshInterval
		if interval <= 0 {
			interval = 5 * time.Second
		}

		director := NewRoundRobinDirector()
		go director.WatchDNS(ctx, upstreamURL.Hostname(), interval)

		rp.Director = func(req *http.Request) {
			req.URL.Scheme = upstreamURL.Scheme
			req.URL.Host = upstreamURL.Host
			req.Host = upstreamURL.Host
			director.DirectRequest(req)
			req.Header.Del("Te")
			req.Header.Del("Trailers")
		}
	} else {
		rp.Director = func(req *http.Request) {
			req.URL.Scheme = upstreamURL.Scheme
			req.URL.Host = upstreamURL.Host
			req.Host = upstreamURL.Host
			req.Header.Del("Te")
			req.Header.Del("Trailers")
		}
	}

	if s.metrics != nil {
		routePrefix := route.Prefix
		rp.ModifyResponse = func(resp *http.Response) error {
			start, _ := resp.Request.Context().Value(requestStartKey{}).(time.Time)
			if !start.IsZero() {
				s.metrics.ObserveProxyLatency(hostname, routePrefix, time.Since(start).Seconds())
			}
			s.metrics.IncProxyRequest(hostname, routePrefix, fmt.Sprintf("%d", resp.StatusCode))
			return nil
		}
		rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			s.metrics.IncProxyError(hostname, routePrefix, "upstream")
			http.Error(w, "Bad Gateway", http.StatusBadGateway)
		}
	}

	return rp, nil
}

type requestStartKey struct{}

// rateLimitMiddleware checks the incoming request against DRL's blocklist and
// asynchronously processes the request through the accounting engine before
// forwarding to the upstream handler.
func (s *Server) rateLimitMiddleware(hostname, routePrefix string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), requestStartKey{}, time.Now())
		r = r.WithContext(ctx)

		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}

		// Collect all request headers for entity matching.
		headers := make(map[string]string, len(r.Header))
		for k := range r.Header {
			headers[strings.ToLower(k)] = r.Header.Get(k)
		}

		// Step 1: Check blocklist.
		if s.blocklist != nil && s.accounting != nil {
			key := s.accounting.BuildEntityKey(ip, r.URL.Path, headers)
			if _, blocked := s.blocklist.IsBlockedWithExpiration(key); blocked {
				if s.metrics != nil {
					s.metrics.IncProxyRequest(hostname, routePrefix, "429")
				}
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				return
			}
		}

		// Step 2: Forward the request.
		next.ServeHTTP(w, r)

		// Step 3: Asynchronous accounting (fire-and-forget, matching the gRPC path).
		if s.accounting != nil {
			go s.accounting.Process(ip, r.URL.Path, headers)
		}
	})
}

// buildTLSConfig decodes base64-encoded PEM cert and key from cfg and builds a
// *tls.Config. Certificate material is never written to disk.
func buildTLSConfig(cfg config.EmbeddedProxyTLSConfig) (*tls.Config, error) {
	certPEM, err := base64.StdEncoding.DecodeString(cfg.Cert)
	if err != nil {
		return nil, fmt.Errorf("decode base64 cert: %w", err)
	}
	keyPEM, err := base64.StdEncoding.DecodeString(cfg.Key)
	if err != nil {
		return nil, fmt.Errorf("decode base64 key: %w", err)
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse X509 key pair: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}
