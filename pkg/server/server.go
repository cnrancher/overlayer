package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/cnrancher/overlayer/pkg/config"
	"github.com/cnrancher/overlayer/pkg/utils"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/http2"
)

type registryServer struct {
	serverURL *url.URL // proxy server actual http url (localURL)
	addr      string   // proxy server bind address
	port      int      // proxy server bind port

	allowedHeaders map[string]string

	cert string
	key  string

	remoteURL *url.URL // the proxied remote registry URL

	redirectBlobs  bool     // Enable redirect public blobs to CDN cached URL
	blobsURL       *url.URL // CDN cached blobs URL
	blobsAuthToken string   // CDN Auth Token

	insecureSkipTLSVerify bool

	// Manifest index proxy map, the manifest index should not be cached by CDN
	manifestProxyMap map[string]*httputil.ReverseProxy // map[repository]Proxy
	// Blobs proxy map, the blobs can be cached by CDN in a long period if the image is public
	blobsProxyMap map[string]*httputil.ReverseProxy // map[repository]Proxy
	// API proxy, proxy other registry v2 API requests, should not be cached by CDN
	apiProxy *httputil.ReverseProxy

	// Custom plaintext proxy map, can be cached by CDN in a short period
	plaintextProxySet map[config.Route]bool // map[route]true
	// Custom static file proxy map, can be cached by CDN in a short period
	staticFileProxySet map[config.Route]bool // map[route]true

	server *http.Server   // HTTP2 server
	mux    *http.ServeMux // HTTP request multiplexer
	errCh  chan error
}

func NewRegistryServer(
	ctx context.Context, c *config.Config,
) (*registryServer, error) {
	var err error
	s := &registryServer{
		serverURL: nil,
		addr:      c.BindAddr,
		port:      c.Port,
		remoteURL: nil,

		allowedHeaders: maps.Clone(c.AllowedHeaders),

		cert: c.CertFile,
		key:  c.KeyFile,

		redirectBlobs:  c.RedirectBlobsLocation.Enabled,
		blobsURL:       nil,
		blobsAuthToken: "",

		insecureSkipTLSVerify: c.InsecureSkipTLSVerify,

		manifestProxyMap:   make(map[string]*httputil.ReverseProxy),
		blobsProxyMap:      make(map[string]*httputil.ReverseProxy),
		apiProxy:           nil,
		plaintextProxySet:  make(map[config.Route]bool),
		staticFileProxySet: make(map[config.Route]bool),

		errCh: make(chan error),
	}
	s.serverURL, err = url.Parse(c.ServerURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse server URL %s: %w", c.ServerURL, err)
	}
	s.remoteURL, err = url.Parse(c.RemoteURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse remote URL %s: %w", c.RemoteURL, err)
	}
	if s.redirectBlobs {
		s.blobsURL, err = url.Parse(c.RedirectBlobsLocation.URL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse blobs URL %s: %w",
				c.RedirectBlobsLocation.URL, err)
		}
		if c.RedirectBlobsLocation.AuthConfig.TokenEnvKey != "" {
			s.blobsAuthToken = strings.TrimSpace(os.Getenv(c.RedirectBlobsLocation.AuthConfig.TokenEnvKey))
		} else {
			s.blobsAuthToken = c.RedirectBlobsLocation.AuthConfig.Token
		}
	}
	if err := s.registerAPIFactory(); err != nil {
		return nil, fmt.Errorf("failed to register API factory: %w", err)
	}
	for _, r := range c.Repositories {
		if err := s.registerRepository(&r); err != nil {
			return nil, fmt.Errorf("failed to register repository %s: %w", r.Name, err)
		}
	}

	for _, r := range c.CustomRoutes {
		if r.PlainText != nil {
			s.registerPlainText(r)
			continue
		}
		if r.StaticFile != "" {
			s.registerStaticFile(r)
			continue
		}
	}

	return s, nil
}

