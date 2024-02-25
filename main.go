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

	Watch bool `yaml:"watch"`

	AuthProvider string `yaml:"auth-provider"`
}

var image = runners.DockerImage{Name: "devopsfaith/krakend", Tag: "latest"}

type Extension struct {
	Exposed   bool `yaml:"exposed"`
	Protected bool `yaml:"protected"`
}

// RestRoute extends the concept of RestRoute to add API Gateway concepts
type RestRoute = configurations.ExtendedRestRoute[Extension]

// RestRouteGroup extends the concept of RestRouteGroup to add API Gateway concepts
type RestRouteGroup = configurations.ExtendedRestRouteGroup[Extension]

// GRPCRoute extends the concept of GRPCRoute to add API Gateway concepts
type GRPCRoute = configurations.ExtendedGRPCRoute[Extension]

type Service struct {
	*services.Base

	// Access
	Port int

	RestRoutesLocation string
	GPRCRoutesLocation string

	RestRouteGroups []*RestRouteGroup

	//GRPCRoutes []*GRPCRoute

	Validator *AuthValidator

	// Settings
	*Settings
	endpoint *basev0.Endpoint

	openapi string
}

func (s *Service) Setup() {
	s.RestRoutesLocation = s.Local("routing/rest")
	//s.GPRCRoutesLocation = s.Local("routing/grpc")
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

// LoadRestRoutes from routing configuration folder
func (s *Service) LoadRestRoutes(ctx context.Context) error {
	loader, err := configurations.NewExtendedRestRouteLoader[Extension](ctx, s.RestRoutesLocation)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot create route loader")
	}
	err = loader.Load(ctx)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot load routes")
	}
	s.RestRouteGroups = loader.Groups()
	s.Wool.Debug("known REST route groups", wool.SliceCountField(s.RestRouteGroups))
	if err != nil {
		return s.Wool.Wrapf(err, "cannot load routing")
	}
	return nil
}

// LoadGRPCRoutes from routing configuration folder
//func (s *Service) LoadGRPCRoutes(ctx context.Context) error {
//	loader, err := configurations.NewExtendedGRPCRouteLoader[Extension](ctx, s.GPRCRoutesLocation)
//	if err != nil {
//		return s.Wool.Wrapf(err, "cannot create route loader")
//	}
//	err = loader.Load(ctx)
//	if err != nil {
//		return s.Wool.Wrapf(err, "cannot load routes")
//	}
//	s.GRPCRoutes = loader.All()
//	s.Wool.Debug("known gRPC route groups", wool.SliceCountField(s.GRPCRoutes))
//	if err != nil {
//		return s.Wool.Wrapf(err, "cannot load routing")
//	}
//	return nil
//}

func (s *Service) CreateValidator(ctx context.Context, infos []*basev0.ProviderInformation) (*AuthValidator, error) {
	// Validator
	for _, prov := range s.Configuration.ProviderDependencies {
		validator, err := configurations.FindProjectProvider(prov, infos)
		if err != nil {
			return nil, s.Wool.Wrapf(err, "cannot get validator")
		}
		return &AuthValidator{
			Alg:             "RS256",
			Audience:        []string{validator.Data["AUDIENCE"]},
			JwkURL:          fmt.Sprintf("%s/.well-known/jwks.json", validator.Data["ISSUER_BASE_URL"]),
			PropagateClaims: [][]string{{"sub", headers.UserAuthID}},
			Cache:           true,
		}, nil
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
