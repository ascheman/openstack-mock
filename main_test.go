package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// buildDispatcherForTest builds a dispatcher using in-memory backend servers
// and the NewDispatcher function from main.go.
func buildDispatcherForTest(t *testing.T) http.Handler {
	t.Helper()

	mkBackend := func(name string) (*httptest.Server, string) {
		hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Backend", name)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(name + ": " + r.URL.Path))
		}))
		return hs, hs.URL
	}

	computeSrv, computeBase := mkBackend("compute")
	networkingSrv, networkingBase := mkBackend("networking")
	lbSrv, lbBase := mkBackend("loadbalancer")
	blockSrv, blockBase := mkBackend("blockstorage")
	dnsSrv, dnsBase := mkBackend("dns")
	imageSrv, imageBase := mkBackend("image")

	t.Cleanup(func() {
		computeSrv.Close()
		networkingSrv.Close()
		lbSrv.Close()
		blockSrv.Close()
		dnsSrv.Close()
		imageSrv.Close()
	})

	return NewDispatcher(Endpoints{
		Compute:      computeBase,
		Networking:   networkingBase,
		LoadBalancer: lbBase,
		BlockStorage: blockBase,
		DNS:          dnsBase,
		Image:        imageBase,
	})
}

func TestTokenEndpoint(t *testing.T) {
	ts := httptest.NewServer(buildDispatcherForTest(t))
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v3/auth/tokens", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d", resp.StatusCode)
	}
	if tok := resp.Header.Get("X-Subject-Token"); tok == "" {
		t.Fatalf("expected X-Subject-Token header to be set")
	}
}

func TestIdentityEndpoint(t *testing.T) {
	ts := httptest.NewServer(buildDispatcherForTest(t))
	defer ts.Close()

	// GET should be 200 and JSON
	resp, err := http.Get(ts.URL + IdentityPath)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	//nolint:errcheck // Response body Close() call
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	// HEAD should be 200
	req, _ := http.NewRequest(http.MethodHead, ts.URL+IdentityPath, nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HEAD failed: %v", err)
	}
	//nolint:errcheck // Response body Close() call
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for HEAD, got %d", resp.StatusCode)
	}

	// PUT should be 405
	req, _ = http.NewRequest(http.MethodPut, ts.URL+IdentityPath, nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT failed: %v", err)
	}
	//nolint:errcheck // Response body Close() call
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for unsupported method, got %d", resp.StatusCode)
	}
}

func TestRoutingAllPrefixes(t *testing.T) {
	ts := httptest.NewServer(buildDispatcherForTest(t))
	defer ts.Close()

	paths := []string{
		"/servers", "/servers/",
		"/os-keypairs", "/os-keypairs/",
		"/flavors", "/flavors/",
		"/os-instance-actions/", // only with slash registered in dispatcher
		"/images", "/images/",
		"/volumes", "/volumes/",
		"/types", "/types/",
		"/os-availability-zone",
		"/zones", "/zones/",
		"/networks", "/networks/",
		"/ports", "/ports/",
		"/routers", "/routers/",
		"/security-groups", "/security-groups/",
		"/security-group-rules", "/security-group-rules/",
		"/subnets", "/subnets/",
		"/floatingips", "/floatingips/",
		"/lbaas/listeners", "/lbaas/listeners/",
		"/lbaas/loadbalancers", "/lbaas/loadbalancers/",
		"/lbaas/pools", "/lbaas/pools/",
	}

	client := &http.Client{Timeout: 10 * time.Second}
	for _, p := range paths {
		resp, err := client.Get(ts.URL + p)
		if err != nil {
			t.Fatalf("GET %s failed: %v", p, err)
		}
		//nolint:errcheck // Response body Close() call
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200 from backend for %s, got %d", p, resp.StatusCode)
		}
	}
}

func TestUnknownPath404(t *testing.T) {
	ts := httptest.NewServer(buildDispatcherForTest(t))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/does/not/exist")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	//nolint:errcheck // Response body Close() call
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown path, got %d", resp.StatusCode)
	}
}
