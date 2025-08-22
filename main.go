// Command openstack-mock runs the miscellaneous OpenStack mock services
// implemented under kops/cloudmock/openstack, prints their base endpoints, and
// exposes a single dispatcher endpoint that forwards requests to the
// appropriate mock service based on URI prefixes.
//
// This is intended for local development and testing. Each mock service spins up
// its own in-memory HTTP server (using net/http/httptest) listening on a random
// localhost port. This program wires up the default set of mock services and
// keeps running until interrupted.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"k8s.io/klog/v2"
	"k8s.io/kops/pkg/testutils"
)

func main() {
	// Reduce klog noise unless overridden
	if os.Getenv("KLOG_V") == "" {
		_ = os.Setenv("KLOG_V", "1")
	}

	port := flag.Int("port", 19090, "Port for the dispatcher to listen on")
	listen := flag.String("listen", "127.0.0.1", "Address/interface for the dispatcher to bind to")
	flag.Parse()

	klog.Infof("Starting OpenStack mock services...")

	cloud := testutils.SetupMockOpenstack()

	// For interactive use, clear any pre-seeded images so listing returns an empty set.
	if cloud.MockImageClient != nil {
		cloud.MockImageClient.Reset()
	}

	computeBase := cloud.ComputeClient().Endpoint
	networkingBase := cloud.NetworkingClient().Endpoint
	lbBase := cloud.LoadBalancerClient().Endpoint
	blockBase := cloud.BlockStorageClient().Endpoint
	dnsBase := cloud.DNSClient().Endpoint
	imageBase := cloud.ImageClient().Endpoint

	// Print service endpoints for convenience
	fmt.Println("OpenStack mock service endpoints (set your clients to these base URLs):")
	fmt.Printf("  compute      (nova):        %s\n", computeBase)
	fmt.Printf("  networking   (neutron):     %s\n", networkingBase)
	fmt.Printf("  loadbalancer (octavia):     %s\n", lbBase)
	fmt.Printf("  blockstorage (cinder):      %s\n", blockBase)
	fmt.Printf("  dns          (designate):   %s\n", dnsBase)
	fmt.Printf("  image        (glance):      %s\n", imageBase)

	// Build reverse proxies for each backend
	mkProxy := func(base string) *httputil.ReverseProxy {
		u, err := url.Parse(base)
		if err != nil {
			log.Fatalf("invalid backend URL %q: %v", base, err)
		}
		rp := httputil.NewSingleHostReverseProxy(u)
		// Preserve the original Host header so handlers that rely on it still work if needed.
		rp.Director = func(req *http.Request) {
			req.URL.Scheme = u.Scheme
			req.URL.Host = u.Host
			// Keep the original path and rawpath; backend muxes expect the same path prefixes
			// (e.g., /images, /servers).
			// Ensure we don't accidentally double-prefix; since backend Endpoint ends with '/',
			// we don't join paths here.
			if req.Header.Get("X-Forwarded-Host") == "" {
				req.Header.Set("X-Forwarded-Host", req.Host)
			}
			req.Host = u.Host
		}
		return rp
	}

	computeProxy := mkProxy(computeBase)
	networkingProxy := mkProxy(networkingBase)
	lbProxy := mkProxy(lbBase)
	blockProxy := mkProxy(blockBase)
	dnsProxy := mkProxy(dnsBase)
	imageProxy := mkProxy(imageBase)

	// Routing table: URI prefix -> proxy
	// Note: order matters; longer/more specific prefixes should be checked first.
	var routes = map[string]*httputil.ReverseProxy{
		// Compute (Nova)
		"/servers/":             computeProxy,
		"/servers":              computeProxy,
		"/os-keypairs/":         computeProxy,
		"/os-keypairs":          computeProxy,
		"/flavors/":             computeProxy,
		"/flavors":              computeProxy,
		"/os-instance-actions/": computeProxy, // included for completeness
		// Image (Glance)
		"/images/": imageProxy,
		"/images":  imageProxy,
		// BlockStorage (Cinder)
		"/volumes/":             blockProxy,
		"/volumes":              blockProxy,
		"/types/":               blockProxy,
		"/types":                blockProxy,
		"/os-availability-zone": blockProxy,
		// DNS (Designate)
		"/zones/": dnsProxy,
		"/zones":  dnsProxy,
		// Networking (Neutron)
		"/networks/":             networkingProxy,
		"/networks":              networkingProxy,
		"/ports/":                networkingProxy,
		"/ports":                 networkingProxy,
		"/routers/":              networkingProxy,
		"/routers":               networkingProxy,
		"/security-groups/":      networkingProxy,
		"/security-groups":       networkingProxy,
		"/security-group-rules/": networkingProxy,
		"/security-group-rules":  networkingProxy,
		"/subnets/":              networkingProxy,
		"/subnets":               networkingProxy,
		"/floatingips/":          networkingProxy,
		"/floatingips":           networkingProxy,
		// LoadBalancer (Octavia)
		"/lbaas/listeners/":     lbProxy,
		"/lbaas/listeners":      lbProxy,
		"/lbaas/loadbalancers/": lbProxy,
		"/lbaas/loadbalancers":  lbProxy,
		"/lbaas/pools/":         lbProxy,
		"/lbaas/pools":          lbProxy,
	}

	// Prepare ordered list of prefixes for deterministic matching
	prefixes := make([]string, 0, len(routes))
	for p := range routes {
		prefixes = append(prefixes, p)
	}
	// Sort by length descending to match the most specific path first
	sort.Slice(prefixes, func(i, j int) bool { return len(prefixes[i]) > len(prefixes[j]) })

	// Minimal Keystone v3 token issuance handler
	tokenHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// Generate a token and set X-Subject-Token header as Keystone does.
		tok := uuid.New().String()
		w.Header().Set("X-Subject-Token", tok)
		// Build a minimal token document
		// Build a minimal token document with a service catalog
		region := "RegionOne"
		makeEndpoint := func(url string) map[string]interface{} {
			return map[string]interface{}{
				"id":        uuid.New().String(),
				"interface": "public",
				"region":    region,
				"region_id": region,
				"url":       url,
			}
		}
		catalog := []map[string]interface{}{
			{
				"id":        uuid.New().String(),
				"type":      "compute",
				"name":      "nova",
				"endpoints": []map[string]interface{}{makeEndpoint(computeBase)},
			},
			{
				"id":        uuid.New().String(),
				"type":      "network",
				"name":      "neutron",
				"endpoints": []map[string]interface{}{makeEndpoint(networkingBase)},
			},
			{
				"id":        uuid.New().String(),
				"type":      "load-balancer",
				"name":      "octavia",
				"endpoints": []map[string]interface{}{makeEndpoint(lbBase)},
			},
			{
				"id":        uuid.New().String(),
				"type":      "block-storage",
				"name":      "cinder",
				"endpoints": []map[string]interface{}{makeEndpoint(blockBase)},
			},
			{
				"id":        uuid.New().String(),
				"type":      "dns",
				"name":      "designate",
				"endpoints": []map[string]interface{}{makeEndpoint(dnsBase)},
			},
			{
				"id":        uuid.New().String(),
				"type":      "image",
				"name":      "glance",
				"endpoints": []map[string]interface{}{makeEndpoint(imageBase)},
			},
			{
				"id":   uuid.New().String(),
				"type": "identity",
				"name": "keystone",
				"endpoints": []map[string]interface{}{makeEndpoint(func() string {
					base := fmt.Sprintf("%s://%s", func() string {
						if r.Header.Get("X-Forwarded-Proto") != "" {
							return r.Header.Get("X-Forwarded-Proto")
						}
						if r.URL.Scheme != "" {
							return r.URL.Scheme
						}
						if r.TLS != nil {
							return "https"
						}
						return "http"
					}(), r.Host)
					return base + "/v3/identity"
				}())},
			},
		}
		resp := map[string]interface{}{
			"token": map[string]interface{}{
				"expires_at": time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339),
				"project": map[string]string{
					"id":   "mock-project-id",
					"name": "mock",
				},
				"user": map[string]string{
					"id":   "mock-user-id",
					"name": "mock-user",
				},
				"catalog": catalog,
			},
		}
		b, _ := json.Marshal(resp)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(b)
	}

	// Minimal Identity discovery endpoint under /v3/identity
	identityHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		base := fmt.Sprintf("%s://%s", func() string {
			if r.Header.Get("X-Forwarded-Proto") != "" {
				return r.Header.Get("X-Forwarded-Proto")
			}
			if r.URL.Scheme != "" {
				return r.URL.Scheme
			}
			if r.TLS != nil {
				return "https"
			}
			return "http"
		}(), r.Host)
		// Construct a lightweight, but plausible identity discovery document
		resp := map[string]interface{}{
			"identity": map[string]interface{}{
				"version": "v3",
				"status":  "ok",
				"updated": time.Now().UTC().Format(time.RFC3339),
				"links": []map[string]string{
					{"rel": "self", "href": base + "/v3/identity"},
				},
			},
		}
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		b, _ := json.Marshal(resp)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	}

	dispatcher := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/v3/auth/tokens" {
			tokenHandler(w, r)
			return
		}
		if path == "/v3/identity" || strings.HasPrefix(path, "/v3/identity/") {
			identityHandler(w, r)
			return
		}
		for _, p := range prefixes {
			if strings.HasPrefix(path, p) {
				routes[p].ServeHTTP(w, r)
				return
			}
		}
		// Default: 404 with some guidance
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("no route for path: " + path + "\n"))
	})

	addr := fmt.Sprintf("%s:%d", *listen, *port)
	server := &http.Server{Addr: addr, Handler: dispatcher}

	go func() {
		klog.Infof("Dispatcher listening on http://%s", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("dispatcher failed: %v", err)
		}
	}()

	fmt.Println("Press Ctrl-C to stop.")

	// Wait for termination signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	klog.Infof("Shutting down OpenStack mock services...")
}
