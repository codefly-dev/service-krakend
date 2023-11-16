package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"github.com/codefly-dev/cli/pkg/plugins/services"
	runtimev1 "github.com/codefly-dev/cli/proto/v1/services/runtime"
	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"
	"os"
)

// KrakendSettings will contain all the static information
// JSON -- yaml not working
type KrakendSettings struct {
	Port        int            `json:"port"`
	Group       []Forwarding   `json:"group"`
	ExtraConfig map[string]any `json:"extra_config"`
}

type Backend struct {
	URL   string   `json:"url"`
	Hosts []string `json:"hosts"`
}

type Forwarding struct {
	Target      string         `json:"target"`
	Backend     Backend        `json:"backend"`
	ExtraConfig map[string]any `json:"extra_config"`
}

// AuthValidatorKey for auth
const AuthValidatorKey = "auth/validator"

type AuthValidator struct {
	Alg      string   `json:"alg,omitempty"`
	Audience []string `json:"audience,omitempty"`
	JwkURL   string   `json:"jwk_url,omitempty"`
	Issuer   string   `json:"issuer,omitempty"`
}

func Protect(config *Forwarding) {
	if config.ExtraConfig == nil {
		config.ExtraConfig = make(map[string]any)
	}
	config.ExtraConfig[AuthValidatorKey] = AuthValidator{
		Alg:      "RS256",
		Audience: []string{"https://codefly.ai"},
		JwkURL:   "https://dev-4c24vdpgjj3eyqmy.us.auth0.com/.well-known/jwks.json",
		Issuer:   "https://dev-4c24vdpgjj3eyqmy.us.auth0.com/",
	}
}

type CorsPolicy struct {
	AllowOrigins     []string `json:"allow_origins,omitempty"`
	AllowMethods     []string `json:"allow_methods,omitempty"`
	AllowHeaders     []string `json:"allow_headers,omitempty"`
	ExposeHeaders    []string `json:"expose_headers,omitempty"`
	MaxAge           string   `json:"max_age,omitempty"`
	AllowCredentials bool     `json:"allow_credentials,omitempty"`
}

const CorsPolicyRouteKey = "github.com/devopsfaith/krakend-cors"
const CorsPolicyKey = "security/cors"

func Cors(key string) map[string]any {
	config := make(map[string]any)
	config[key] = CorsPolicy{
		AllowOrigins:  []string{"*"},
		AllowMethods:  []string{"GET", "POST", "PUT", "DELETE"},
		AllowHeaders:  []string{"Content-Type", "Origin", "Authorization", "Accept"},
		ExposeHeaders: []string{"Content-Length", "Content-Type"},
		MaxAge:        "12h",
	}
	return config
}

func gatewayTarget(r *configurations.RestRoute) string {
	return fmt.Sprintf("/%s/%s%s", r.Application, r.Service, r.Path)
}

func (p *Runtime) writeConfig(nms []*runtimev1.NetworkMapping) error {
	// Write the main config
	err := shared.Embed(config).Copy("templates/krakend.config", p.Local("config/krakend.tmpl"))
	if err != nil {
		return p.PluginLogger.Wrapf(err, "cannot copy config")
	}

	p.DebugMe("write config with known routes: %v", p.Routes)
	settings := KrakendSettings{Port: p.Port}
	// setup CORS configuration globally
	settings.ExtraConfig = Cors(CorsPolicyKey)

	for _, route := range p.Routes {
		nm, err := services.NetworkMappingForRoute(p.Context(), &route.RestRoute, nms)
		if err != nil {
			return p.PluginLogger.Wrapf(err, "cannot get network mapping for route")
		}
		var hosts []string
		for _, h := range nm.Addresses {
			hosts = append(hosts, fmt.Sprintf("http://%s", h))
		}
		fwd := Forwarding{
			Target: gatewayTarget(&route.RestRoute),
			Backend: Backend{
				URL:   route.Path,
				Hosts: hosts,
			},
		}
		if route.Extension.Protected {
			Protect(&fwd)
		}
		settings.Group = append(settings.Group, fwd)
		break
	}
	target := p.Local("config/settings/routing.json")
	content, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return p.PluginLogger.Wrapf(err, "cannot marshal settings")
	}
	err = os.WriteFile(target, content, 0o644)
	if err != nil {
		return p.PluginLogger.Wrapf(err, "cannot write settings to %s", target)
	}
	return nil
}

//go:embed templates/krakend.config
var config embed.FS
