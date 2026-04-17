package server

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/cnrancher/overlayer/pkg/utils"
	"github.com/sirupsen/logrus"
)

type FactoryKind int

const (
	APIFactory FactoryKind = iota // Default Factory Kind
	ManifestFactory
	BlobsFactory
)

func (k *FactoryKind) String() string {
	if k == nil {
		return "<nil>"
	}
	switch *k {
	case APIFactory:
		return "API"
	case ManifestFactory:
		return "Manifest"
	case BlobsFactory:
		return "Blobs"
	default:
		return "UNKNOW"
	}
}

const (
	CacheControlHeaderKey = "Cache-Control"
	NoCacheHeader         = "no-store, no-cache, max-age=0, must-revalidate, proxy-revalidate"

	// 604800: 7 days;
	Cache7DaysHeader = "max-age=604800"
	// 864000: 10 days;
	Cache10DaysHeader = "max-age=864000"
)

type factory struct {
	kind      FactoryKind
	localURL  *url.URL
	remoteURL *url.URL
	prefixURL *url.URL

	redirectBlobs  bool
	blobsURL       *url.URL
	blobsAuthToken string

	privateRepo           bool
	insecureSkipTLSVerify bool

	director       func(r *http.Request)
	modifyResponse func(r *http.Response) error
	errorHandler   func(w http.ResponseWriter, r *http.Request, err error)

	serverErrCh chan error
}

func (f *factory) defaultDirector(r *http.Request) {
	// Change host
	r.Host = f.prefixURL.Host
	r.URL.Scheme = f.prefixURL.Scheme
	r.URL.Host = f.prefixURL.Host

	// Dump request debug data
	if logrus.GetLevel() >= logrus.DebugLevel {
		b, err := httputil.DumpRequest(r, false)
		if err != nil {
			logrus.Debugf("failed to dump request: %v", err)
		} else {
			logrus.Debugf("%v Factory MODIFIED REQUEST %q\n%v",
				f.kind.String(), r.URL.Path, string(b))
		}
	}
}

func (f *factory) hookLocationHeader(r *http.Response) error {
	location := r.Header.Get("Location")
	if location == "" {
		return nil
	}
	if location == f.localURL.String() {
		// Skip if the Location header is already updated
		return nil
	}

	switch r.Request.Method {
	case http.MethodGet, http.MethodHead:
		switch f.kind {
		case BlobsFactory:
			return f.locationHookGetPublic(r, location)
		default:
			return f.locationHookGetPrivate(r, location)
		}
	default:
		// For non GET/HEAD method, update the location URL directly
		remote := f.remoteURL.String()
		local := f.localURL.String()
		location = strings.ReplaceAll(location, remote, local)
		r.Header.Set("Location", location)
		logrus.Debugf("hookLocationHeader: replace blobs response header [%v] the [%v] with [%v]",
			location, remote, local)
	}

	return nil
}

func (f *factory) locationHookGetPublic(r *http.Response, location string) error {
	if !f.redirectBlobs || f.blobsURL == nil {
		return f.locationHookGetPrivate(r, location)
	}

	lurl, err := url.Parse(location)
	if err != nil {
		return fmt.Errorf("failed to parse blobs original location URL: %v: %w", location, err)
	}
	lurl.Host = f.blobsURL.Host
	lurl.Scheme = f.blobsURL.Scheme
	lurl.RawQuery = ""
	lurl.Fragment = ""
	lurl.RawFragment = ""

	t := utils.UTCTimestamp()
	sign := utils.SignTypeD(lurl.Path, f.blobsAuthToken, t)
	lurl.RawQuery = fmt.Sprintf("sign=%v&t=%v", sign, t)

	newLocation := lurl.String()
	r.Header.Set("Location", newLocation)

	logrus.Debugf("hookLocationHeader: replace blobs response location URL [%v] with [%v]",
		location, newLocation)

	return nil
}

