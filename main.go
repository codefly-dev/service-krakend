package main

import (
	"context"
	"embed"
	"fmt"
	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/configurations/headers"
	"github.com/codefly-dev/core/configurations/standards"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/runners"
	"github.com/codefly-dev/core/templates"
	"github.com/codefly-dev/core/wool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/configurations"
	agentv0 "github.com/codefly-dev/core/generated/go/services/agent/v0"
	"github.com/codefly-dev/core/shared"
)

// Agent version
var agent = shared.Must(configurations.LoadFromFs[configurations.Agent](shared.Embed(info)))

var requirements = builders.NewDependencies(agent.Name,
	builders.NewDependency("service.codefly.yaml"),
	builders.NewDependency("routing"),
)

type Settings struct {
	Debug bool `yaml:"debug"` // Developer only
}

var image = runners.DockerImage{Name: "devopsfaith/krakend", Tag: "latest"}

type Extension struct {
	Exposed   bool `yaml:"exposed"`
	Protected bool `yaml:"protected"`
}

// RestRouteGroup extends the concept of RestRouteGroup to add API Gateway concepts
type RestRouteGroup = configurations.ExtendedRestRouteGroup[Extension]

// RestRoute extends the concept of RestRoute to add API Gateway concepts
type RestRoute = configurations.ExtendedRestRoute[Extension]

type Service struct {
	*services.Base

	// Access
	Port int

	RoutesLocation string
	RouteGroups    []*RestRouteGroup

	Validator *AuthValidator

	// Settings
	*Settings
	endpoint *basev0.Endpoint
}

func (s *Service) Setup() {
	s.RoutesLocation = s.Local("routing")
}

func (s *Service) GetAgentInformation(ctx context.Context, _ *agentv0.AgentInformationRequest) (*agentv0.AgentInformation, error) {

	rm, err := templates.ApplyTemplateFrom(ctx, shared.Embed(readme), "templates/agent/README.md", s.Information)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &agentv0.AgentInformation{
		RuntimeRequirements: []*agentv0.Runtime{
			{Type: agentv0.Runtime_DOCKER},
		},
		Capabilities: []*agentv0.Capability{
			{Type: agentv0.Capability_BUILDER},
			{Type: agentv0.Capability_RUNTIME},
		},
		Protocols: []*agentv0.Protocol{
			{Type: agentv0.Protocol_HTTP},
		},
		ReadMe: rm,
	}, nil
}

func NewService() *Service {
	return &Service{
		Base:     services.NewServiceBase(context.Background(), agent.Of(configurations.ServiceAgent)),
		Settings: &Settings{},
	}
}

// LoadRoutes from routing configuration folder
func (s *Service) LoadRoutes(ctx context.Context) error {
	loader, err := configurations.NewExtendedRouteLoader[Extension](ctx, s.RoutesLocation)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot create route loader")
	}
	err = loader.Load(ctx)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot load routes")
	}
	s.RouteGroups = loader.Groups()
	s.Wool.Debug("known route groups", wool.SliceCountField(s.RouteGroups))
	if err != nil {
		return s.Wool.Wrapf(err, "cannot load routing")
	}
	return nil
}

const Auth0 = "auth0"

func (s *Service) CreateValidator(ctx context.Context, provs []*basev0.ProviderInformation) (*AuthValidator, error) {
	// Validator
	for _, prov := range s.Configuration.ProviderDependencies {
		validator, err := configurations.FindProjectProvider(prov, provs)
		if err != nil {
			return nil, s.Wool.Wrapf(err, "cannot get validator")
		}
		switch validator.Name {
		case Auth0:
			return &AuthValidator{
				Alg:             "RS256",
				Audience:        []string{validator.Data["AUTH0_AUDIENCE"]},
				JwkURL:          fmt.Sprintf("%s/.well-known/jwks.json", validator.Data["AUTH0_ISSUER_BASE_URL"]),
				PropagateClaims: [][]string{{"sub", headers.UserID}},
				Cache:           true,
			}, nil
		}
	}
	return nil, fmt.Errorf("unknown validator")
}

func (s *Service) LoadEndpoints(ctx context.Context) error {
	defer s.Wool.Catch()
	s.Endpoints = []*basev0.Endpoint{}
	var err error
	for _, endpoint := range s.Configuration.Endpoints {
		var api *basev0.Endpoint
		endpoint.Application = s.Configuration.Application
		endpoint.Service = s.Configuration.Name
		if shared.FileExists(s.Local("swagger.json")) {
			api, err = configurations.NewRestAPIFromOpenAPI(ctx, &configurations.Endpoint{Name: standards.REST, API: standards.REST, Visibility: endpoint.Visibility}, s.Local("swagger.json"))
			if err != nil {
				return s.Wool.Wrapf(err, "cannot  create rest endpoint")
			}
		} else {
			api, err = configurations.NewRestAPI(ctx, &configurations.Endpoint{Name: standards.REST, API: standards.REST, Visibility: endpoint.Visibility})
			if err != nil {
				return s.Wool.Wrapf(err, "cannot  create rest endpoint")
			}
		}
		s.endpoint = api
		s.Endpoints = append(s.Endpoints, s.endpoint)
	}
	return nil
}

func main() {
	agents.Register(
		services.NewServiceAgent(agent.Of(configurations.ServiceAgent), NewService()),
		services.NewBuilderAgent(agent.Of(configurations.RuntimeServiceAgent), NewBuilder()),
		services.NewRuntimeAgent(agent.Of(configurations.BuilderServiceAgent), NewRuntime()))
}

//go:embed agent.codefly.yaml
var info embed.FS

//go:embed templates/agent
var readme embed.FS
