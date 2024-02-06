package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"github.com/codefly-dev/core/configurations/headers"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/wool"
	"os"

	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"
)

// KrakendSettings will contain all the static information
// JSON -- yaml not working
type KrakendSettings struct {
	Port        int                 `json:"port"`
	Group       []ForwardedEndpoint `json:"group"`
	ExtraConfig map[string]any      `json:"extra_config"`
}

type ForwardedEndpoint struct {
	Target       string         `json:"target"`
	Method       string         `json:"method"`
	InputHeaders []string       `json:"input_headers"`
	Backend      Backend        `json:"backend"`
	ExtraConfig  map[string]any `json:"extra_config"`
}

type Backend struct {
	URL   string   `json:"url"`
	Hosts []string `json:"hosts"`
}

type InputHeaders struct {
	Headers []string `json:"headers"`
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

func Protect(config *ForwardedEndpoint, validator *AuthValidator) {
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
	allowedHeaders := []string{"Content-Type", "Origin", "Authorization", "Accept"}
	allowedHeaders = append(allowedHeaders, headers.UserHeaders()...)
	return CorsPolicy{
		AllowOrigins:  []string{"*"},
		AllowMethods:  []string{"GET", "POST", "PUT", "DELETE"},
		AllowHeaders:  allowedHeaders,
		ExposeHeaders: []string{"Content-Length", "Content-Type"},
		MaxAge:        "12h",
	}
}

func gatewayTarget(r *configurations.RestRouteGroup) string {
	return fmt.Sprintf("/%s/%s%s", r.Application, r.Service, r.Path)
}

func (s *Service) writeConfig(ctx context.Context, nms []*basev0.NetworkMapping) error {
	conf, err := s.createConfig(ctx, nms, true)
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

func (s *Service) createConfig(ctx context.Context, otherNetworkMappings []*basev0.NetworkMapping, withIndent bool) ([]byte, error) {
	// Write the main config
	err := shared.Embed(config).Copy("templates/krakend.config", s.Local("config/krakend.tmpl"))
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot copy config")
	}

	settings := KrakendSettings{Port: s.Port, ExtraConfig: make(map[string]any)}
	// setup CORS configuration globally
	settings.ExtraConfig[CorsPolicyKey] = Cors(CorsPolicyKey)

	// TODO FIX
	//settings.ExtraConfig[TelemetryKey] = TelemetryLogging{Level: "CRITICAL", StdOut: true}

	for _, group := range s.RouteGroups {
		baseGroup := configurations.UnwrapRouteGroup(group)
		nm, err := services.NetworkMappingForRestRouteGroup(ctx, baseGroup, otherNetworkMappings)
		if err != nil {
			return nil, s.Wool.Wrapf(err, "cannot get network mapping for group")
		}
		var hosts []string
		for _, h := range nm.Addresses {
			hosts = append(hosts, fmt.Sprintf("http://%s", h))
		}
		for _, route := range group.Routes {
			if !route.Extension.Exposed {
				continue
			}
			fwd := NewForwarding(gatewayTarget(baseGroup), configurations.UnwrapRoute(route), hosts)
			if route.Extension.Protected {
				fwd.InputHeaders = headers.UserHeaders()
				Protect(&fwd, s.Validator)
			}
			settings.Group = append(settings.Group, fwd)
		}
	}
	s.Wool.Debug("exposing routes", wool.SliceCountField(s.RouteGroups))
	var content []byte
	if withIndent {
		content, err = json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return nil, s.Wool.Wrapf(err, "cannot marshal settings")
		}
	} else {
		content, err = json.Marshal(settings)
	}
	return content, nil
}

func (s *Service) writeOpenAPI(ctx context.Context, endpoints []*basev0.Endpoint) error {
	w := wool.Get(ctx).In("create open api")
	gateway := configurations.EndpointFromProto(s.endpoint)
	combinator, err := configurations.NewOpenAPICombinator(ctx, gateway, endpoints...)
	if err != nil {
		return w.Wrapf(err, "cannot create combinator")
	}
	combinator.WithDestination(s.Local("swagger.json"))
	combinator.WithVersion(s.Configuration.Version)
	for _, group := range s.RouteGroups {
		baseGroup := configurations.UnwrapRouteGroup(group)
		combinator.Only(baseGroup.ServiceUnique(), baseGroup.Path)
	}
	s.endpoint, err = combinator.Combine(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot combine open api")
	}
	s.Endpoints = []*basev0.Endpoint{s.endpoint}
	return nil
}

func NewForwarding(target string, route *configurations.RestRoute, hosts []string) ForwardedEndpoint {
	return ForwardedEndpoint{
		Target: target,
		Method: string(route.Method),
		Backend: Backend{
			URL:   route.Path,
			Hosts: hosts,
		},
		ExtraConfig: make(map[string]any),
	}
}

//go:embed templates/krakend.config
var config embed.FS