func (f *factory) locationHookGetPrivate(r *http.Response, location string) error {
	logrus.Debugf("hookLocationHeader: %q request location: %v", r.Request.Method, location)
	req, err := http.NewRequestWithContext(r.Request.Context(), r.Request.Method, location, nil)
	if err != nil {
		return fmt.Errorf("failed to create new request %q: %w", location, err)
	}
	req.Header = r.Request.Header
	res, err := utils.DoHTTPRequest(req, f.insecureSkipTLSVerify)
	if err != nil {
		return fmt.Errorf("failed to %v %q; %w", r.Request.Method, location, err)
	}
	if err := r.Body.Close(); err != nil {
		logrus.Errorf("failed to close response: %v", err)
	}
	r.Body = res.Body
	r.Header = res.Header
	r.ContentLength = res.ContentLength
	r.Status = res.Status
	r.StatusCode = res.StatusCode
	r.Proto = res.Proto
	r.Close = res.Close
	r.ProtoMajor = res.ProtoMajor
	r.ProtoMinor = res.ProtoMinor
	return nil
}

func (f *factory) hookAuthenticateHeader(r *http.Response) error {
	// Re-write the Authenticate header if exists
	// E.x. https://registry.abc.com/service/token
	auth := r.Header.Get("Www-Authenticate")
	if auth == "" {
		return nil
	}
	if strings.HasPrefix(auth, "Bearer realm=") {
		remote := f.remoteURL.String()
		local := f.localURL.String()
		if !strings.Contains(auth, remote) {
			// Skip if no need to replace
			return nil
		}
		auth = strings.ReplaceAll(auth, remote, local)
		r.Header.Set("Www-Authenticate", auth)
		logrus.Debugf("hookAuthenticateHeader: replace response header [%v] the [%v] with [%v]",
			auth, remote, local)
	}
	return nil
}

func (f *factory) hookHeaderCacheControl(r *http.Response) error {
	switch f.kind {
	case APIFactory:
		// Other API requests should not be cached
		r.Header.Set(CacheControlHeaderKey, NoCacheHeader)
	case ManifestFactory:
		// Set no-cache headers for manifest response
		// Manifest index should not be cached
		r.Header.Set(CacheControlHeaderKey, NoCacheHeader)
	case BlobsFactory:
		if f.privateRepo {
			// Do not cache private image blobs
			r.Header.Set(CacheControlHeaderKey, NoCacheHeader)
		} else {
			// Cache the public image blobs to save traffic
			// Default cache 10 days
			r.Header.Set(CacheControlHeaderKey, Cache10DaysHeader)
		}
	}
	return nil
}

func (f *factory) defaultModifyResponse(r *http.Response) error {
	if err := f.hookLocationHeader(r); err != nil {
		return err
	}
	if err := f.hookAuthenticateHeader(r); err != nil {
		return err
	}
	if err := f.hookHeaderCacheControl(r); err != nil {
		return err
	}

	if logrus.GetLevel() >= logrus.DebugLevel {
		// Dump response debug data
		b, err := httputil.DumpResponse(r, false)
		if err != nil {
			logrus.Debugf("failed to dump response: %v", err)
		} else {
			logrus.Debugf("%v Factory MODIFIED RESPONSE %q\n%v",
				f.kind.String(), r.Request.URL.Path, string(b))
		}
	}
	return nil
}

func (f *factory) defaultErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	logrus.Errorf("Error on %v factory handler [%v]: %v",
		f.kind.String(), r.URL.Path, err)
	b, _ := httputil.DumpRequest(r, true)
	logrus.Errorf("%v Factory failed request %q\n%v\n=========================\n",
		f.kind.String(), r.URL.String(), string(b))

	w.WriteHeader(http.StatusBadGateway)
	w.Write(fmt.Appendf(nil, "%v", err))

	if strings.Contains(err.Error(), http.StatusText(http.StatusBadGateway)) {
		if f.serverErrCh == nil {
			return
		}
		f.serverErrCh <- fmt.Errorf("server failed on proxy error: %w", err)
	}
}

// Proxy generates the ReverseProxy server
func (f *factory) Proxy() *httputil.ReverseProxy {
	p := httputil.NewSingleHostReverseProxy(f.prefixURL)
	p.Director = f.director
	p.ModifyResponse = f.modifyResponse
	p.ErrorHandler = f.errorHandler
	return p
}
