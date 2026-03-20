// Package xray generates Xray-core JSON configurations for the hub-and-spoke VPN architecture.
// The main server acts as a gateway: users connect to it via VLESS/SOCKS5/HTTP,
// and traffic is routed through exit nodes via VLESS tunnels.
package xray

import (
	"encoding/json"
	"fmt"
)

// XrayConfig represents the top-level Xray JSON configuration.
type XrayConfig struct {
	Log       *LogConfig       `json:"log,omitempty"`
	Inbounds  []Inbound        `json:"inbounds"`
	Outbounds []Outbound       `json:"outbounds"`
	Routing   *RoutingConfig   `json:"routing,omitempty"`
}

type LogConfig struct {
	Loglevel string `json:"loglevel"`
	Access   string `json:"access,omitempty"`
	Error    string `json:"error,omitempty"`
}

type Inbound struct {
	Tag            string          `json:"tag"`
	Port           int             `json:"port"`
	Listen         string          `json:"listen,omitempty"`
	Protocol       string          `json:"protocol"`
	Settings       json.RawMessage `json:"settings"`
	StreamSettings json.RawMessage `json:"streamSettings,omitempty"`
	Sniffing       *Sniffing       `json:"sniffing,omitempty"`
}

type Outbound struct {
	Tag            string          `json:"tag"`
	Protocol       string          `json:"protocol"`
	Settings       json.RawMessage `json:"settings,omitempty"`
	StreamSettings json.RawMessage `json:"streamSettings,omitempty"`
}

type RoutingConfig struct {
	DomainStrategy string        `json:"domainStrategy"`
	Rules          []RoutingRule `json:"rules"`
	Balancers      []Balancer    `json:"balancers,omitempty"`
}

type RoutingRule struct {
	Type        string   `json:"type"`
	Domain      []string `json:"domain,omitempty"`
	IP          []string `json:"ip,omitempty"`
	OutboundTag string   `json:"outboundTag,omitempty"`
	BalancerTag string   `json:"balancerTag,omitempty"`
}

type Balancer struct {
	Tag      string   `json:"tag"`
	Selector []string `json:"selector"`
	Strategy struct {
		Type string `json:"type"`
	} `json:"strategy"`
}

type Sniffing struct {
	Enabled      bool     `json:"enabled"`
	DestOverride []string `json:"destOverride"`
	RouteOnly    bool     `json:"routeOnly,omitempty"`
}

// ExitNode represents a VPN exit node for tunnel configuration.
type ExitNode struct {
	ID        int
	IPAddress string
	Port      int
	UUID      string // VLESS UUID for authentication
	Tag       string // Xray outbound tag
}

// MaskingRule represents a traffic masking configuration.
type MaskingRule struct {
	SourceDomain string
	MaskDomain   string
}

// GenerateHubConfig creates the Xray configuration for the main (hub) server.
func GenerateHubConfig(opts HubConfigOptions) (*XrayConfig, error) {
	cfg := &XrayConfig{
		Log: &LogConfig{Loglevel: "warning"},
	}

	// ── Inbounds ──
	// 1. VLESS Reality (VPN mode) — port 443
	vlessInbound, err := buildVLESSRealityInbound(opts)
	if err != nil {
		return nil, fmt.Errorf("build VLESS inbound: %w", err)
	}
	cfg.Inbounds = append(cfg.Inbounds, *vlessInbound)

	// 2. SOCKS5 proxy — port 1080
	socks5Inbound := buildSOCKS5Inbound(opts.SOCKS5Port)
	cfg.Inbounds = append(cfg.Inbounds, socks5Inbound)

	// 3. HTTP proxy — port 8080
	httpInbound := buildHTTPProxyInbound(opts.HTTPProxyPort)
	cfg.Inbounds = append(cfg.Inbounds, httpInbound)

	// ── Outbounds ──
	// Freedom outbound (direct)
	freedomSettings, _ := json.Marshal(map[string]string{})
	cfg.Outbounds = append(cfg.Outbounds, Outbound{
		Tag:      "direct",
		Protocol: "freedom",
		Settings: freedomSettings,
	})

	// Block outbound
	blackholeSettings, _ := json.Marshal(map[string]any{
		"response": map[string]string{"type": "none"},
	})
	cfg.Outbounds = append(cfg.Outbounds, Outbound{
		Tag:      "block",
		Protocol: "blackhole",
		Settings: blackholeSettings,
	})

	// Exit node outbounds
	var exitTags []string
	for _, node := range opts.ExitNodes {
		outbound, err := buildExitNodeOutbound(node)
		if err != nil {
			continue
		}
		cfg.Outbounds = append(cfg.Outbounds, *outbound)
		exitTags = append(exitTags, node.Tag)
	}

	// ── Routing ──
	routing := &RoutingConfig{
		DomainStrategy: "AsIs",
	}

	// Traffic masking: create outbounds and routing rules for SNI override (premium users)
	for _, rule := range opts.MaskingRules {
		tag := "masked-" + rule.MaskDomain
		// Create a freedom outbound that overrides the destination domain (SNI masking)
		maskSettings, _ := json.Marshal(map[string]any{
			"domainStrategy": "AsIs",
			"redirect":       rule.MaskDomain + ":443",
		})
		cfg.Outbounds = append(cfg.Outbounds, Outbound{
			Tag:      tag,
			Protocol: "freedom",
			Settings: maskSettings,
		})
		routing.Rules = append(routing.Rules, RoutingRule{
			Type:        "field",
			Domain:      []string{"domain:" + rule.SourceDomain},
			OutboundTag: tag,
		})
	}

	// Load balance across exit nodes
	if len(exitTags) > 0 {
		routing.Balancers = append(routing.Balancers, Balancer{
			Tag:      "exit-balancer",
			Selector: exitTags,
			Strategy: struct {
				Type string `json:"type"`
			}{Type: "leastPing"},
		})
		// Default route: balance across exit nodes
		routing.Rules = append(routing.Rules, RoutingRule{
			Type:        "field",
			BalancerTag: "exit-balancer",
		})
	}

	cfg.Routing = routing
	return cfg, nil
}