func (s *registryServer) registerPlainText(r config.Route) {
	s.plaintextProxySet[r] = true
}

func (s *registryServer) registerStaticFile(r config.Route) {
	s.staticFileProxySet[r] = true
}

func (s *registryServer) registerManifestFactory(r *config.Repository) error {
	// https://registry_url/v2/REPO_NAME/manifests/latest
	manifestPrefixURL := s.remoteURL.JoinPath("v2", r.Name)
	f := &factory{
		kind:      ManifestFactory,
		localURL:  s.serverURL,
		remoteURL: s.remoteURL,

		redirectBlobs:  s.redirectBlobs,
		blobsURL:       s.blobsURL,
		blobsAuthToken: s.blobsAuthToken,

		prefixURL:             manifestPrefixURL,
		insecureSkipTLSVerify: s.insecureSkipTLSVerify,

		serverErrCh: s.errCh,
	}
	f.errorHandler = f.defaultErrorHandler
	f.modifyResponse = f.defaultModifyResponse
	f.director = f.defaultDirector
	s.manifestProxyMap[r.Name] = f.Proxy()

	logrus.Debugf("Registered repository [%s] with manifest URL [%s] [privateRepo: %v]",
		r.Name, manifestPrefixURL, r.Private)

	return nil
}

func (s *registryServer) registerBlobsFactory(r *config.Repository) error {
	// https://registry_url/v2/REPO_NAME/blobs/sha256:aabbccdd....
	blobsPrefixURL := s.remoteURL.JoinPath("v2", r.Name, "blobs")
	f := &factory{
		kind:      BlobsFactory,
		localURL:  s.serverURL,
		remoteURL: s.remoteURL,
		prefixURL: blobsPrefixURL,

		redirectBlobs:  s.redirectBlobs,
		blobsURL:       s.blobsURL,
		blobsAuthToken: s.blobsAuthToken,

		privateRepo:           r.Private,
		insecureSkipTLSVerify: s.insecureSkipTLSVerify,

		serverErrCh: s.errCh,
	}
	f.errorHandler = f.defaultErrorHandler
	f.modifyResponse = f.defaultModifyResponse
	f.director = f.defaultDirector
	s.blobsProxyMap[r.Name] = f.Proxy()

	logrus.Debugf("Registered repository [%s] with blobs URL [%s] [privateRepo: %v]",
		r.Name, blobsPrefixURL, r.Private)

	return nil
}

func (s *registryServer) registerAPIFactory() error {
	// https://registry_url/
	f := &factory{
		kind:      APIFactory,
		localURL:  s.serverURL,
		remoteURL: s.remoteURL,
		prefixURL: s.remoteURL,

		redirectBlobs:  s.redirectBlobs,
		blobsURL:       s.blobsURL,
		blobsAuthToken: s.blobsAuthToken,

		privateRepo:           true, // Set to true for other API requests
		insecureSkipTLSVerify: s.insecureSkipTLSVerify,

		serverErrCh: s.errCh,
	}
	f.errorHandler = f.defaultErrorHandler
	f.modifyResponse = f.defaultModifyResponse
	f.director = f.defaultDirector
	s.apiProxy = f.Proxy()

	logrus.Debugf("Registered default API request proxy")

	return nil
}

func (s *registryServer) registerRepository(r *config.Repository) error {
	if err := s.registerManifestFactory(r); err != nil {
		return fmt.Errorf("register manifest factory on repo [%v]: %w", r.Name, err)
	}
	if err := s.registerBlobsFactory(r); err != nil {
		return fmt.Errorf("register blobs factory on repo [%v]: %w", r.Name, err)
	}
	return nil
}

