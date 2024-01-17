package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"

	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/configurations"
	runtimev0 "github.com/codefly-dev/core/generated/go/services/runtime/v0"
	"github.com/codefly-dev/core/shared"
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

type InputHeaders struct {
	Headers []string `json:"headers"`
}

type Forwarding struct {
	Target       string         `json:"target"`
	InputHeaders []string       `json:"input_headers"`
	Backend      Backend        `json:"backend"`
	ExtraConfig  map[string]any `json:"extra_config"`
}

// AuthValidatorKey for auth
const AuthValidatorKey = "auth/validator"

type AuthValidator struct {
	Alg             string     `json:"alg,omitempty"`
	Audience        []string   `json:"audience,omitempty"`
	JwkURL          string     `json:"jwk_url,omitempty"`
	Cache           bool       `json:"cache,omitempty"`
	Roles           []string   `json:"roles,omitempty"`
	PropagateClaims [][]string `json:"propagate_claims,omitempty"`
}

func Protect(config *Forwarding, validator *AuthValidator) {
	config.ExtraConfig[AuthValidatorKey] = *validator
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

func Cors(key string) CorsPolicy {
	return CorsPolicy{
		AllowOrigins:  []string{"*"},
		AllowMethods:  []string{"GET", "POST", "PUT", "DELETE"},
		AllowHeaders:  []string{"Content-Type", "Origin", "Authorization", "Accept"},
		ExposeHeaders: []string{"Content-Length", "Content-Type"},
		MaxAge:        "12h",
	}
}

func gatewayTarget(r *configurations.RestRoute) string {
	return fmt.Sprintf("/%s/%s%s", r.Application, r.Service, r.Path)
}

func (s *Service) writeConfig(ctx context.Context, nms []*runtimev0.NetworkMapping) error {
	conf, err := s.createConfig(ctx, nms)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot create config")
	}
	target := s.Local("config/settings/routing.json")
	err = os.WriteFile(target, conf, 0o644)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot write settings to %s", target)
	}
	return nil
}

const TelemetryKey = "telemetry/logging"

type TelemetryLogging struct {
	Level  string `json:"level,omitempty"`
	StdOut bool   `json:"stdout,omitempty"`
}

func (s *Service) createConfig(ctx context.Context, nms []*runtimev0.NetworkMapping) ([]byte, error) {
	// Write the main config
	err := shared.Embed(config).Copy("templates/krakend.config", s.Local("config/krakend.tmpl"))
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot copy config")
	}

	settings := KrakendSettings{Port: s.Port, ExtraConfig: make(map[string]any)}
	// setup CORS configuration globally
	settings.ExtraConfig[CorsPolicyKey] = Cors(CorsPolicyKey)
	settings.ExtraConfig[TelemetryKey] = TelemetryLogging{Level: "DEBUG", StdOut: true}

	for _, route := range s.Routes {
		nm, err := services.NetworkMappingForRoute(ctx, &route.RestRoute, nms)
		if err != nil {
			return nil, s.Wool.Wrapf(err, "cannot get network mapping for route")
		}
		var hosts []string
		for _, h := range nm.Addresses {
			hosts = append(hosts, fmt.Sprintf("http://%s", h))
		}
		fwd := NewForwarding(gatewayTarget(&route.RestRoute), route.Path, hosts)
		if route.Extension.Protected {
			fwd.InputHeaders = []string{"*"}
			Protect(&fwd, s.Validator)
		}
		settings.Group = append(settings.Group, fwd)
		break
	}

	content, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot marshal settings")
	}
	return content, nil
}

func NewForwarding(target string, path string, hosts []string) Forwarding {
	return Forwarding{
		Target: target,
		Backend: Backend{
			URL:   path,
			Hosts: hosts,
		},
		ExtraConfig: make(map[string]any),
	}
}

//go:embed templates/krakend.config
var config embed.FS
