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
	configurations "github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
)

// KrakendSettings will contain all the static information
// JSON -- yaml not working
type KrakendSettings struct {
	Port      uint16               `json:"port"`
	RESTGroup []ForwardedRESTRoute `json:"rest_group"`

	ExtraConfig map[string]any `json:"extra_config"`
}

type ForwardedRESTRoute struct {
	Endpoint     string         `json:"endpoint"`
	Method       string         `json:"method"`
	InputHeaders []string       `json:"input_headers"`
	Backend      Backend        `json:"backend"`
	ExtraConfig  map[string]any `json:"extra_config"`
}

type ForwardedGRPCRoute struct {
	Endpoint string  `json:"endpoint"`
	Backend  Backend `json:"backend"`
}

type Backend struct {
	URLPattern string   `json:"url_pattern"`
	Hosts      []string `json:"hosts"`
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

func ProtectRestRoute(config *ForwardedRESTRoute, validator *AuthValidator) {
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

func gatewayRestTarget(r *configurations.RestRouteGroup) string {
	return fmt.Sprintf("/%s/%s%s", r.Application, r.Service, r.Path)
}

func (s *Service) writeConfig(ctx context.Context, nms []*basev0.NetworkMapping, scope basev0.NetworkScope) error {
	conf, err := s.createConfig(ctx, nms, scope)
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

func (s *Service) createConfig(ctx context.Context, otherNetworkMappings []*basev0.NetworkMapping, scope basev0.NetworkScope) ([]byte, error) {
	// Write the main config
	err := shared.Embed(config).Copy("templates/krakend.config", s.Local("config/krakend.tmpl"))
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot copy config")
	}

	settings := KrakendSettings{Port: s.port, ExtraConfig: make(map[string]any)}
	// setup CORS configuration globally
	settings.ExtraConfig[CorsPolicyKey] = Cors(CorsPolicyKey)

	for _, group := range s.RestRouteGroups {
		baseGroup := configurations.UnwrapRestRouteGroup(group)
		nm, err := services.NetworkInstanceForRestRouteGroup(ctx, baseGroup, scope, otherNetworkMappings)
		if err != nil {
			return nil, s.Wool.Wrapf(err, "cannot get network mapping for group")
		}
		s.Wool.Debug("exposing routes", wool.Field("group", baseGroup.ServiceUnique()), wool.Field("routes", group.Routes))
		for _, route := range group.Routes {
			if !route.Extension.Exposed {
				continue
			}
			fwd := NewRESTForwarding(gatewayRestTarget(baseGroup), configurations.UnwrapRestRoute(route), nm.Address)
			if route.Extension.Protected {
				fwd.InputHeaders = headers.UserHeaders()
				ProtectRestRoute(&fwd, s.validator)
			}
			settings.RESTGroup = append(settings.RESTGroup, fwd)
		}
	}
	var content []byte
	content, err = json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot marshal settings")
	}
	return content, nil
}

func (s *Service) writeOpenAPI(ctx context.Context, endpoints []*basev0.Endpoint) error {
	w := wool.Get(ctx).In("create open api")
	gateway := configurations.EndpointFromProto(s.restEndpoint)
	combinator, err := configurations.NewOpenAPICombinator(ctx, gateway, endpoints...)
	if err != nil {
		return w.Wrapf(err, "cannot create combinator")
	}
	combinator.WithDestination(s.openapiDestination)
	combinator.WithVersion(s.Base.Service.Version)
	for _, group := range s.RestRouteGroups {
		baseGroup := configurations.UnwrapRestRouteGroup(group)
		combinator.Only(baseGroup.ServiceUnique(), baseGroup.Path)
	}
	s.restEndpoint, err = combinator.Combine(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot combine open api")
	}
	s.Endpoints = []*basev0.Endpoint{s.restEndpoint}
	return nil
}

func NewRESTForwarding(target string, route *configurations.RestRoute, host string) ForwardedRESTRoute {
	return ForwardedRESTRoute{
		Endpoint: target,
		Method:   string(route.Method),
		Backend: Backend{
			URLPattern: route.Path,
			Hosts:      []string{host},
		},
		ExtraConfig: make(map[string]any),
	}
}

func NewGRPCForwarding(target string, base *configurations.GRPCRoute, hosts []string) ForwardedGRPCRoute {
	return ForwardedGRPCRoute{
		Endpoint: target,
		Backend: Backend{
			URLPattern: base.Route(),
			Hosts:      hosts,
		},
	}
}

//go:embed templates/krakend.config
var config embed.FS