// HubConfigOptions holds all parameters needed to generate the hub config.
type HubConfigOptions struct {
	// Server identity
	ServerDomain   string // e.g., "heyearth.ru"
	PrivateKey     string // X25519 private key for Reality
	ShortIDs       []string

	// Ports
	VLESSPort     int // default 443
	SOCKS5Port    int // default 1080
	HTTPProxyPort int // default 8080

	// Exit nodes
	ExitNodes []ExitNode

	// Traffic masking
	MaskingRules []MaskingRule
}

func buildVLESSRealityInbound(opts HubConfigOptions) (*Inbound, error) {
	port := opts.VLESSPort
	if port == 0 {
		port = 443
	}

	settings, _ := json.Marshal(map[string]any{
		"clients": []map[string]any{},
		"decryption": "none",
	})

	streamSettings, _ := json.Marshal(map[string]any{
		"network":  "tcp",
		"security": "reality",
		"realitySettings": map[string]any{
			"dest":       opts.ServerDomain + ":443",
			"serverNames": []string{opts.ServerDomain},
			"privateKey": opts.PrivateKey,
			"shortIds":   opts.ShortIDs,
		},
	})

	return &Inbound{
		Tag:            "vless-reality",
		Port:           port,
		Protocol:       "vless",
		Settings:       settings,
		StreamSettings: streamSettings,
		Sniffing: &Sniffing{
			Enabled:      true,
			DestOverride: []string{"http", "tls", "quic"},
		},
	}, nil
}

func buildSOCKS5Inbound(port int) Inbound {
	if port == 0 {
		port = 1080
	}
	settings, _ := json.Marshal(map[string]any{
		"auth": "noauth",
		"udp":  true,
	})
	return Inbound{
		Tag:      "socks5-proxy",
		Port:     port,
		Listen:   "0.0.0.0",
		Protocol: "socks",
		Settings: settings,
		Sniffing: &Sniffing{
			Enabled:      true,
			DestOverride: []string{"http", "tls", "quic"},
		},
	}
}

func buildHTTPProxyInbound(port int) Inbound {
	if port == 0 {
		port = 8080
	}
	settings, _ := json.Marshal(map[string]any{
		"allowTransparent": false,
	})
	return Inbound{
		Tag:      "http-proxy",
		Port:     port,
		Listen:   "0.0.0.0",
		Protocol: "http",
		Settings: settings,
		Sniffing: &Sniffing{
			Enabled:      true,
			DestOverride: []string{"http", "tls"},
		},
	}
}

func buildExitNodeOutbound(node ExitNode) (*Outbound, error) {
	port := node.Port
	if port == 0 {
		port = 443
	}

	settings, _ := json.Marshal(map[string]any{
		"vnext": []map[string]any{
			{
				"address": node.IPAddress,
				"port":    port,
				"users": []map[string]any{
					{
						"id":         node.UUID,
						"encryption": "none",
						"flow":       "xtls-rprx-vision",
					},
				},
			},
		},
	})

	streamSettings, _ := json.Marshal(map[string]any{
		"network":  "tcp",
		"security": "reality",
		"realitySettings": map[string]any{
			"fingerprint": "chrome",
			"serverName":  node.IPAddress,
		},
	})

	return &Outbound{
		Tag:            node.Tag,
		Protocol:       "vless",
		Settings:       settings,
		StreamSettings: streamSettings,
	}, nil
}

// ToJSON serializes the config to formatted JSON.
func (c *XrayConfig) ToJSON() ([]byte, error) {
	return json.MarshalIndent(c, "", "  ")
}
