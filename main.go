package main

import (
	"context"
	"embed"
	"fmt"
	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/configurations/standards"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/runners"
	"github.com/codefly-dev/core/templates"
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

var requirements = &builders.Dependency{Components: []string{"routing"}}

type Settings struct {
	Debug  bool `yaml:"debug"`  // Developer only
	Public bool `yaml:"public"` // Accessible to external users
}

var image = runners.DockerImage{Name: "devopsfaith/krakend", Tag: "latest"}

type Service struct {
	*services.Base

	endpoint *basev0.Endpoint

	// Access
	Port int

	Routes         []*RestRoute
	RoutesLocation string
	Validator      *AuthValidator

	// Settings
	*Settings
}

func (s *Service) GetAgentInformation(ctx context.Context, _ *agentv0.AgentInformationRequest) (*agentv0.AgentInformation, error) {

	readme, err := templates.ApplyTemplateFrom(shared.Embed(readme), "templates/agent/README.md", s.Information)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &agentv0.AgentInformation{
		RuntimeRequirements: []*agentv0.Runtime{
			{Type: agentv0.Runtime_DOCKER},
		},
		Capabilities: []*agentv0.Capability{
			{Type: agentv0.Capability_FACTORY},
			{Type: agentv0.Capability_RUNTIME},
		},
		Protocols: []*agentv0.Protocol{
			{Type: agentv0.Protocol_HTTP},
		},
		ReadMe: readme,
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
	var err error
	s.Routes, err = configurations.LoadApplicationExtendedRoutes[Auth](ctx, s.RoutesLocation)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot load routing")
	}
	return nil
}

func (s *Service) LoadEndpoint(ctx context.Context) error {
	visibility := configurations.VisibilityApplication
	if s.Settings.Public {
		visibility = configurations.VisibilityPublic
	}
	var err error
	if shared.FileExists(s.Local("swagger.json")) {
		s.endpoint, err = configurations.NewRestAPIFromOpenAPI(ctx, &configurations.Endpoint{Name: standards.REST, API: standards.REST, Visibility: visibility}, s.Local("swagger.json"))
		if err != nil {
			return s.Wool.Wrapf(err, "cannot  create rest endpoint")
		}
	} else {
		s.endpoint, err = configurations.NewRestAPI(ctx, &configurations.Endpoint{Name: standards.REST, API: standards.REST, Visibility: visibility})
		if err != nil {
			return s.Wool.Wrapf(err, "cannot  create rest endpoint")
		}
	}
	s.endpoint.Application = s.Configuration.Application
	s.endpoint.Service = s.Configuration.Name
	s.Endpoints = []*basev0.Endpoint{s.endpoint}
	return nil
}

const Auth0 = "auth0"

func (s *Service) CreateValidator(ctx context.Context, provs []*basev0.ProviderInformation) (*AuthValidator, error) {
	// Validator
	for _, prov := range s.Configuration.ProviderDependencies {
		validator, err := configurations.GetProjectProvider(prov, provs)
		if err != nil {
			return nil, s.Wool.Wrapf(err, "cannot get validator")
		}
		switch validator.Name {
		case Auth0:
			return &AuthValidator{
				Alg:             "RS256",
				Audience:        []string{validator.Data["AUTH0_AUDIENCE"]},
				JwkURL:          fmt.Sprintf("%s/.well-known/jwks.json", validator.Data["AUTH0_ISSUER_BASE_URL"]),
				PropagateClaims: [][]string{{"sub", "X-User-ID"}},
				Cache:           true,
			}, nil
		}
	}
	return nil, fmt.Errorf("unknown validator")
}

func main() {
	agents.Register(
		services.NewServiceAgent(agent.Of(configurations.ServiceAgent), NewService()),
		services.NewFactoryAgent(agent.Of(configurations.RuntimeServiceAgent), NewFactory()),
		services.NewRuntimeAgent(agent.Of(configurations.FactoryServiceAgent), NewRuntime()))
}

//go:embed agent.codefly.yaml
var info embed.FS

//go:embed templates/agent
var readme embed.FS