func (s *registryServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	logrus.Debugf("Proxy path [%v]", path)
	if len(s.allowedHeaders) > 0 {
		for k, v := range s.allowedHeaders {
			if r.Header.Get(k) != v {
				logrus.Infof("block [%v] request %q on header key %q mismatch:",
					r.RemoteAddr, r.URL, k)
				w.WriteHeader(http.StatusForbidden)
				return
			}
		}
	}

	switch utils.DetectURLType(path) {
	case "manifest":
		for repo, fn := range s.manifestProxyMap {
			if !strings.HasPrefix(path, fmt.Sprintf("/v2/%s/", repo)) {
				continue
			}
			fn.ServeHTTP(w, r)
			return
		}
	case "blobs":
		for repo, fn := range s.blobsProxyMap {
			if !strings.HasPrefix(path, fmt.Sprintf("/v2/%s/", repo)) {
				continue
			}
			fn.ServeHTTP(w, r)
			return
		}
	default:
		for r := range s.plaintextProxySet {
			if !matchCustomRoute(&r, path) {
				continue
			}

			if r.PlainText.Status != 0 {
				w.WriteHeader(r.PlainText.Status)
			}
			w.Write([]byte(r.PlainText.Content))
			logrus.Debugf("response plaintext path [%v] status [%v] content [%v]",
				path, r.PlainText.Status, r.PlainText.Content)
			return
		}

		for r := range s.staticFileProxySet {
			if !matchCustomRoute(&r, path) {
				continue
			}
			if r.StaticFile == "" {
				continue
			}

			f, err := os.Open(r.StaticFile)
			if err != nil {
				logrus.Warnf("failed to open file %q: %v", r.StaticFile, err)
				if os.IsNotExist(err) {
					w.WriteHeader(http.StatusNotFound)
					w.Write([]byte(err.Error()))
					return
				}
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(err.Error()))
			}
			defer f.Close()

			if _, err := io.Copy(w, f); err != nil {
				logrus.Errorf("Failed to response file content: %q: %v",
					r.StaticFile, err)
				return
			}
			logrus.Debugf("response file [%v] prefix [%v]",
				r.StaticFile, path)
			return
		}
	}

	s.apiProxy.ServeHTTP(w, r)
}

func (s *registryServer) initServer() error {
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("/", s.ServeHTTP)
	addr := fmt.Sprintf("%v:%v", s.addr, s.port)
	scheme := "http"
	if s.cert != "" && s.key != "" {
		scheme = "https"
	}
	s.server = &http.Server{
		Addr:              addr,
		Handler:           s.mux,
		ReadHeaderTimeout: time.Second * 10,
		TLSConfig: &tls.Config{
			InsecureSkipVerify: s.insecureSkipTLSVerify,
		},
	}
	if err := http2.ConfigureServer(s.server, &http2.Server{}); err != nil {
		return fmt.Errorf("failed to configure http2 server: %v", err)
	}
	logrus.Infof("server listen on %v://%v", scheme, addr)
	return nil
}

func (s *registryServer) waitServerShutDown(ctx context.Context) error {
	select {
	case err := <-s.errCh:
		timeoutCtx, cancel := context.WithTimeout(ctx, time.Second*5)
		s.server.Shutdown(timeoutCtx)
		cancel()
		return err
	case <-ctx.Done():
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
		s.server.Shutdown(timeoutCtx)
		cancel()
		logrus.Warnf("%v", ctx.Err())
	}
	return nil
}

func (s *registryServer) Serve(ctx context.Context) error {
	if err := s.initServer(); err != nil {
		return err
	}
	go func() {
		var err error
		if s.cert == "" {
			err = s.server.ListenAndServe()
		} else {
			err = s.server.ListenAndServeTLS(s.cert, s.key)
		}

		if err != nil {
			s.errCh <- fmt.Errorf("failed to start server: %w", err)
		}
	}()
	return s.waitServerShutDown(ctx)
}

func matchCustomRoute(r *config.Route, path string) bool {
	switch {
	case r.Path != "":
		return r.Path == path
	case r.Prefix != "":
		return strings.HasPrefix(path, r.Prefix)
	}
	return false
}
