package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/wool"
	"os"

	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
)

// KrakendSettings will contain all the static information
// JSON -- yaml not working
type KrakendSettings struct {
	Port      uint16               `json:"port"`
	RESTGroup []ForwardedRESTRoute `json:"rest_group,omitempty"`

	ExtraConfig map[string]any `json:"extra_config,omitempty"`
}

type ForwardedRESTRoute struct {
	Endpoint     string         `json:"endpoint"`
	Method       string         `json:"method"`
	InputHeaders []string       `json:"input_headers,omitempty"`
	Backend      Backend        `json:"backend"`
	ExtraConfig  map[string]any `json:"extra_config,omitempty"`
}

type ForwardedGRPCRoute struct {
	Endpoint string  `json:"endpoint"`
	Backend  Backend `json:"backend"`
}

type Backend struct {
	URLPattern string   `json:"url_pattern"`
	Hosts      []string `json:"hosts"`
}

type AuthValidator struct {
	Key           string
	Configuration any
}

// JWTAuthValidatorKey for auth
const JWTAuthValidatorKey = "auth/validator"

type JWTAuthValidator struct {
	Alg             string     `json:"alg,omitempty"`
	Audience        []string   `json:"audience,omitempty"`
	JwkURL          string     `json:"jwk_url,omitempty"`
	Cache           bool       `json:"cache,omitempty"`
	Roles           []string   `json:"roles,omitempty"`
	PropagateClaims [][]string `json:"propagate_claims,omitempty"`
}

type ModifierMartian struct {
	HeaderCopy HeaderCopy `json:"header.Copy"`
}

type HeaderCopy struct {
	Scope    []string `json:"scope"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	Modifier any      `json:"modifier,omitempty"`
}

type RegexModifier struct {
	Scope       []string `json:"scope"`
	Expression  string   `json:"expression"`
	Replacement string   `json:"replacement"`
}

const ModifierMartianKey = "modifier/martian"

func ProtectRestRoute(config *ForwardedRESTRoute, validators []*AuthValidator) error {
	if config.ExtraConfig == nil {
		config.ExtraConfig = make(map[string]any)
	}
	for _, validator := range validators {
		config.ExtraConfig[validator.Key] = validator.Configuration
	}
	return nil
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
	allowedHeaders = append(allowedHeaders, wool.Headers()...)
	return CorsPolicy{
		AllowOrigins:  []string{"*"},
		AllowMethods:  []string{"GET", "POST", "PUT", "DELETE"},
		AllowHeaders:  allowedHeaders,
		ExposeHeaders: []string{"Content-Length", "Content-Type"},
		MaxAge:        "12h",
	}
}

func gatewayRestTarget(r *resources.RestRouteGroup) string {
	return fmt.Sprintf("/%s/%s%s", r.Module, r.Service, r.Path)
}

func (s *Service) writeConfig(ctx context.Context, nms []*basev0.NetworkMapping, networkAccess *basev0.NetworkAccess) error {
	conf, err := s.createConfig(ctx, nms, networkAccess)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot create config")
	}
	target := s.Local("routing/config/settings/routing.json")
	err = os.WriteFile(target, conf, 0o644)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot write settings to %s", target)
	}
	return nil
}

func (s *Service) createConfig(ctx context.Context, otherNetworkMappings []*basev0.NetworkMapping, networkAccess *basev0.NetworkAccess) ([]byte, error) {
	// Write the main config
	err := shared.Embed(config).Copy("templates/krakend.config", s.Local("routing/config/krakend.tmpl"))
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot copy config")
	}

	settings := KrakendSettings{Port: s.port, ExtraConfig: make(map[string]any)}
	// setup CORS configuration globally
	settings.ExtraConfig[CorsPolicyKey] = Cors(CorsPolicyKey)

	for _, group := range s.RestRouteGroups {
		baseGroup := resources.UnwrapRestRouteGroup(group)

		nm, err := services.NetworkInstanceForRestRouteGroup(ctx, otherNetworkMappings, baseGroup, networkAccess)
		if err != nil {
			return nil, s.Wool.Wrapf(err, "cannot get network mapping for group")
		}

		s.Wool.Debug("exposing routes", wool.Field("group", baseGroup.ServiceUnique()), wool.Field("routes", group.Routes))
		for _, route := range group.Routes {
			if !route.Extension.Exposed {
				continue
			}
			fwd := NewRESTForwarding(gatewayRestTarget(baseGroup), resources.UnwrapRestRoute(route), nm.Address)
			if route.Extension.Protected {
				// fwd.InputHeaders = wool.Headers()

				err = ProtectRestRoute(&fwd, s.validators)
				if err != nil {
					return nil, s.Wool.Wrapf(err, "cannot create protected route without validator")
				}
			}
			settings.RESTGroup = append(settings.RESTGroup, fwd)
		}
	}
	var content []byte
	content, err = json.Marshal(settings)
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot marshal settings")
	}
	return content, nil
}

func (s *Service) writeOpenAPI(ctx context.Context, endpoints []*basev0.Endpoint) error {
	w := wool.Get(ctx).In("create open api")
	if s.restEndpoint == nil {
		return w.NewError("REST endpoint nil")
	}
	gateway := resources.EndpointFromProto(s.restEndpoint)

	combinator, err := resources.NewOpenAPICombinator(ctx, gateway, endpoints...)
	if err != nil {
		return w.Wrapf(err, "cannot create combinator")
	}
	combinator.WithDestination(s.openapiDestination)
	combinator.WithVersion(s.Base.Service.Version)

	s.Wool.Debug("rest routes groups", wool.SliceCountField(s.RestRouteGroups))

	for _, group := range s.RestRouteGroups {
		for _, route := range group.Routes {
			if !route.Extension.Exposed {
				continue
			}
			baseGroup := resources.UnwrapRestRouteGroup(group)
			s.Wool.Focus("adding route", wool.Field("group", baseGroup.ServiceUnique()), wool.Field("route", route.Path))
			combinator.Only(baseGroup.ServiceUnique(), baseGroup.Path, string(route.Method))
		}
	}
	restAPI, err := combinator.Combine(ctx)

	if err != nil {
		return w.Wrapf(err, "cannot combine open api")
	}

	s.restEndpoint.ApiDetails = resources.ToRestAPI(restAPI)

	s.Endpoints = []*basev0.Endpoint{s.restEndpoint}

	return nil
}

func NewRESTForwarding(target string, route *resources.RestRoute, host string) ForwardedRESTRoute {
	return ForwardedRESTRoute{
		Endpoint:     target,
		Method:       string(route.Method),
		InputHeaders: wool.Headers(),
		Backend: Backend{
			URLPattern: route.Path,
			Hosts:      []string{host},
		},
		ExtraConfig: make(map[string]any),
	}
}

func NewGRPCForwarding(target string, base *resources.GRPCRoute, hosts []string) ForwardedGRPCRoute {
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
