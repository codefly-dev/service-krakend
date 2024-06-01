package main

import (
	"context"
	"embed"
	"fmt"
	"github.com/codefly-dev/core/builders"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/standards/headers"
	"github.com/codefly-dev/core/templates"
	"github.com/codefly-dev/core/wool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/agents/services"
	agentv0 "github.com/codefly-dev/core/generated/go/services/agent/v0"
	configurations "github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
)

// Agent version
var agent = shared.Must(configurations.LoadFromFs[configurations.Agent](shared.Embed(info)))

var requirements = builders.NewDependencies(agent.Name,
	builders.NewDependency("service.codefly.yaml"),
	builders.NewDependency("routing"),
)

type Settings struct {
	AuthProvider string `yaml:"auth-provider,omitempty"`
}

var image = &configurations.DockerImage{Name: "devopsfaith/krakend", Tag: "2.6"}

type Extension struct {
	Exposed   bool `yaml:"exposed"`
	Protected bool `yaml:"protected"`
}

// RestRoute extends the concept of RestRoute to add API Gateway concepts
type RestRoute = configurations.ExtendedRestRoute[Extension]

// RestRouteGroup extends the concept of RestRouteGroup to add API Gateway concepts
type RestRouteGroup = configurations.ExtendedRestRouteGroup[Extension]

type Service struct {
	*services.Base

	// Access
	port uint16

	restRoutesLocation string

	RestRouteGroups []*RestRouteGroup

	validator *AuthValidator

	// Settings
	*Settings

	restEndpoint       *basev0.Endpoint
	openapiDestination string
}

func (s *Service) Setup() {
	s.restRoutesLocation = s.Local("routing/rest")
}

func (s *Service) GetAgentInformation(ctx context.Context, _ *agentv0.AgentInformationRequest) (*agentv0.AgentInformation, error) {

	rm, err := templates.ApplyTemplateFrom(ctx, shared.Embed(readme), "templates/agent/README.md", s.Information)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &agentv0.AgentInformation{
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
	loader, err := configurations.NewExtendedRestRouteLoader[Extension](ctx, s.restRoutesLocation)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot create route loader")
	}
	err = loader.Load(ctx)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot load routes")
	}
	s.RestRouteGroups = loader.Groups()
	s.Wool.Debug("known REST route groups", wool.SliceCountField(s.RestRouteGroups))
	return nil
}

func (s *Service) CreateValidator(ctx context.Context, conf *basev0.Configuration) (*AuthValidator, error) {
	// Extract the data
	if !configurations.HasConfigurationInformation(ctx, conf, s.Settings.AuthProvider) {
		return nil, nil
	}
	audience, err := configurations.GetConfigurationValue(ctx, conf, s.Settings.AuthProvider, "AUDIENCE")
	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot get audience")
	}
	issuerBaseURL, err := configurations.GetConfigurationValue(ctx, conf, s.Settings.AuthProvider, "ISSUER_BASE_URL")

	if err != nil {
		return nil, s.Wool.Wrapf(err, "cannot get issuer base url")
	}
	return &AuthValidator{
		Alg:             "RS256",
		Audience:        []string{audience},
		JwkURL:          fmt.Sprintf("%s/.well-known/jwks.json", issuerBaseURL),
		PropagateClaims: [][]string{{"sub", headers.UserAuthID}},
		Cache:           true,
	}, nil
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
