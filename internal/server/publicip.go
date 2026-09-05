package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// metadataBudget bounds the whole search. The link-local metadata address
// either answers immediately, on a cloud instance, or fails immediately, on
// everything else — the budget only matters on a network that blackholes it
// instead of refusing, and there it is the difference between a pause and a
// hang at startup.
const metadataBudget = 700 * time.Millisecond

// Reach describes how far the address a server is advertising actually goes.
type Reach int

const (
	// ReachAdvertised means the operator said what the address is, and we
	// take their word for it.
	ReachAdvertised Reach = iota
	// ReachPublic means a globally routable address: anyone can reach it.
	ReachPublic
	// ReachLocal means a private address: this network only.
	ReachLocal
)

// publicHost works out the host to put in invites, and how far it reaches.
//
// Most providers — DigitalOcean, Hetzner, Linode, Vultr — put the public
// address directly on the interface, so the routed address is already the
// answer. AWS, GCP and Azure hand the instance a private address and map a
// public one in front of it, and the only place that mapping is written down
// is the provider's metadata service.
func publicHost() (string, Reach) {
	local := LocalIP()
	if ip := net.ParseIP(local); ip != nil && isGloballyRoutable(ip) {
		return local, ReachPublic
	}
	if ip := fromMetadata(); ip != "" {
		return ip, ReachPublic
	}
	return local, ReachLocal
}

// isGloballyRoutable reports whether the outside world could route to this
// address. Carrier-grade NAT space counts as private: it is what an ISP or a
// mesh hands out, and neither is reachable from the internet at large.
func isGloballyRoutable(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return false
	}
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 64 {
		return false // 100.64.0.0/10, RFC 6598
	}
	return ip.IsGlobalUnicast()
}

// probe is one provider's way of being asked for the instance's public
// address.
type probe struct {
	name    string
	method  string
	url     string
	headers map[string]string
	// token, when set, is fetched first and sent as tokenHeader — AWS's
	// IMDSv2 refuses to answer without it.
	token       string
	tokenHeader string
}

var probes = []probe{
	{
		name:        "aws",
		method:      http.MethodGet,
		url:         "http://169.254.169.254/latest/meta-data/public-ipv4",
		token:       "http://169.254.169.254/latest/api/token",
		tokenHeader: "X-aws-ec2-metadata-token",
	},
	{
		name:    "gcp",
		method:  http.MethodGet,
		url:     "http://169.254.169.254/computeMetadata/v1/instance/network-interfaces/0/access-configs/0/external-ip",
		headers: map[string]string{"Metadata-Flavor": "Google"},
	},
	{
		name:    "azure",
		method:  http.MethodGet,
		url:     "http://169.254.169.254/metadata/instance/network/interface/0/ipv4/ipAddress/0/publicIpAddress?api-version=2021-02-01",
		headers: map[string]string{"Metadata": "true"},
	},
	{
		name:   "digitalocean",
		method: http.MethodGet,
		url:    "http://169.254.169.254/metadata/v1/interfaces/public/0/ipv4/address",
	},
}

// A machine's public address does not change underneath a running process,
// and the search costs a round trip that a blackholing network turns into the
// full budget — so it is made once and remembered.
var (
	metadataOnce sync.Once
	metadataIP   string
)

// fromMetadata asks every provider at once and takes the first usable answer.
func fromMetadata() string {
	metadataOnce.Do(func() { metadataIP = askMetadata() })
	return metadataIP
}

func askMetadata() string {
	ctx, cancel := context.WithTimeout(context.Background(), metadataBudget)
	defer cancel()

	found := make(chan string, len(probes))
	for _, p := range probes {
		go func(p probe) {
			found <- p.run(ctx)
		}(p)
	}
	for range probes {
		select {
		case ip := <-found:
			if ip != "" {
				return ip
			}
		case <-ctx.Done():
			return ""
		}
	}
	return ""
}

func (p probe) run(ctx context.Context) string {
	client := &http.Client{
		// No proxy and no redirects: a metadata address is next door by
		// definition, and an HTTP_PROXY in the environment would send this
		// somewhere it has no business going.
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	headers := map[string]string{}
	for k, v := range p.headers {
		headers[k] = v
	}
	if p.token != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, p.token, nil)
		if err != nil {
			return ""
		}
		req.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "60")
		resp, err := client.Do(req)
		if err != nil {
			return ""
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return ""
		}
		headers[p.tokenHeader] = strings.TrimSpace(string(body))
	}

	req, err := http.NewRequestWithContext(ctx, p.method, p.url, nil)
	if err != nil {
		return ""
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return ""
	}

	got := strings.TrimSpace(string(body))
	ip := net.ParseIP(got)
	if ip == nil || !isGloballyRoutable(ip) {
		return ""
	}
	return got
}

// hostOf pulls the hostname out of a base URL or a bare host string.
func hostOf(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	if h := u.Hostname(); h != "" {
		return h
	}
	return ""
}
